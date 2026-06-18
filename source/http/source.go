package httpsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Options struct {
	URL     string
	Method  string
	Headers map[string]string
	Timeout time.Duration
	Client  *http.Client
}

type Source[T any] struct {
	opts Options
}

const defaultTimeout = 30 * time.Second

type responseEnvelope struct {
	Code    *int            `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func New[T any](opts Options) *Source[T] {
	if opts.Method == "" {
		opts.Method = http.MethodGet
	}
	if opts.Timeout == 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: opts.Timeout}
	}
	return &Source[T]{opts: opts}
}

func (s *Source[T]) Fetch(ctx context.Context) ([]T, error) {
	if s == nil || s.opts.URL == "" {
		return nil, fmt.Errorf("snapshotcache/http: url is required")
	}
	reqCtx := ctx
	cancel := func() {}
	if s.opts.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, s.opts.Timeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, s.opts.Method, s.opts.URL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range s.opts.Headers {
		req.Header.Set(key, value)
	}
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("snapshotcache/http: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	payload, err := decodeEnvelope(body)
	if err != nil {
		return nil, err
	}
	var items []T
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeEnvelope(body []byte) ([]byte, error) {
	var envelope responseEnvelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Code == nil {
		return nil, fmt.Errorf("snapshotcache/http: response code is required")
	}
	if envelope.Message == nil {
		return nil, fmt.Errorf("snapshotcache/http: response message is required")
	}
	if *envelope.Code != 0 {
		return nil, fmt.Errorf("snapshotcache/http: business code %d: %s", *envelope.Code, *envelope.Message)
	}
	data := bytes.TrimSpace(envelope.Data)
	if len(data) == 0 {
		return nil, fmt.Errorf("snapshotcache/http: response data is required")
	}
	if data[0] != '[' {
		return nil, fmt.Errorf("snapshotcache/http: response data must be an array")
	}
	return data, nil
}
