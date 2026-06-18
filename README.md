# snapshotcache: In-Process Snapshot Cache for Go

Load read-mostly metadata into local memory, refresh it safely, and keep request paths fast.

[![Go Reference](https://pkg.go.dev/badge/github.com/mooyang-code/snapshotcache.svg)](https://pkg.go.dev/github.com/mooyang-code/snapshotcache)
[![Release](https://img.shields.io/github/v/tag/mooyang-code/snapshotcache?label=release)](https://github.com/mooyang-code/snapshotcache/tags)
[![GitHub stars](https://img.shields.io/github/stars/mooyang-code/snapshotcache?style=social)](https://github.com/mooyang-code/snapshotcache)

[中文文档](./README_zh.md) · [Quick Start](#quick-start) · [Documentation](#documentation) · [Architecture](#architecture) · [Development](#development)

---

`snapshotcache` is a Go generic library for loading read-mostly control data into a local, immutable in-memory snapshot.

It is designed for metadata, route tables, field definitions, data source configs, feature switches, and other data that services read frequently but update less often. Your request path reads memory only; background refreshes fetch the full dataset, rebuild indexes, and atomically publish the new snapshot.

```text
go get github.com/mooyang-code/snapshotcache
```

---

## News

- **2026-06-18**: Initial public release with HTTP, SQL, SQLite, file fallback, unique key, multi-key, filtering, lifecycle control, and refresh jitter.

---

## Core Highlights

- **Local Snapshot Reads**
  Request handlers query in-process memory through `Get`, `List`, `Filter`, or `All`.

- **Background Full Refresh**
  `Start(ctx)` loads an initial snapshot, then refreshes in the background at a fixed interval.

- **Atomic Publish**
  A failed refresh never replaces the last usable snapshot.

- **File Fallback**
  Local files can restore a snapshot during startup when the remote source is unavailable.

- **Typed Generic API**
  Callers define their own row type `T`; the cache stores and returns strongly typed values.

- **Unique and Multi-Value Indexes**
  Use `NewHTTPMap` for one-to-one lookups and `NewHTTPMultiMap` for one-to-many lookups.

- **HTTP, SQL, SQLite, and Custom Sources**
  Use the high-level HTTP helpers or plug in any source that implements `Fetch(ctx)`.

## When to Use It

Use `snapshotcache` when:

- The dataset can fit in one process's memory.
- The data is read often and changes less frequently than request traffic.
- Startup can load a complete snapshot from HTTP, DB, SQLite, or file fallback.
- Business code wants local `Get/List/Filter` calls instead of request-time DB/API calls.

Do not use it as:

- A large-scale detail-data cache.
- A distributed consistency layer.
- An LRU or TTL key-value cache.
- A SQL query engine.
- A configuration platform with schema versions, gray release, or approval workflow.

## Quick Start

The example below loads instruments from an HTTP API, builds a unique key on `symbol`, starts background refresh, and reads the local snapshot from business code.

The API must return this fixed JSON envelope:

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "symbol": "APT-USDT",
      "market": "binance_spot",
      "status": "online"
    },
    {
      "symbol": "AR-USDT",
      "market": "binance_spot",
      "status": "online"
    }
  ]
}
```

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/mooyang-code/snapshotcache"
)

type Instrument struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"`
	Status string `json:"status"`
}

type App struct {
	instrumentCache *snapshotcache.HTTPMap[Instrument]
}

func main() {
	// Business services usually read the remote API URL from config,
	// environment variables, or command-line flags. This example uses a flag:
	//
	//   go run . -instrument-api=http://127.0.0.1:8080/instruments
	//
	instrumentAPI := flag.String("instrument-api", "http://127.0.0.1:8080/instruments", "instrument snapshot API")
	flag.Parse()

	// ctx represents the service process lifecycle.
	// It is canceled when the process receives SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize cache during service startup.
	// Start performs the first load, then starts background refresh.
	app, err := NewApp(ctx, *instrumentAPI)
	if err != nil {
		panic(err)
	}

	// Business handlers should only read the in-memory snapshot.
	// Do not Stop the cache after every request.
	if apt, ok := app.GetInstrument("APT-USDT"); ok {
		fmt.Println("APT status:", apt.Status)
	}

	// Start your HTTP/gRPC server here. This example waits for a shutdown signal.
	<-ctx.Done()

	// Stop cache during service shutdown.
	// Use a new shutdown context because ctx has already been canceled.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Close(shutdownCtx); err != nil {
		panic(err)
	}
}

// NewApp is called during service startup.
// It creates instrumentCache and calls Start to load the first snapshot.
func NewApp(ctx context.Context, instrumentAPI string) (*App, error) {
	cache, err := snapshotcache.NewHTTPMap[Instrument](
		instrumentAPI,
		snapshotcache.UniqueKey(func(item Instrument) []string {
			return []string{item.Symbol}
		}),
	)
	if err != nil {
		return nil, err
	}
	if err := cache.Start(ctx); err != nil {
		return nil, err
	}

	return &App{instrumentCache: cache}, nil
}

// Close is called during service shutdown.
// It stops background refresh and waits for any in-flight refresh to finish or be canceled.
func (a *App) Close(ctx context.Context) error {
	if a == nil || a.instrumentCache == nil {
		return nil
	}
	return a.instrumentCache.Stop(ctx)
}

// GetInstrument does a unique-key lookup.
// Handlers can call it directly; reads only touch the local snapshot.
func (a *App) GetInstrument(symbol string) (Instrument, bool) {
	return a.instrumentCache.Get(symbol)
}

// ListOnlineBinanceInstruments filters the current snapshot.
// Filter is convenient for small metadata sets; prefer Get for hot key lookups.
func (a *App) ListOnlineBinanceInstruments() []Instrument {
	onlineRows := a.instrumentCache.Filter(func(item Instrument) bool {
		return item.Market == "binance_spot" && item.Status == "online"
	})
	return onlineRows
}
```

## Two Ways to Use snapshotcache

### For HTTP Metadata APIs

Use `NewHTTPMap` when one key maps to one row:

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
)
```

Use `NewHTTPMultiMap` when one key maps to multiple rows:

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.Key(func(item Instrument) []string {
		return []string{item.Market}
	}),
)

rows := cache.List("binance_spot")
```

### For DB, SQLite, or Custom Sources

Use the lower-level `Cache[T] + Source[T] + Index[T]` API when your data does not come from the fixed HTTP envelope.

```go
type MySource struct{}

func (s *MySource) Fetch(ctx context.Context) ([]Instrument, error) {
	return []Instrument{
		{Symbol: "APT-USDT", Market: "binance_spot", Status: "online"},
	}, nil
}
```

Any source that implements `Fetch(ctx)` can be used by the cache.

## Core Behavior

- `Start(ctx)` performs one initial load. The cache becomes readable only after a usable snapshot is loaded.
- `RefreshInterval` defaults to `10s`. Pass a non-zero interval to override it.
- `WithRefreshDisabled()` disables background refresh for tests or one-shot loads.
- `WithRandomStartDelay(delay)` waits for a random delay in `0..min(delay, 15s)` before the first background refresh.
- Every refresh fetches the full dataset and rebuilds all indexes.
- A successful refresh writes file fallback first, then publishes the new memory snapshot.
- File write failure fails the refresh to keep file and memory consistent.
- Refresh failure keeps the previous snapshot alive.
- File fallback is used only during startup recovery.
- `Stop(ctx)` stops the ticker, cancels background refresh, and waits for in-flight refresh work.
- `Refresh(ctx)` after `Stop(ctx)` returns `ErrCacheStopped`.
- `Get`, `List`, `Filter`, and `All` read memory only.

## Key Design

`UniqueKey` enforces one row per key. Startup fails when duplicate keys are found and no file fallback is available. Background refresh fails and keeps the old snapshot when duplicate keys appear.

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
)

apt, ok := cache.Get("APT-USDT")
```

Composite keys are expressed by returning multiple key parts:

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Market, item.Symbol}
	}),
)

