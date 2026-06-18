# snapshotcache：Go 进程内快照缓存库

把读多写少的元数据加载到本地内存，安全刷新，让请求链路保持轻快。

[![Go Reference](https://pkg.go.dev/badge/github.com/mooyang-code/snapshotcache.svg)](https://pkg.go.dev/github.com/mooyang-code/snapshotcache)
[![Release](https://img.shields.io/github/v/tag/mooyang-code/snapshotcache?label=release)](https://github.com/mooyang-code/snapshotcache/tags)
[![GitHub stars](https://img.shields.io/github/stars/mooyang-code/snapshotcache?style=social)](https://github.com/mooyang-code/snapshotcache)

[English](./README.md) · [快速开始](#快速开始) · [文档](#文档) · [架构](#架构) · [开发](#开发)

---

`snapshotcache` 是一个 Go 泛型公共库，用来把读多写少的控制面数据加载成进程内只读快照。

它适合元数据、路由表、字段定义、数据源配置、开关配置等高频读取、低频变化的数据。业务请求只读本地内存；后台刷新会全量拉取数据，重建索引，并原子发布新的快照。

```text
go get github.com/mooyang-code/snapshotcache
```

---

## News

- **2026-06-18**：首次公开发布，支持 HTTP、SQL、SQLite、文件兜底、唯一 key、多值 key、过滤查询、生命周期控制和随机刷新抖动。

---

## 核心亮点

- **本地快照读取**
  请求处理逻辑通过 `Get`、`List`、`Filter` 或 `All` 读取进程内内存。

- **后台全量刷新**
  `Start(ctx)` 会先加载初始快照，然后按固定周期在后台刷新。

- **原子发布**
  刷新失败不会替换上一份可用快照。

- **文件兜底**
  启动时远端不可用时，可以从本地文件恢复旧快照。

- **泛型类型安全**
  调用方定义自己的行类型 `T`，缓存存储和返回强类型数据。

- **唯一索引和多值索引**
  使用 `NewHTTPMap` 做一对一点查，使用 `NewHTTPMultiMap` 做一对多查询。

- **HTTP、SQL、SQLite 和自定义数据源**
  常见 HTTP 场景可以使用高层封装；其他场景只要实现 `Fetch(ctx)` 即可接入。

---

## 适用场景

适合使用 `snapshotcache` 的场景：

- 数据量可以被单进程内存完整承载。
- 数据读取频率高，但变化频率低于请求流量。
- 服务启动时可以从 HTTP、DB、SQLite 或文件兜底加载完整快照。
- 业务代码希望通过本地 `Get/List/Filter` 查询，而不是每次请求都访问 DB 或 API。

不适合把它当作：

- 大规模明细数据缓存。
- 分布式一致性缓存。
- LRU 或 TTL KV 缓存。
- SQL 查询引擎。
- 带 schema 版本、灰度发布和审批流的配置平台。

---

## 快速开始

下面示例从 HTTP API 拉取交易标的数据，按 `symbol` 建立唯一 key，启动后台刷新，并在业务代码中读取本地快照。

API 必须返回固定 JSON 包装格式：

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
	// 业务服务通常从配置文件、环境变量或启动参数读取远端 API 地址。
	// 示例为了便于复制运行，使用命令行参数传入：
	//
	//   go run . -instrument-api=http://127.0.0.1:8080/instruments
	//
	instrumentAPI := flag.String("instrument-api", "http://127.0.0.1:8080/instruments", "instrument snapshot API")
	flag.Parse()

	// ctx 表示整个服务进程的生命周期。
	// 收到 SIGINT 或 SIGTERM 后，ctx 会被取消。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 服务启动阶段初始化 cache。
	// Start 会先完成首次加载，再启动后台定时刷新。
	app, err := NewApp(ctx, *instrumentAPI)
	if err != nil {
		panic(err)
	}

	// 业务请求处理逻辑只读内存快照。
	// 不要在每次请求后 Stop cache。
	if apt, ok := app.GetInstrument("APT-USDT"); ok {
		fmt.Println("APT status:", apt.Status)
	}

	// 这里可以启动你的 HTTP/gRPC server。示例用等待退出信号代替。
	<-ctx.Done()

	// 服务退出阶段统一 Stop cache。
	// 注意使用新的 shutdownCtx，因为上面的 ctx 已经被取消。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Close(shutdownCtx); err != nil {
		panic(err)
	}
}

// NewApp 在服务启动阶段调用。
// 它创建 instrumentCache，并调用 Start 加载第一份快照。
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

// Close 在服务退出阶段调用。
// 它会停止后台刷新，并等待正在进行的刷新结束或被 ctx 取消。
func (a *App) Close(ctx context.Context) error {
	if a == nil || a.instrumentCache == nil {
		return nil
	}
	return a.instrumentCache.Stop(ctx)
}

// GetInstrument 演示按唯一 key 点查。
// handler 可以直接调用它；读取只访问本地快照。
func (a *App) GetInstrument(symbol string) (Instrument, bool) {
	return a.instrumentCache.Get(symbol)
}

// ListOnlineBinanceInstruments 演示过滤当前快照。
// Filter 适合小规模元数据过滤；高频点查优先使用 Get。
func (a *App) ListOnlineBinanceInstruments() []Instrument {
	onlineRows := a.instrumentCache.Filter(func(item Instrument) bool {
		return item.Market == "binance_spot" && item.Status == "online"
	})
	return onlineRows
}
```

---

## 两种使用方式

### HTTP 元数据 API

一个 key 只对应一条数据时，使用 `NewHTTPMap`：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
)
```

一个 key 对应多条数据时，使用 `NewHTTPMultiMap`：

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.Key(func(item Instrument) []string {
		return []string{item.Market}
	}),
)

rows := cache.List("binance_spot")
```

### DB、SQLite 或自定义数据源

如果数据不是固定 HTTP 包装格式，可以使用底层 `Cache[T] + Source[T] + Index[T]` API。

```go
type MySource struct{}

func (s *MySource) Fetch(ctx context.Context) ([]Instrument, error) {
	return []Instrument{
		{Symbol: "APT-USDT", Market: "binance_spot", Status: "online"},
	}, nil
}
```

只要实现 `Fetch(ctx)`，就可以接入任意数据来源。

---

## 核心行为

- `Start(ctx)` 会先执行一次初始加载。只有加载到可用快照后，缓存才进入可读状态。
- `RefreshInterval` 默认是 `10s`。传入非零值可以覆盖默认周期。
- `WithRefreshDisabled()` 可以关闭后台刷新，适合测试或一次性加载场景。
- `WithRandomStartDelay(delay)` 会在第一次后台刷新前等待 `0..min(delay, 15s)` 的随机延迟。
- 每次刷新都会全量拉取数据，并重建所有索引。
- 刷新成功时，会先写文件兜底，再发布新的内存快照。
- 文件写入失败会让本次刷新失败，以保证文件和内存一致。
- 刷新失败时，上一份快照继续可用。
- 文件兜底只用于启动恢复。
- `Stop(ctx)` 会停止 ticker，取消后台刷新，并等待在途刷新结束。
- `Stop(ctx)` 后再调用 `Refresh(ctx)` 会返回 `ErrCacheStopped`。
- `Get`、`List`、`Filter` 和 `All` 只读内存。

---

## Key 设计

`UniqueKey` 强制一个 key 只能对应一行。启动时发现重复 key 且没有可用文件兜底时，`Start` 返回错误。后台刷新发现重复 key 时，本次刷新失败，旧快照继续可用。

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
)

apt, ok := cache.Get("APT-USDT")
```

多个字段共同确定唯一性时，key 函数返回多个字段：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Market, item.Symbol}
	}),
)

