package snapshotcache_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mooyang-code/snapshotcache"
)

type sampleItem struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Score int    `json:"score"`
}

type keyPartsItem struct {
	Parts []string `json:"parts"`
	Label string   `json:"label"`
}

type memorySource[T any] struct {
	mu    sync.Mutex
	items []T
	err   error
}

func (s *memorySource[T]) Fetch(ctx context.Context) ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]T, len(s.items))
	copy(out, s.items)
	return out, nil
}

func (s *memorySource[T]) set(items []T, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
	s.err = err
}

type blockingAfterFirstSource struct {
	mu          sync.Mutex
	count       int
	started     chan struct{}
	release     chan struct{}
	start       sync.Once
	releaseOnce sync.Once
	canceled    bool
}

func newBlockingAfterFirstSource() *blockingAfterFirstSource {
	return &blockingAfterFirstSource{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingAfterFirstSource) Fetch(ctx context.Context) ([]sampleItem, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 1 {
		return []sampleItem{{ID: "apt", Score: 1}}, nil
	}
	s.start.Do(func() {
		close(s.started)
	})
	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.canceled = true
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.release:
		return []sampleItem{{ID: "apt", Score: 2}}, nil
	}
}

func (s *blockingAfterFirstSource) wasCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

func (s *blockingAfterFirstSource) unblock() {
	s.releaseOnce.Do(func() {
		close(s.release)
	})
}

func TestCacheStartRefreshQueryAndStatus(t *testing.T) {
	ctx := context.Background()
	src := &memorySource[sampleItem]{items: []sampleItem{
		{ID: "apt", Group: "spot", Score: 10},
		{ID: "ar", Group: "spot", Score: 30},
		{ID: "btc", Group: "swap", Score: 90},
	}}
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:   "samples",
		Source: src,
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
			{Name: "by_group", Key: func(item sampleItem) []string { return []string{item.Group} }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	if got := cache.Snapshot(); got == nil || len(got.Items) != 3 || got.ContentHash == "" {
		t.Fatalf("Snapshot() = %#v, want 3 items and content hash", got)
	}
	item, ok := cache.Get("by_id", "apt")
	if !ok || item.Score != 10 {
		t.Fatalf("Get(by_id, apt) = %#v/%v, want score 10", item, ok)
	}
	rows := cache.List(snapshotcache.Query[sampleItem]{
		Filters: []snapshotcache.Filter[sampleItem]{
			snapshotcache.Eq[sampleItem]("by_group", "spot"),
			snapshotcache.Where(func(item sampleItem) bool { return item.Score > 20 }),
		},
	})
	if len(rows) != 1 || rows[0].ID != "ar" {
		t.Fatalf("List filtered rows = %#v, want ar only", rows)
	}
	rows = cache.List(snapshotcache.Query[sampleItem]{
		Filters: []snapshotcache.Filter[sampleItem]{
			snapshotcache.In[sampleItem]("by_group", []string{"spot"}),
			snapshotcache.Prefix[sampleItem]("by_id", "a"),
		},
	})
	if len(rows) != 2 {
		t.Fatalf("List with In+Prefix = %#v, want two spot assets with id prefix a", rows)
	}

	src.set([]sampleItem{{ID: "apt", Group: "spot", Score: 42}}, nil)
	if err := cache.Refresh(ctx); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	item, ok = cache.Get("by_id", "apt")
	if !ok || item.Score != 42 {
		t.Fatalf("Get after Refresh = %#v/%v, want updated score 42", item, ok)
	}
	status := cache.Status()
	if !status.Ready || status.ItemCount != 1 || status.LoadedFrom != snapshotcache.LoadedFromSource || status.LastError != "" {
		t.Fatalf("Status() = %#v, want source-loaded ready status", status)
	}
}

func TestCacheRejectsDuplicateIndexNames(t *testing.T) {
	_, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:   "bad",
		Source: &memorySource[sampleItem]{},
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "dup", Key: func(item sampleItem) []string { return []string{item.ID} }},
			{Name: "dup", Key: func(item sampleItem) []string { return []string{item.Group} }},
		},
	})
	if err == nil {
		t.Fatalf("New with duplicate index names returned nil error")
	}
}

