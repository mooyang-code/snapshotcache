package snapshotcache

import (
	"context"
	"testing"
	"time"
)

func TestNewUsesDefaultRefreshInterval(t *testing.T) {
	cache, err := New[struct{}](Options[struct{}]{
		Name: "items",
		Source: SourceFunc[struct{}](func(ctx context.Context) ([]struct{}, error) {
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if cache.opts.RefreshInterval != 10*time.Second {
		t.Fatalf("RefreshInterval = %s, want 10s default", cache.opts.RefreshInterval)
	}
}

func TestRandomStartDelayIsCappedAtFifteenSeconds(t *testing.T) {
	for range 100 {
		delay := boundedRandomStartDelay(30 * time.Second)
		if delay < 0 || delay > 15*time.Second {
			t.Fatalf("boundedRandomStartDelay(30s) = %s, want value in [0, 15s]", delay)
		}
	}
}

func TestRandomStartDelayUsesSmallerConfiguredRange(t *testing.T) {
	for range 100 {
		delay := boundedRandomStartDelay(25 * time.Millisecond)
		if delay < 0 || delay > 25*time.Millisecond {
			t.Fatalf("boundedRandomStartDelay(25ms) = %s, want value in [0, 25ms]", delay)
		}
	}
}