apt, ok := cache.Get("binance_spot", "APT-USDT")
```

`Key` 允许一个 key 对应多行：

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.Key(func(item Instrument) []string {
		return []string{item.Market}
	}),
)

rows := cache.List("binance_spot")
```

---

## HTTP API 格式

HTTP Source 只接受一种响应格式：

- HTTP 状态码必须是 `2xx`。
- 响应 body 必须是 JSON。
- body 必须是包含 `code`、`message` 和 `data` 的对象。
- `code` 必须为 `0`。
- `message` 必须是字符串。
- `data` 必须是数组。
- `data` 中的每个元素会被解码为调用方的泛型类型 `T`。

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {"symbol": "APT-USDT", "market": "binance_spot", "status": "online"}
  ]
}
```

如果你的 API 返回裸数组、使用其他字段名，或者把数据放在嵌套对象中，请先在 API 层做适配，再接入 `snapshotcache`。

---

## 文件兜底

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
	"http://127.0.0.1:8080/instruments",
	snapshotcache.UniqueKey(func(item Instrument) []string {
		return []string{item.Symbol}
	}),
	snapshotcache.WithFileCache("./var/cache/instruments.snapshot.json", 24*time.Hour),
)
```

文件兜底只用于启动恢复。运行时刷新失败时，缓存会保留旧内存快照，不会从兜底文件重新覆盖内存。