func TestCacheStartReturnsBuildErrorWithoutPublishingEmptySnapshot(t *testing.T) {
	ctx := context.Background()
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name: "samples",
		Source: &memorySource[sampleItem]{items: []sampleItem{
			{ID: "apt", Score: 10},
			{ID: "apt", Score: 20},
		}},
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = cache.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("Start error = %v, want duplicate key error", err)
	}
	if snapshot := cache.Snapshot(); snapshot != nil {
		t.Fatalf("Snapshot after failed Start = %#v, want nil", snapshot)
	}
	if status := cache.Status(); status.Ready {
		t.Fatalf("Status after failed Start = %#v, want not ready", status)
	}
}

func TestCacheCompositeKeyDoesNotCollideWhenPartContainsSeparator(t *testing.T) {
	ctx := context.Background()
	cache, err := snapshotcache.New[keyPartsItem](snapshotcache.Options[keyPartsItem]{
		Name: "key_parts",
		Source: &memorySource[keyPartsItem]{items: []keyPartsItem{
			{Parts: []string{"a\x00b"}, Label: "single"},
			{Parts: []string{"a", "b"}, Label: "composite"},
		}},
		Indexes: []snapshotcache.Index[keyPartsItem]{
			{Name: "by_parts", Unique: true, Key: func(item keyPartsItem) []string { return item.Parts }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	single, ok := cache.Get("by_parts", "a\x00b")
	if !ok || single.Label != "single" {
		t.Fatalf("Get(single key) = %#v/%v, want single item", single, ok)
	}
	composite, ok := cache.Get("by_parts", "a", "b")
	if !ok || composite.Label != "composite" {
		t.Fatalf("Get(composite key) = %#v/%v, want composite item", composite, ok)
	}
}

func TestCacheStartRejectsRepeatedStart(t *testing.T) {
	ctx := context.Background()
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:   "samples",
		Source: &memorySource[sampleItem]{items: []sampleItem{{ID: "apt"}}},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	if err := cache.Start(ctx); err == nil {
		t.Fatalf("second Start returned nil error, want lifecycle error")
	}
}

func TestFileFallbackAndRefreshFailureKeepsOldSnapshot(t *testing.T) {
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "samples.snapshot.json")
	store := snapshotcache.NewJSONFileStore[sampleItem](snapshotcache.FileStoreOptions{
		Path:     filePath,
		MaxStale: time.Hour,
	})
	good := &memorySource[sampleItem]{items: []sampleItem{{ID: "apt", Group: "spot", Score: 10}}}
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:      "samples",
		Source:    good,
		FileStore: store,
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	bad := &memorySource[sampleItem]{err: errors.New("remote unavailable")}
	fallback, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:      "samples",
		Source:    bad,
		FileStore: store,
		Startup: snapshotcache.StartupOptions{
			FallbackToFile:   true,
			FailIfNoSnapshot: true,
		},
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		t.Fatalf("New fallback returned error: %v", err)
	}
	if err := fallback.Start(ctx); err != nil {
		t.Fatalf("fallback Start returned error: %v", err)
	}
	defer fallback.Stop(ctx)

	item, ok := fallback.Get("by_id", "apt")
	if !ok || item.Score != 10 {
		t.Fatalf("fallback Get = %#v/%v, want file-backed apt", item, ok)
	}
	if status := fallback.Status(); status.LoadedFrom != snapshotcache.LoadedFromFile || status.LastError == "" {
		t.Fatalf("fallback Status = %#v, want file fallback with last source error", status)
	}
	if err := fallback.Refresh(ctx); err == nil {
		t.Fatalf("Refresh with failing source returned nil error")
	}
	item, ok = fallback.Get("by_id", "apt")
	if !ok || item.Score != 10 {
		t.Fatalf("Get after failed Refresh = %#v/%v, want old snapshot kept", item, ok)
	}
}

func TestJSONFileStoreRejectsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stale.snapshot.json")
	hashStore := snapshotcache.NewJSONFileStore[sampleItem](snapshotcache.FileStoreOptions{Path: path})
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:      "samples",
		Source:    &memorySource[sampleItem]{items: []sampleItem{{ID: "apt"}}},
		FileStore: hashStore,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	staleStore := snapshotcache.NewJSONFileStore[sampleItem](snapshotcache.FileStoreOptions{
		Path:     path,
		MaxStale: time.Hour,
		Now:      func() time.Time { return time.Now().Add(2 * time.Hour) },
	})
	if _, err := staleStore.Load(ctx); err == nil {
		t.Fatalf("Load returned nil error for stale file")
	}
}

func TestJSONFileStoreSaveComputesContentHash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "samples.snapshot.json")
	store := snapshotcache.NewJSONFileStore[sampleItem](snapshotcache.FileStoreOptions{Path: path})

	if err := store.Save(ctx, &snapshotcache.Snapshot[sampleItem]{
		Name:        "samples",
		Items:       []sampleItem{{ID: "apt"}},
		LoadedAt:    time.Now(),
		SourceName:  "manual",
		LoadedFrom:  snapshotcache.LoadedFromSource,
		ContentHash: "caller-supplied-wrong-hash",
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load returned error after Save with stale caller hash: %v", err)
	}
	if loaded.ContentHash == "" || loaded.ContentHash == "caller-supplied-wrong-hash" {
		t.Fatalf("Load ContentHash = %q, want store-computed hash", loaded.ContentHash)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var envelope struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("Unmarshal saved file returned error: %v", err)
	}
	if envelope.ContentHash != loaded.ContentHash {
		t.Fatalf("saved content_hash = %q, loaded hash = %q", envelope.ContentHash, loaded.ContentHash)
	}
}

