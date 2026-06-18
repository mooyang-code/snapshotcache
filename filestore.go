package snapshotcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type FileStoreOptions struct {
	Path     string
	MaxStale time.Duration
	Now      func() time.Time
}

type JSONFileStore[T any] struct {
	path     string
	maxStale time.Duration
	now      func() time.Time
}

type fileEnvelope[T any] struct {
	CacheName   string    `json:"cache_name"`
	SavedAt     time.Time `json:"saved_at"`
	Source      string    `json:"source"`
	LoadedFrom  string    `json:"loaded_from"`
	ItemCount   int       `json:"item_count"`
	ContentHash string    `json:"content_hash"`
	Data        []T       `json:"data"`
}

func NewJSONFileStore[T any](opts FileStoreOptions) *JSONFileStore[T] {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &JSONFileStore[T]{
		path:     opts.Path,
		maxStale: opts.MaxStale,
		now:      now,
	}
}

func (s *JSONFileStore[T]) Save(ctx context.Context, snapshot *Snapshot[T]) error {
	if s == nil || s.path == "" {
		return errors.New("snapshotcache: file store path is required")
	}
	if snapshot == nil {
		return errors.New("snapshotcache: snapshot is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	items := make([]T, len(snapshot.Items))
	copy(items, snapshot.Items)
	hash, err := contentHash(items)
	if err != nil {
		return err
	}
	envelope := fileEnvelope[T]{
		CacheName:   snapshot.Name,
		SavedAt:     snapshot.LoadedAt,
		Source:      snapshot.SourceName,
		LoadedFrom:  snapshot.LoadedFrom,
		ItemCount:   len(items),
		ContentHash: hash,
		Data:        items,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *JSONFileStore[T]) Load(ctx context.Context) (*Snapshot[T], error) {
	if s == nil || s.path == "" {
		return nil, errors.New("snapshotcache: file store path is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var envelope fileEnvelope[T]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if s.maxStale > 0 && s.now().Sub(envelope.SavedAt) > s.maxStale {
		return nil, fmt.Errorf("snapshotcache: file snapshot is stale: saved_at=%s max_stale=%s", envelope.SavedAt.Format(time.RFC3339Nano), s.maxStale)
	}
	hash, err := contentHash(envelope.Data)
	if err != nil {
		return nil, err
	}
	if envelope.ContentHash != "" && envelope.ContentHash != hash {
		return nil, fmt.Errorf("snapshotcache: file snapshot hash mismatch")
	}
	items := make([]T, len(envelope.Data))
	copy(items, envelope.Data)
	return &Snapshot[T]{
		Name:        envelope.CacheName,
		Items:       items,
		LoadedAt:    envelope.SavedAt,
		SourceName:  envelope.Source,
		LoadedFrom:  LoadedFromFile,
		ContentHash: hash,
	}, nil
}