apt, ok := cache.Get("binance_spot", "APT-USDT")
```

`Key` allows one key to map to many rows:

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.Key(func(item Instrument) []string {
		return []string{item.Market}
	}),
)

rows := cache.List("binance_spot")
```

## HTTP API Format

HTTP sources accept one response format only:

- HTTP status must be `2xx`.
- Response body must be JSON.
- Body must be an object with `code`, `message`, and `data`.
- `code` must be `0`.
- `message` must be a string.
- `data` must be an array.
- Each `data` item is decoded into the caller's generic type `T`.

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {"symbol": "APT-USDT", "market": "binance_spot", "status": "online"}
  ]
}
```

If your API returns a bare array, different field names, or a nested payload, adapt the API response before connecting it to `snapshotcache`.

## File Fallback

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
	snapshotcache.WithFileCache("./var/cache/instruments.snapshot.json", 24*time.Hour),
)
```

File fallback is for startup recovery. During runtime, refresh failures keep the old memory snapshot and do not reload from the fallback file.

## Source Backends

### HTTP

```go
source := httpsource.New[Instrument](httpsource.Options{
	URL:     "http://127.0.0.1:8080/instruments",
	Timeout: 3 * time.Second,
})
```

### SQLite

SQLite is useful for local metadata files. Missing database files return an error; the source will not create an empty DB file.

