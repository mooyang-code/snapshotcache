# snapshotcache

`snapshotcache` 是一个 Go 泛型公共库，用来把外部配置、元数据表或 HTTP API 返回的数据拉取成进程内只读快照，并为这些快照建立索引，方便业务代码高频查询。

模块路径：

```text
github.com/mooyang-code/snapshotcache
```

## 它解决什么问题

很多服务会频繁读取变化不高的控制面数据，例如元数据、路由表、字段定义、数据源配置和开关配置。每次请求都查 DB 或调 API 会增加延迟，也会让下游依赖变成请求链路上的强依赖。

`snapshotcache` 把这类数据按固定周期全量拉取到内存。业务请求只读本地快照；后台刷新成功后原子替换快照；刷新失败时继续使用上一份可用快照。你也可以配置本地文件快照，让服务启动时在远端不可用的情况下恢复旧数据。

## 适合使用的场景

- 元数据、配置表、路由表、字段表等读多写少的数据。
- 数据量可以被单进程内存完整承载。
- 调用方希望用 `Get/List` 直接查询本地快照。
- 启动时允许先拉远端，远端失败时再用本地文件兜底。

不适合：

- 大规模明细数据缓存。
- 分布式一致性缓存。
- LRU/TTL KV 缓存。
- 复杂 SQL 查询引擎。
- 带版本、灰度和发布流程的配置管理系统。

## 安装

```bash
go get github.com/mooyang-code/snapshotcache
```

## 快速使用

下面示例从 HTTP API 拉取数据，按 `symbol` 建立唯一 key，启动后台刷新，并在业务代码中直接查询本地内存快照。

`Stop` 只需要在服务退出时调用。短生命周期程序可以把 `defer cache.Stop(ctx)` 写在 `Start` 后面；常驻服务更推荐在启动阶段初始化 cache，在请求处理逻辑里只读 cache，在统一的 shutdown hook 里停止 cache。

`http://127.0.0.1:8080/instruments` 必须返回统一格式：

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
    // 业务服务通常把远端 API 地址放在配置文件、环境变量或启动参数中。
    // 示例为了便于复制运行，使用命令行参数传入：
    //
    //   go run . -instrument-api=http://127.0.0.1:8080/instruments
    //
    instrumentAPI := flag.String("instrument-api", "http://127.0.0.1:8080/instruments", "instrument snapshot API")
    flag.Parse()

    // ctx 表示整个服务进程的生命周期。收到 SIGINT/SIGTERM 后，ctx 会被取消。
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    // 服务启动阶段初始化 cache。Start 会先同步加载一份快照，再启动后台定时刷新。
    app, err := NewApp(ctx, *instrumentAPI)
    if err != nil {
        panic(err)
    }

    // 业务使用阶段只读内存快照，不要在每次请求后 Stop。
    // 在真实服务中，这一行通常写在 HTTP/gRPC handler 里。
    if apt, ok := app.GetInstrument("APT-USDT"); ok {
        fmt.Println("APT status:", apt.Status)
    }

    // 这里可以启动你的 HTTP/gRPC server。示例用阻塞等待信号代替。
    <-ctx.Done()

    // 服务退出阶段统一 Stop cache。使用新的 shutdownCtx，避免复用已经取消的 ctx。
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := app.Close(shutdownCtx); err != nil {
        panic(err)
    }
}

// NewApp 在服务启动阶段调用。
// 这里创建 instrumentCache，并调用 Start 完成首次加载和后台刷新初始化。
// 如果 API 地址来自配置文件或环境变量，也可以在 main 里读取后传进来。
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
// 业务 handler 可以直接调用它；读取只访问内存快照，不会请求远端 API。
func (a *App) GetInstrument(symbol string) (Instrument, bool) {
    return a.instrumentCache.Get(symbol)
}

// ListOnlineBinanceInstruments 演示按业务条件过滤当前快照。
// Filter 适合小规模元数据过滤；高频点查优先使用 Get。
func (a *App) ListOnlineBinanceInstruments() []Instrument {
    onlineRows := a.instrumentCache.Filter(func(item Instrument) bool {
        return item.Market == "binance_spot" && item.Status == "online"
    })
    return onlineRows
}
```

## 使用步骤

1. 定义业务结构体 `T`。它就是快照中的单行数据类型。
2. 调用 `NewHTTPMap` 或 `NewHTTPMultiMap`，传入 API 地址和 key 函数。
3. 调用 `Start(ctx)`。它会先同步加载一次数据，再启动后台刷新。
4. 在业务请求中调用 `Get`、`List`、`Filter` 或 `All` 查询本地内存。
5. 服务退出时调用 `Stop(ctx)`。

## Key 设计

如果一个 key 只能对应一条数据，使用 `NewHTTPMap + UniqueKey`：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Symbol}
    }),
)

apt, ok := cache.Get("APT-USDT")
```

如果唯一性需要多个字段共同确定，key 函数返回多个字段即可：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Market, item.Symbol}
    }),
)

apt, ok := cache.Get("binance_spot", "APT-USDT")
```

如果一个 key 可以对应多条数据，使用 `NewHTTPMultiMap + Key`：

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.Key(func(item Instrument) []string {
        return []string{item.Market}
    }),
)

rows := cache.List("binance_spot")
```

`UniqueKey` 会强制唯一性。首次启动发现重复 key 时，如果没有可用文件兜底，`Start` 返回错误；后台刷新发现重复 key 时，本次刷新失败，旧快照继续可用。`Key` 不做唯一性校验，适合一对多查询。