func TestBackgroundRefreshPublishesNewSnapshot(t *testing.T) {
	ctx := context.Background()
	src := &memorySource[sampleItem]{items: []sampleItem{{ID: "apt", Score: 10}}}
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:            "samples",
		Source:          src,
		RefreshInterval: 10 * time.Millisecond,
		RefreshTimeout:  time.Second,
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)
	src.set([]sampleItem{{ID: "apt", Score: 99}}, nil)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if item, ok := cache.Get("by_id", "apt"); ok && item.Score == 99 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not publish updated score before timeout")
}

func TestCacheStopCancelsInFlightBackgroundRefresh(t *testing.T) {
	ctx := context.Background()
	src := newBlockingAfterFirstSource()
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:            "samples",
		Source:          src,
		RefreshInterval: time.Millisecond,
		Indexes: []snapshotcache.Index[sampleItem]{
			{Name: "by_id", Unique: true, Key: func(item sampleItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	select {
	case <-src.started:
	case <-time.After(time.Second):
		src.unblock()
		t.Fatalf("background refresh did not start")
	}
	stopCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := cache.Stop(stopCtx); err != nil {
		src.unblock()
		t.Fatalf("Stop returned error: %v", err)
	}
	if !src.wasCanceled() {
		src.unblock()
		t.Fatalf("Stop returned before canceling in-flight background refresh")
	}
}

func TestCacheRefreshAfterStopReturnsStoppedError(t *testing.T) {
	ctx := context.Background()
	cache, err := snapshotcache.New[sampleItem](snapshotcache.Options[sampleItem]{
		Name:            "samples",
		Source:          &memorySource[sampleItem]{items: []sampleItem{{ID: "apt", Score: 1}}},
		RefreshInterval: -1,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := cache.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := cache.Refresh(ctx); !errors.Is(err, snapshotcache.ErrCacheStopped) {
		t.Fatalf("Refresh after Stop error = %v, want ErrCacheStopped", err)
	}
}
