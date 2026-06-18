package snapshotcache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	httpsource "github.com/mooyang-code/snapshotcache/source/http"
)

const primaryHTTPIndexName = "__primary__"

type KeyFunc[T any] func(T) []string

type KeySpec[T any] struct {
	unique bool
	key    KeyFunc[T]
}

func UniqueKey[T any](key KeyFunc[T]) KeySpec[T] {
	return KeySpec[T]{
		unique: true,
		key:    key,
	}
}

func Key[T any](key KeyFunc[T]) KeySpec[T] {
	return KeySpec[T]{
		key: key,
	}
}

type HTTPOption func(*httpMapOptions)

type httpMapOptions struct {
	name               string
	headers            map[string]string
	timeout            time.Duration
	client             *http.Client
	filePath           string
	fileMaxStale       time.Duration
	refreshInterval    time.Duration
	refreshTimeout     time.Duration
	initialLoadTimeout time.Duration
	randomStartDelay   time.Duration
	startup            StartupOptions
	startupSet         bool
	now                func() time.Time
}

func WithName(name string) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.name = name
	}
}

func WithHeaders(headers map[string]string) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.headers = copyStringMap(headers)
	}
}

func WithHTTPClient(client *http.Client) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.client = client
	}
}

func WithTimeout(timeout time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.timeout = timeout
	}
}

func WithRefreshInterval(interval time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.refreshInterval = interval
	}
}

func WithRefreshDisabled() HTTPOption {
	return func(opts *httpMapOptions) {
		opts.refreshInterval = -1
	}
}

func WithRefreshTimeout(timeout time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.refreshTimeout = timeout
	}
}

func WithInitialLoadTimeout(timeout time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.initialLoadTimeout = timeout
	}
}

func WithRandomStartDelay(delay time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.randomStartDelay = delay
	}
}

func WithFileCache(path string, maxStale time.Duration) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.filePath = path
		opts.fileMaxStale = maxStale
	}
}

func WithStartupOptions(startup StartupOptions) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.startup = startup
		opts.startupSet = true
	}
}

func WithClock(now func() time.Time) HTTPOption {
	return func(opts *httpMapOptions) {
		opts.now = now
	}
}

type HTTPMap[T any] struct {
	cache *Cache[T]
}

func NewHTTPMap[T any](url string, key KeySpec[T], opts ...HTTPOption) (*HTTPMap[T], error) {
	cache, err := newHTTPPrimaryCache(url, key, true, opts...)
	if err != nil {
		return nil, err
	}
	return &HTTPMap[T]{cache: cache}, nil
}

func (m *HTTPMap[T]) Start(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return errors.New("snapshotcache: http map is nil")
	}
	return m.cache.Start(ctx)
}

func (m *HTTPMap[T]) Stop(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return nil
	}
	return m.cache.Stop(ctx)
}

func (m *HTTPMap[T]) Refresh(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return errors.New("snapshotcache: http map is nil")
	}
	return m.cache.Refresh(ctx)
}

func (m *HTTPMap[T]) Get(key ...string) (T, bool) {
	var zero T
	if m == nil || m.cache == nil {
		return zero, false
	}
	return m.cache.Get(primaryHTTPIndexName, key...)
}

func (m *HTTPMap[T]) All() []T {
	if m == nil || m.cache == nil {
		return nil
	}
	snapshot := m.cache.Snapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.Items
}

func (m *HTTPMap[T]) Filter(predicate func(T) bool) []T {
	if m == nil || m.cache == nil {
		return nil
	}
	return m.cache.List(Query[T]{
		Filters: []Filter[T]{Where(predicate)},
	})
}

func (m *HTTPMap[T]) Status() Status {
	if m == nil || m.cache == nil {
		return Status{}
	}
	return m.cache.Status()
}

type HTTPMultiMap[T any] struct {
	cache *Cache[T]
}

func NewHTTPMultiMap[T any](url string, key KeySpec[T], opts ...HTTPOption) (*HTTPMultiMap[T], error) {
	cache, err := newHTTPPrimaryCache(url, key, false, opts...)
	if err != nil {
		return nil, err
	}
	return &HTTPMultiMap[T]{cache: cache}, nil
}

func (m *HTTPMultiMap[T]) Start(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return errors.New("snapshotcache: http multi map is nil")
	}
	return m.cache.Start(ctx)
}

func (m *HTTPMultiMap[T]) Stop(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return nil
	}
	return m.cache.Stop(ctx)
}

func (m *HTTPMultiMap[T]) Refresh(ctx context.Context) error {
	if m == nil || m.cache == nil {
		return errors.New("snapshotcache: http multi map is nil")
	}
	return m.cache.Refresh(ctx)
}

func (m *HTTPMultiMap[T]) List(key ...string) []T {
	if m == nil || m.cache == nil {
		return nil
	}
	return m.cache.List(Query[T]{
		Filters: []Filter[T]{Eq[T](primaryHTTPIndexName, key...)},
	})
}

func (m *HTTPMultiMap[T]) All() []T {
	if m == nil || m.cache == nil {
		return nil
	}
	snapshot := m.cache.Snapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.Items
}

func (m *HTTPMultiMap[T]) Filter(predicate func(T) bool) []T {
	if m == nil || m.cache == nil {
		return nil
	}
	return m.cache.List(Query[T]{
		Filters: []Filter[T]{Where(predicate)},
	})
}

func (m *HTTPMultiMap[T]) Status() Status {
	if m == nil || m.cache == nil {
		return Status{}
	}
	return m.cache.Status()
}

func newHTTPPrimaryCache[T any](url string, key KeySpec[T], wantUnique bool, opts ...HTTPOption) (*Cache[T], error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, errors.New("snapshotcache: http url is required")
	}
	if key.key == nil {
		return nil, errors.New("snapshotcache: key func is required")
	}
	if key.unique != wantUnique {
		if wantUnique {
			return nil, errors.New("snapshotcache: NewHTTPMap requires UniqueKey")
		}
		return nil, errors.New("snapshotcache: NewHTTPMultiMap requires Key")
	}

	config := httpMapOptions{name: url}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	if strings.TrimSpace(config.name) == "" {
		config.name = url
	}

	source := httpsource.New[T](httpsource.Options{
		URL:     url,
		Headers: copyStringMap(config.headers),
		Timeout: config.timeout,
		Client:  config.client,
	})
	cacheOpts := Options[T]{
		Name:   config.name,
		Source: source,
		Indexes: []Index[T]{
			{
				Name:   primaryHTTPIndexName,
				Unique: key.unique,
				Key:    key.key,
			},
		},
		Startup:            config.startup,
		RefreshInterval:    config.refreshInterval,
		RefreshTimeout:     config.refreshTimeout,
		InitialLoadTimeout: config.initialLoadTimeout,
		RandomStartDelay:   config.randomStartDelay,
		SourceName:         url,
		Now:                config.now,
	}
	if !config.startupSet {
		cacheOpts.Startup = StartupOptions{
			FailIfNoSnapshot: true,
		}
	}
	if config.filePath != "" {
		cacheOpts.FileStore = NewJSONFileStore[T](FileStoreOptions{
			Path:     config.filePath,
			MaxStale: config.fileMaxStale,
			Now:      config.now,
		})
		if !config.startupSet {
			cacheOpts.Startup = StartupOptions{
				FallbackToFile:   true,
				FailIfNoSnapshot: true,
			}
		}
	}

	cache, err := New(cacheOpts)
	if err != nil {
		return nil, fmt.Errorf("snapshotcache: create http cache failed: %w", err)
	}
	return cache, nil
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