Single-column JSON:

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
	Path:  "./metadata.db",
	Query: `SELECT payload FROM instruments ORDER BY payload`,
})
```

Multi-column mapping:

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
	Path:  "./metadata.db",
	Query: `SELECT symbol, market, status FROM instruments`,
})
```

### SQL

SQL sources support the same two shapes as SQLite:

- One JSON object column per row.
- Multiple columns mapped by column name into `T`.

## Architecture

```text
snapshotcache
├── cache.go                    # lifecycle, refresh, atomic snapshot publish
├── httpmap.go                  # NewHTTPMap and NewHTTPMultiMap helpers
├── query.go                    # key encoding and in-memory query filters
├── filestore.go                # local file fallback store
├── source/
│   ├── http/                   # fixed-envelope HTTP source
│   ├── sql/                    # generic SQL source
│   └── sqlite/                 # local SQLite source
├── cmd/snapshotcache-verify/   # smoke verification command
├── docs/                       # design notes
└── openspec/                   # change proposal, specs, and tasks
```

Refresh flow:

```text
Source.Fetch(ctx)
      │
      ▼
Build Snapshot + Indexes
      │
      ▼
Write File Fallback (optional)
      │
      ▼
Atomic Publish to Memory
      │
      ▼
Business Reads: Get / List / Filter / All
```

## Documentation

| Document | Description |
|----------|-------------|
| [README.md](./README.md) | Overview, quick start, behavior, and examples |
| [README_zh.md](./README_zh.md) | Chinese documentation |
| [docs/snapshotcache-design.md](./docs/snapshotcache-design.md) | Design intent and lifecycle details |
| [openspec/changes/build-snapshotcache-library/proposal.md](./openspec/changes/build-snapshotcache-library/proposal.md) | Initial change proposal |
| [openspec/changes/build-snapshotcache-library/design.md](./openspec/changes/build-snapshotcache-library/design.md) | OpenSpec design notes |
| [openspec/changes/build-snapshotcache-library/tasks.md](./openspec/changes/build-snapshotcache-library/tasks.md) | Implementation checklist |

### Quick Links

- **For HTTP users**: Start with [Quick Start](#quick-start).
- **For custom sources**: See [Source Backends](#source-backends).
- **For lifecycle behavior**: See [Core Behavior](#core-behavior).

---

## Development

Run tests:

```bash
go test ./...
```

Run the smoke verifier:

```bash
go run ./cmd/snapshotcache-verify
```

---

**If this project helps you, please give it a star.**
