package snapshotcache_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mooyang-code/snapshotcache"
)

type httpMapInstrument struct {
	Market string `json:"market"`
	Symbol string `json:"symbol"`
	Status string `json:"status"`
}

func TestHTTPMapGetsItemByUniqueKey(t *testing.T) {
	ctx := context.Background()
	server, _ := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "online"},
		{Market: "binance_spot", Symbol: "AR-USDT", Status: "offline"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMap[httpMapInstrument](
		server.URL,
		snapshotcache.UniqueKey(func(item httpMapInstrument) []string {
			return []string{item.Symbol}
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPMap returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	item, ok := cache.Get("APT-USDT")
	if !ok || item.Market != "binance_spot" || item.Status != "online" {
		t.Fatalf("Get(APT-USDT) = %#v/%v, want binance_spot online item", item, ok)
	}
	if got := cache.All(); len(got) != 2 {
		t.Fatalf("All length = %d, want 2", len(got))
	}
}

func TestHTTPMapGetsItemByCompositeUniqueKey(t *testing.T) {
	ctx := context.Background()
	server, _ := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "binance"},
		{Market: "okx_spot", Symbol: "APT-USDT", Status: "okx"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMap[httpMapInstrument](
		server.URL,
		snapshotcache.UniqueKey(func(item httpMapInstrument) []string {
			return []string{item.Market, item.Symbol}
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPMap returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	item, ok := cache.Get("okx_spot", "APT-USDT")
	if !ok || item.Status != "okx" {
		t.Fatalf("Get(okx_spot, APT-USDT) = %#v/%v, want okx item", item, ok)
	}
}

func TestHTTPMapRejectsDuplicateUniqueKeyOnStart(t *testing.T) {
	ctx := context.Background()
	server, _ := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "v1"},
		{Market: "okx_spot", Symbol: "APT-USDT", Status: "v2"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMap[httpMapInstrument](
		server.URL,
		snapshotcache.UniqueKey(func(item httpMapInstrument) []string {
			return []string{item.Symbol}
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPMap returned error: %v", err)
	}

	err = cache.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("Start error = %v, want duplicate key error", err)
	}
	if _, ok := cache.Get("APT-USDT"); ok {
		t.Fatalf("Get returned item after failed Start, want cache not ready")
	}
}

func TestHTTPMapDuplicateRefreshKeepsPreviousSnapshot(t *testing.T) {
	ctx := context.Background()
	server, setItems := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "v1"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMap[httpMapInstrument](
		server.URL,
		snapshotcache.UniqueKey(func(item httpMapInstrument) []string {
			return []string{item.Symbol}
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPMap returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	setItems([]httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "v2"},
		{Market: "okx_spot", Symbol: "APT-USDT", Status: "duplicate"},
	})
	err = cache.Refresh(ctx)
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("Refresh error = %v, want duplicate key error", err)
	}
	item, ok := cache.Get("APT-USDT")
	if !ok || item.Status != "v1" {
		t.Fatalf("Get after failed Refresh = %#v/%v, want previous v1 item", item, ok)
	}
	if status := cache.Status(); status.LastError == "" {
		t.Fatalf("Status after failed Refresh = %#v, want LastError", status)
	}
}

func TestHTTPMultiMapListsItemsByDuplicateKey(t *testing.T) {
	ctx := context.Background()
	server, _ := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "online"},
		{Market: "binance_spot", Symbol: "AR-USDT", Status: "online"},
		{Market: "okx_spot", Symbol: "APT-USDT", Status: "online"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMultiMap[httpMapInstrument](
		server.URL,
		snapshotcache.Key(func(item httpMapInstrument) []string {
			return []string{item.Market}
		}),
	)
	if err != nil {
		t.Fatalf("NewHTTPMultiMap returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer cache.Stop(ctx)

	items := cache.List("binance_spot")
	if len(items) != 2 {
		t.Fatalf("List(binance_spot) = %#v, want 2 items", items)
	}
}

func TestHTTPMapWithRefreshDisabledStopsWithoutWaitingForTicker(t *testing.T) {
	ctx := context.Background()
	server, _ := newHTTPMapServer(t, []httpMapInstrument{
		{Market: "binance_spot", Symbol: "APT-USDT", Status: "online"},
	})
	defer server.Close()

	cache, err := snapshotcache.NewHTTPMap[httpMapInstrument](
		server.URL,
		snapshotcache.UniqueKey(func(item httpMapInstrument) []string {
			return []string{item.Symbol}
		}),
		snapshotcache.WithRefreshDisabled(),
	)
	if err != nil {
		t.Fatalf("NewHTTPMap returned error: %v", err)
	}
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	stopCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := cache.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func newHTTPMapServer(t *testing.T, items []httpMapInstrument) (*httptest.Server, func([]httpMapInstrument)) {
	t.Helper()

	var mu sync.Mutex
	current := append([]httpMapInstrument(nil), items...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		data := append([]httpMapInstrument(nil), current...)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(struct {
			Code    int                 `json:"code"`
			Message string              `json:"message"`
			Data    []httpMapInstrument `json:"data"`
		}{
			Code:    0,
			Message: "ok",
			Data:    data,
		})
		if err != nil {
			t.Errorf("encode response failed: %v", err)
		}
	}))
	setItems := func(next []httpMapInstrument) {
		mu.Lock()
		current = append([]httpMapInstrument(nil), next...)
		mu.Unlock()
	}
	return server, setItems
}