## 核心行为

- `Start` 会先拉取一次远端数据。初始加载成功后，缓存才进入可读状态。
- `RefreshInterval` 不传时默认 `10s`。传入非零值可以覆盖默认周期。
- 测试或一次性加载场景可以使用 `WithRefreshDisabled()` 关闭后台刷新。
- 配置 `WithRandomStartDelay` 时，启动刷新会在 `0` 到 `min(配置值, 15s)` 内随机延迟。
- 每轮刷新都会全量拉取数据，重新构建快照和索引。
- 配置了 `WithFileCache` 或 `FileStore` 时，每次刷新成功都会先写文件，再发布新内存快照。
- 文件写入失败会让本次刷新失败；这是为了保证“已发布内存快照”和“本地兜底文件”一致。
- 刷新失败时不会替换旧快照；读请求继续使用上一份成功快照。
- 只有启动初始加载失败时，才会按 `Startup.FallbackToFile` 从文件恢复。
- `Stop(ctx)` 会停止后台 ticker，取消并等待在途后台刷新；`Stop` 后手动 `Refresh` 返回 `ErrCacheStopped`。
- `Get/List/Filter/All` 只读内存，不会访问远端 API、DB 或文件。

## HTTP API 返回格式

HTTP 拉取只接受统一响应格式。接入方必须把 API 适配为下面的格式后，才能使用本库拉取：

- HTTP 状态码必须是 `2xx`。
- 响应 body 必须是 JSON。
- body 必须是 JSON 对象，且包含 `code`、`message`、`data` 三个字段。
- `code` 必须为 `0`，否则本次拉取失败。
- `message` 必须是字符串，用于错误信息和排查。
- `data` 必须是数组。
- `data` 数组中的每个元素会按调用方的泛型类型 `T` 反序列化。

固定 API 返回格式：

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {"symbol": "APT-USDT", "market": "binance_spot", "status": "online"}
  ]
}
```

对应配置不需要声明数据路径；HTTP 拉取固定读取 `data` 字段：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Symbol}
    }),
)
```

如果 API 直接返回数组、使用其他字段名，或者把数据放在嵌套对象中，调用方需要先在 API 层做适配。本库不会通过配置兼容多种返回格式。

## 文件兜底

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Symbol}
    }),
    snapshotcache.WithFileCache("./var/cache/instruments.snapshot.json", 24 * time.Hour),
)
```

文件兜底只用于启动恢复。运行时刷新失败时，库会保留旧内存快照，不会重新读文件覆盖内存。

## 查询方式

`NewHTTPMap` 用唯一 key 点查：

```go
item, ok := cache.Get("APT-USDT")
```

`NewHTTPMultiMap` 用普通 key 查多条：

```go
rows := cache.List("binance_spot")
```

`Filter` 在当前内存快照上做业务过滤：

```go
rows := cache.Filter(func(item Instrument) bool {
    return item.Market == "binance_spot" && item.Status == "online"
})
```

`All` 返回当前完整快照的切片副本：

```go
rows := cache.All()
```

## 进阶：Source 选择

如果你需要接 SQLite、SQL DB 或自定义数据源，可以直接使用底层 `Cache[T] + Source[T] + Index[T]` API。HTTP 场景优先使用上面的 `NewHTTPMap` 和 `NewHTTPMultiMap`。

### HTTP Source

```go
source := httpsource.New[Instrument](httpsource.Options{
    URL:     "http://127.0.0.1:8080/instruments",
    Timeout: 3 * time.Second,
})
```

### SQLite Source

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
    Path:  "./metadata.db",
    Query: `SELECT payload FROM instruments ORDER BY payload`,
})
```

SQLite Source 适合读取本地元数据文件。当前 SQL/SQLite Source 支持单列 JSON 解码：查询结果每行一列，列内容是一个 JSON 对象。
SQLite Source 不会创建缺失的数据库文件；路径不存在时直接返回错误。

SQL/SQLite Source 也支持多列查询：每行结果会先按列名组装成 JSON 对象，再解码到泛型类型 `T`。

```go
source := sqlitesource.New[Instrument](sqlitesource.Options{
    Path:  "./metadata.db",
    Query: `SELECT symbol, market, status FROM instruments`,
})
```

### 自定义 Source

```go
type MySource struct{}

func (s *MySource) Fetch(ctx context.Context) ([]Instrument, error) {
    return []Instrument{
        {Symbol: "APT-USDT", Market: "binance_spot", Status: "online"},
    }, nil
}
```

只要实现 `Fetch(ctx)`，就可以接任意数据来源。

底层 `Cache` 的过滤查询仍然支持 `Eq`、`In`、`Prefix`、`Range` 和 `Where`。其中 `Eq` 和 `In` 可以利用索引缩小候选集。

## 状态和手动刷新

```go
status := cache.Status()
fmt.Println(status.Ready, status.ItemCount, status.LoadedFrom, status.LastError)

if err := cache.Refresh(ctx); err != nil {
    return err
}
```

`Refresh(ctx)` 会立即执行一次同步刷新，适合管理接口、CLI 或测试场景。

## 验证

```bash
go test ./...
go run ./cmd/snapshotcache-verify
```

验证程序会启动本地 HTTP API、创建临时 SQLite 文件，并覆盖 HTTP 拉取、SQLite 拉取、索引查询、文件兜底和手动刷新。