---

## Source 后端

### HTTP

```go
source := httpsource.New[Instrument](httpsource.Options{
	URL:     "http://127.0.0.1:8080/instruments",
	Timeout: 3 * time.Second,
})
```

### SQLite

SQLite 适合读取本地元数据文件。数据库文件不存在时会返回错误，不会创建空 DB 文件。

单列 JSON：

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
	Path:  "./metadata.db",
	Query: `SELECT payload FROM instruments ORDER BY payload`,
})
```

多列映射：

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
	Path:  "./metadata.db",
	Query: `SELECT symbol, market, status FROM instruments`,
})
```

### SQL

SQL Source 支持与 SQLite 相同的两种形态：

- 每行一列 JSON 对象。
- 多列按列名映射到 `T`。

---

## 架构

```text
snapshotcache
├── cache.go                    # 生命周期、刷新、原子发布
├── httpmap.go                  # NewHTTPMap 和 NewHTTPMultiMap 封装
├── query.go                    # key 编码和内存查询过滤
├── filestore.go                # 本地文件兜底
├── source/
│   ├── http/                   # 固定包装格式 HTTP Source
│   ├── sql/                    # 通用 SQL Source
│   └── sqlite/                 # 本地 SQLite Source
├── cmd/snapshotcache-verify/   # 冒烟验证命令
├── docs/                       # 设计文档
└── openspec/                   # 变更提案、规格和任务
```

刷新流程：

```text
Source.Fetch(ctx)
      │
      ▼
构建 Snapshot + Indexes
      │
      ▼
写入文件兜底（可选）
      │
      ▼
原子发布到内存
      │
      ▼
业务读取：Get / List / Filter / All
```

---

## 文档

| 文档 | 说明 |
|------|------|
| [README.md](./README.md) | 英文概览、快速开始、核心行为和示例 |
| [README_zh.md](./README_zh.md) | 中文文档 |
| [docs/snapshotcache-design.md](./docs/snapshotcache-design.md) | 设计意图和生命周期细节 |
| [openspec/changes/build-snapshotcache-library/proposal.md](./openspec/changes/build-snapshotcache-library/proposal.md) | 初始变更提案 |
| [openspec/changes/build-snapshotcache-library/design.md](./openspec/changes/build-snapshotcache-library/design.md) | OpenSpec 设计说明 |
| [openspec/changes/build-snapshotcache-library/tasks.md](./openspec/changes/build-snapshotcache-library/tasks.md) | 实现任务清单 |

### 快速链接

- **HTTP 用户**：从 [快速开始](#快速开始) 开始。
- **自定义数据源**：阅读 [Source 后端](#source-后端)。
- **生命周期行为**：阅读 [核心行为](#核心行为)。

---

## 开发

运行测试：

```bash
go test ./...
```

运行冒烟验证：

```bash
go run ./cmd/snapshotcache-verify
```

---

**如果这个项目对你有帮助，欢迎给一个 Star。**

