package snapshotcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCacheNotReady       = errors.New("snapshotcache: cache is not ready")
	ErrRefreshInProgress   = errors.New("snapshotcache: refresh already in progress")
	ErrCacheAlreadyStarted = errors.New("snapshotcache: cache is already started")
	ErrCacheStopped        = errors.New("snapshotcache: cache is stopped")
)

type Cache[T any] struct {
	opts    Options[T]
	indexes map[string]Index[T]

	stateMu sync.RWMutex
	state   *state[T]

	statusMu sync.RWMutex
	status   Status

	refreshMu   sync.Mutex
	refreshing  atomic.Bool
	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	stopOnce    sync.Once
	stopCh      chan struct{}
	doneCh      chan struct{}
	bgCancel    context.CancelFunc
	refreshWG   sync.WaitGroup
}

type state[T any] struct {
	snapshot *Snapshot[T]
	indexes  map[string]indexState
}

type indexState struct {
	unique bool
	values map[string][]int
}

func New[T any](opts Options[T]) (*Cache[T], error) {
	if opts.Name == "" {
		return nil, errors.New("snapshotcache: name is required")
	}
	if opts.Source == nil {
		return nil, errors.New("snapshotcache: source is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SourceName == "" {
		opts.SourceName = opts.Name
	}
	if opts.RefreshInterval == 0 {
		opts.RefreshInterval = DefaultRefreshInterval
	}
	indexes := make(map[string]Index[T], len(opts.Indexes))
	for _, index := range opts.Indexes {
		if index.Name == "" {
			return nil, errors.New("snapshotcache: index name is required")
		}
		if index.Key == nil {
			return nil, fmt.Errorf("snapshotcache: index %s key func is required", index.Name)
		}
		if _, ok := indexes[index.Name]; ok {
			return nil, fmt.Errorf("snapshotcache: duplicate index name %s", index.Name)
		}
		indexes[index.Name] = index
	}
	return &Cache[T]{
		opts:    opts,
		indexes: indexes,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}, nil
}

func (c *Cache[T]) Start(ctx context.Context) error {
	if c == nil {
		return errors.New("snapshotcache: cache is nil")
	}
	c.lifecycleMu.Lock()
	if c.stopped {
		c.lifecycleMu.Unlock()
		return ErrCacheStopped
	}
	if c.started {
		c.lifecycleMu.Unlock()
		return ErrCacheAlreadyStarted
	}
	c.started = true
	c.lifecycleMu.Unlock()
	loadCtx := ctx
	cancel := func() {}
	if c.opts.InitialLoadTimeout > 0 {
		loadCtx, cancel = context.WithTimeout(ctx, c.opts.InitialLoadTimeout)
	}
	defer cancel()
	if err := c.initialLoad(loadCtx); err != nil {
		c.lifecycleMu.Lock()
		c.started = false
		c.lifecycleMu.Unlock()
		return err
	}
	if c.opts.RefreshInterval > 0 {
		bgCtx, cancel := context.WithCancel(context.Background())
		c.lifecycleMu.Lock()
		c.bgCancel = cancel
		c.lifecycleMu.Unlock()
		go c.refreshLoop(bgCtx)
	}
	return nil
}

func (c *Cache[T]) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.lifecycleMu.Lock()
	if !c.started {
		c.lifecycleMu.Unlock()
		return nil
	}
	c.stopped = true
	refreshInterval := c.opts.RefreshInterval
	bgCancel := c.bgCancel
	c.lifecycleMu.Unlock()
	if bgCancel != nil {
		bgCancel()
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	select {
	case <-c.doneCh:
	case <-ctx.Done():
		return ctx.Err()
	default:
		if refreshInterval <= 0 {
			return nil
		}
		select {
		case <-c.doneCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.waitBackgroundRefreshes(ctx)
}

func (c *Cache[T]) Refresh(ctx context.Context) error {
	if c == nil {
		return errors.New("snapshotcache: cache is nil")
	}
	c.lifecycleMu.Lock()
	stopped := c.stopped
	c.lifecycleMu.Unlock()
	if stopped {
		return ErrCacheStopped
	}
	return c.refresh(ctx)
}

func (c *Cache[T]) refresh(ctx context.Context) error {
	refreshCtx := ctx
	cancel := func() {}
	if c.opts.RefreshTimeout > 0 {
		refreshCtx, cancel = context.WithTimeout(ctx, c.opts.RefreshTimeout)
	}
	defer cancel()
	err := c.refreshFromSource(refreshCtx)
	if err != nil {
		c.setLastError(err)
	}
	return err
}

func (c *Cache[T]) waitBackgroundRefreshes(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		c.refreshWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Cache[T]) Snapshot() *Snapshot[T] {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.state == nil || c.state.snapshot == nil {
		return nil
	}
	return cloneSnapshot(c.state.snapshot)
}

func (c *Cache[T]) Get(indexName string, key ...string) (T, bool) {
	var zero T
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.state == nil {
		return zero, false
	}
	index, ok := c.state.indexes[indexName]
	if !ok {
		return zero, false
	}
	positions := index.values[joinKey(key)]
	if len(positions) == 0 {
		return zero, false
	}
	return c.state.snapshot.Items[positions[0]], true
}

func (c *Cache[T]) List(query Query[T]) []T {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.state == nil || c.state.snapshot == nil {
		return nil
	}
	candidates := c.candidatePositionsLocked(query)
	out := make([]T, 0, len(candidates))
	for _, pos := range candidates {
		item := c.state.snapshot.Items[pos]
		if c.matchesAllLocked(item, query.Filters) {
			out = append(out, item)
		}
	}
	return out
}

func (c *Cache[T]) Status() Status {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *Cache[T]) initialLoad(ctx context.Context) error {
	next, sourceFetched, err := c.loadFromSource(ctx)
	if err == nil {
		c.publish(next, nil)
		return nil
	}
	if c.opts.Startup.FallbackToFile && c.opts.FileStore != nil {
		snapshot, loadErr := c.opts.FileStore.Load(ctx)
		if loadErr == nil {
			snapshot.Name = c.opts.Name
			snapshot.LoadedFrom = LoadedFromFile
			if snapshot.SourceName == "" {
				snapshot.SourceName = c.opts.SourceName
			}
			next, buildErr := c.buildState(snapshot.Items, snapshot.LoadedFrom)
			if buildErr != nil {
				return buildErr
			}
			next.snapshot.LoadedAt = snapshot.LoadedAt
			next.snapshot.ContentHash = snapshot.ContentHash
			c.publish(next, err)
			return nil
		}
	}
	if sourceFetched {
		return err
	}
	if c.opts.Startup.FailIfNoSnapshot {
		return err
	}
	next, buildErr := c.buildState(nil, LoadedFromEmpty)
	if buildErr != nil {
		return buildErr
	}
	c.publish(next, err)
	return nil
}

func (c *Cache[T]) refreshFromSource(ctx context.Context) error {
	if !c.refreshing.CompareAndSwap(false, true) {
		return ErrRefreshInProgress
	}
	c.setRefreshing(true)
	c.refreshMu.Lock()
	defer func() {
		c.refreshMu.Unlock()
		c.setRefreshing(false)
		c.refreshing.Store(false)
	}()
	next, _, err := c.loadFromSource(ctx)
	if err != nil {
		return err
	}
	c.publish(next, nil)
	return nil
}

func (c *Cache[T]) loadFromSource(ctx context.Context) (*state[T], bool, error) {
	items, err := c.opts.Source.Fetch(ctx)
	if err != nil {
		return nil, false, err
	}
	next, err := c.buildState(items, LoadedFromSource)
	if err != nil {
		return nil, true, err
	}
	if c.opts.FileStore != nil {
		if err := c.opts.FileStore.Save(ctx, next.snapshot); err != nil {
			return nil, true, err
		}
	}
	return next, true, nil
}

func (c *Cache[T]) buildState(items []T, loadedFrom string) (*state[T], error) {
	copied := make([]T, len(items))
	copy(copied, items)
	hash, err := contentHash(copied)
	if err != nil {
		return nil, err
	}
	indexes := make(map[string]indexState, len(c.indexes))
	for _, definition := range c.indexes {
		idx := indexState{unique: definition.Unique, values: make(map[string][]int)}
		for pos, item := range copied {
			key := definition.Key(item)
			keyText := joinKey(key)
			if definition.Unique && len(idx.values[keyText]) > 0 {
				return nil, fmt.Errorf("snapshotcache: duplicate key %q for unique index %s", strings.Join(key, ","), definition.Name)
			}
			idx.values[keyText] = append(idx.values[keyText], pos)
		}
		indexes[definition.Name] = idx
	}
	return &state[T]{
		snapshot: &Snapshot[T]{
			Name:        c.opts.Name,
			Items:       copied,
			LoadedAt:    c.opts.Now(),
			SourceName:  c.opts.SourceName,
			LoadedFrom:  loadedFrom,
			ContentHash: hash,
		},
		indexes: indexes,
	}, nil
}

func (c *Cache[T]) publish(next *state[T], cause error) {
	c.stateMu.Lock()
	c.state = next
	c.stateMu.Unlock()
	lastErr := ""
	if cause != nil {
		lastErr = cause.Error()
	}
	c.statusMu.Lock()
	c.status.Ready = true
	c.status.ItemCount = len(next.snapshot.Items)
	c.status.LoadedAt = next.snapshot.LoadedAt
	c.status.SourceName = next.snapshot.SourceName
	c.status.LoadedFrom = next.snapshot.LoadedFrom
	c.status.ContentHash = next.snapshot.ContentHash
	c.status.LastError = lastErr
	c.statusMu.Unlock()
}

func (c *Cache[T]) setLastError(err error) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	if err == nil {
		c.status.LastError = ""
		return
	}
	c.status.LastError = err.Error()
}

func (c *Cache[T]) setRefreshing(refreshing bool) {
	c.statusMu.Lock()
	c.status.Refreshing = refreshing
	c.statusMu.Unlock()
}

func (c *Cache[T]) refreshLoop(ctx context.Context) {
	defer close(c.doneCh)
	if c.opts.RandomStartDelay > 0 {
		timer := time.NewTimer(boundedRandomStartDelay(c.opts.RandomStartDelay))
		select {
		case <-timer.C:
		case <-c.stopCh:
			timer.Stop()
			return
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
	ticker := time.NewTicker(c.opts.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if c.refreshing.Load() {
				continue
			}
			c.refreshWG.Add(1)
			go func() {
				defer c.refreshWG.Done()
				_ = c.refresh(ctx)
			}()
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func boundedRandomStartDelay(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	if max > 15*time.Second {
		max = 15 * time.Second
	}
	return time.Duration(rand.Int64N(int64(max) + 1))
}

func (c *Cache[T]) candidatePositionsLocked(query Query[T]) []int {
	total := 0
	if c.state != nil && c.state.snapshot != nil {
		total = len(c.state.snapshot.Items)
	}
	best := map[int]bool(nil)
	for _, filter := range query.Filters {
		switch filter.Kind {
		case FilterEq:
			positions, ok := c.lookupIndexLocked(filter.IndexName, joinKey(filter.Key))
			if !ok {
				continue
			}
			best = positionsToSet(positions)
			return setToSortedSlice(best)
		case FilterIn:
			var positions []int
			for _, key := range filter.Keys {
				if found, ok := c.lookupIndexLocked(filter.IndexName, joinKey(key)); ok {
					positions = append(positions, found...)
				}
			}
			if positions != nil {
				best = positionsToSet(positions)
				return setToSortedSlice(best)
			}
		}
	}
	all := make([]int, total)
	for i := range total {
		all[i] = i
	}
	return all
}

func (c *Cache[T]) matchesAllLocked(item T, filters []Filter[T]) bool {
	for _, filter := range filters {
		if !c.matchesLocked(item, filter) {
			return false
		}
	}
	return true
}

func (c *Cache[T]) matchesLocked(item T, filter Filter[T]) bool {
	switch filter.Kind {
	case FilterEq:
		definition, ok := c.indexes[filter.IndexName]
		return ok && joinKey(definition.Key(item)) == joinKey(filter.Key)
	case FilterIn:
		definition, ok := c.indexes[filter.IndexName]
		if !ok {
			return false
		}
		itemKey := joinKey(definition.Key(item))
		for _, key := range filter.Keys {
			if itemKey == joinKey(key) {
				return true
			}
		}
		return false
	case FilterPrefix:
		definition, ok := c.indexes[filter.IndexName]
		return ok && strings.HasPrefix(compareKey(definition.Key(item)), filter.Prefix)
	case FilterRange:
		definition, ok := c.indexes[filter.IndexName]
		if !ok {
			return false
		}
		itemKey := compareKey(definition.Key(item))
		if filter.Min != "" && itemKey < filter.Min {
			return false
		}
		if filter.Max != "" && itemKey > filter.Max {
			return false
		}
		return true
	case FilterFunc:
		return filter.Predicate == nil || filter.Predicate(item)
	default:
		return true
	}
}

func (c *Cache[T]) lookupIndexLocked(indexName string, key string) ([]int, bool) {
	if c.state == nil {
		return nil, false
	}
	index, ok := c.state.indexes[indexName]
	if !ok {
		return nil, false
	}
	positions, ok := index.values[key]
	return positions, ok
}

func cloneSnapshot[T any](snapshot *Snapshot[T]) *Snapshot[T] {
	if snapshot == nil {
		return nil
	}
	copied := *snapshot
	copied.Items = make([]T, len(snapshot.Items))
	copy(copied.Items, snapshot.Items)
	return &copied
}

func contentHash[T any](items []T) (string, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func positionsToSet(values []int) map[int]bool {
	out := make(map[int]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func setToSortedSlice(values map[int]bool) []int {
	out := make([]int, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}
