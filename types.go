package snapshotcache

import (
	"context"
	"time"
)

const (
	DefaultRefreshInterval = 10 * time.Second
	LoadedFromSource       = "source"
	LoadedFromFile         = "file"
	LoadedFromEmpty        = "empty"
)

type Source[T any] interface {
	Fetch(ctx context.Context) ([]T, error)
}

type SourceFunc[T any] func(ctx context.Context) ([]T, error)

func (f SourceFunc[T]) Fetch(ctx context.Context) ([]T, error) {
	return f(ctx)
}

type FileStore[T any] interface {
	Save(ctx context.Context, snapshot *Snapshot[T]) error
	Load(ctx context.Context) (*Snapshot[T], error)
}

type Options[T any] struct {
	Name               string
	Source             Source[T]
	Indexes            []Index[T]
	FileStore          FileStore[T]
	Startup            StartupOptions
	RefreshInterval    time.Duration
	RefreshTimeout     time.Duration
	InitialLoadTimeout time.Duration
	RandomStartDelay   time.Duration
	SourceName         string
	Now                func() time.Time
}

type StartupOptions struct {
	FallbackToFile   bool
	FailIfNoSnapshot bool
}

type Index[T any] struct {
	Name   string
	Unique bool
	Key    func(T) []string
}

type Snapshot[T any] struct {
	Name        string
	Items       []T
	LoadedAt    time.Time
	SourceName  string
	LoadedFrom  string
	ContentHash string
}

type Status struct {
	Ready       bool
	Refreshing  bool
	ItemCount   int
	LoadedAt    time.Time
	SourceName  string
	LoadedFrom  string
	ContentHash string
	LastError   string
}
