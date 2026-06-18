package sqlsource

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type Options struct {
	DB    *sql.DB
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
	if s == nil || s.opts.DB == nil {
		return nil, errors.New("snapshotcache/sql: db is required")
	}
	if s.opts.Query == "" {
		return nil, errors.New("snapshotcache/sql: query is required")
	}
	rows, err := s.opts.DB.QueryContext(ctx, s.opts.Query, s.opts.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var items []T
	for rows.Next() {
		item, err := scanRow[T](rows, columns)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRow[T any](rows *sql.Rows, columns []string) (T, error) {
	var zero T
	if len(columns) == 1 {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return zero, err
		}
		data, err := rawBytes(raw)
		if err != nil {
			return zero, err
		}
		return decodeJSON[T](data)
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return zero, err
	}
	obj := make(map[string]any, len(columns))
	for i, name := range columns {
		obj[name] = normalizeSQLValue(values[i])
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return zero, err
	}
	return decodeJSON[T](data)
}

func decodeJSON[T any](data []byte) (T, error) {
	var out T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err := decoder.Decode(&out)
	return out, err
}

func rawBytes(raw any) ([]byte, error) {
	switch value := raw.(type) {
	case nil:
		return nil, fmt.Errorf("snapshotcache/sql: nil JSON column")
	case []byte:
		return value, nil
	case string:
		return []byte(value), nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
}

func normalizeSQLValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}
