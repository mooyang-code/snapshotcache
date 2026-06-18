package sqlitesource

import (
	"context"
	"database/sql"
	"errors"
	"os"

	sqlsource "github.com/mooyang-code/snapshotcache/source/sql"
	_ "modernc.org/sqlite"
)

type Options struct {
	Path  string
	Query string
	Args  []any
}

type Source[T any] struct {
	opts Options
}

func New[T any](opts Options) *Source[T] {
	return &Source[T]{opts: opts}
}

func (s *Source[T]) Fetch(ctx context.Context) ([]T, error) {
	if s == nil || s.opts.Path == "" {
		return nil, errors.New("snapshotcache/sqlite: path is required")
	}
	info, err := os.Stat(s.opts.Path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("snapshotcache/sqlite: path is a directory")
	}
	db, err := sql.Open("sqlite", s.opts.Path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	source := sqlsource.New[T](sqlsource.Options{DB: db, Query: s.opts.Query, Args: s.opts.Args})
	return source.Fetch(ctx)
}
