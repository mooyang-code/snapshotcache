# snapshotcache 公共库设计

## 背景

`snapshotcache` 是一个独立公共 Go 库，模块路径为：

```text
github.com/mooyang-code/snapshotcache
```

它用于把外部配置表、元数据表或 API 返回的数据周期性拉取为本地快照，并在进程内构建只读索引供高频查询使用。它吸收了“远端拉取、本地文件兜底、内存索引、后台刷新”的通用思想，但不绑定任何业务概念。

## 目标

- 支持 HTTP、通用 SQL DB、SQLite 和用户自定义数据源。
- 支持启动时远端拉取，失败后使用本地文件快照兜底。
- 支持后台定时刷新，刷新过程中不阻塞读请求。
- 支持内存索引和轻量 filter query。
- 支持全量快照原子替换，避免半更新状态暴露给调用方。
- 提供清晰、可测试、无业务耦合的泛型 API。

## 非目标

- 不支持 `schemaID`、`etag`、灰度发布、多版本规则。
- 不做 LRU、TTL KV 或分布式缓存。
- 不实现复杂 SQL 查询引擎。
- 不感知 moox 的 Space、DataSet、View 等业务概念。
- 不直接管理业务表 DDL 或迁移。

## 核心模型

`snapshotcache` 提供两层 API：

- 常用层：`NewHTTPMap` 和 `NewHTTPMultiMap`。调用方只需要提供 HTTP API 地址和 key 函数。
- 进阶层：`Cache[T] + Source[T] + Index[T]`。调用方可以接 SQLite、SQL DB 或自定义数据源，并定义多个索引。

进阶层的核心对象是 `Cache[T]`：

```text
Cache[T]
  -> Source[T]      拉取外部数据
  -> FileStore[T]   本地文件快照兜底
  -> IndexSet[T]    内存索引
  -> Snapshot[T]    当前不可变快照
  -> Refresher      启动加载、定时刷新、手动刷新
```

每次刷新会完整拉取数据，构建新的 `Snapshot[T]`。只有当拉取、解码、索引构建、文件落盘全部成功后，才会把新快照发布给读请求。运行中的刷新失败不替换旧快照。

## 核心接口

### HTTPMap

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Symbol}
    }),
)
```

`HTTPMap` 面向唯一 key 查询：

```go
item, ok := cache.Get("APT-USDT")
```

如果唯一性由多个字段共同确定，key 函数返回多个字段：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Market, item.Symbol}
    }),
)

item, ok := cache.Get("binance_spot", "APT-USDT")
```

`UniqueKey` 会强制唯一性。首次启动发现重复 key 时，如果没有可用文件兜底，`Start` 返回错误；后台刷新发现重复 key 时，本次刷新失败，旧快照继续可用。

### HTTPMultiMap

```go
cache, err := snapshotcache.NewHTTPMultiMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.Key(func(item Instrument) []string {
        return []string{item.Market}
    }),
)

rows := cache.List("binance_spot")
```

`HTTPMultiMap` 面向一对多查询。它不做唯一性校验，适合按分组 key 获取多条数据。

### Source

```go
type Source[T any] interface {
    Fetch(ctx context.Context) ([]T, error)
}
```

内置 Source：

- `source/http`：从 HTTP API 拉取统一 envelope 格式中的 `data` 数组。
- `source/sql`：从 `database/sql` 支持的 DB 中执行查询。
- `source/sqlite`：基于 SQL Source 的 SQLite 便捷封装。
- 用户自定义数据源：直接实现 `Source[T]`。

### Cache

```go
type Cache[T any] interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Refresh(ctx context.Context) error
    Snapshot() *Snapshot[T]
    Get(indexName string, key ...string) (T, bool)
    List(query Query[T]) []T
    Status() Status
}
```

`Start` 负责初始加载和后台刷新；`Refresh` 用于手动刷新；`Snapshot` 返回当前快照；`Get` 通过索引查询；`List` 执行轻量过滤。

`Stop` 会停止后台 ticker，取消并等待在途后台刷新。`Stop` 后仍允许读取已有快照，但手动 `Refresh` 会返回 `ErrCacheStopped`。

### Snapshot

```go
type Snapshot[T any] struct {
    Items       []T
    LoadedAt    time.Time
    SourceName  string
    ContentHash string
}
```

`Snapshot` 对调用方应视为只读。公共库不会深拷贝任意泛型对象，业务如需避免指针对象被修改，应传入值类型，或在适配层自行 clone。

## 索引与查询

索引通过显式 key 函数定义，避免反射依赖：

```go
Index[T]{
    Name:   "by_space_dataset",
    Unique: true,
    Key: func(item T) []string {
        return []string{item.SpaceID, item.DatasetID}
    },
}
```

查询支持：

- `Eq`：字段等值匹配，可走索引。
- `In`：字段多值匹配，可走索引。
- `Prefix`：前缀匹配，默认扫描当前快照。
- `Range`：范围匹配，默认扫描当前快照。
- `Func`：用户自定义 predicate。

第一版只保证 `Eq/In` 使用索引加速，其余过滤在当前内存快照中完成。元数据规模通常较小，这个实现足够简单可靠。

复合 key 的等值索引使用无碰撞编码保存，不依赖分隔符拼接。`Prefix` 和 `Range` 是扫描型过滤，主要面向单字段字符串 key；复合 key 的前缀/范围语义应由调用方用 `Where` 明确表达。

## 文件快照兜底

每个 Cache 可以配置一个本地文件快照。HTTP 常用层通过 `WithFileCache` 开启：

```go
cache, err := snapshotcache.NewHTTPMap[Instrument](
    "http://127.0.0.1:8080/instruments",
    snapshotcache.UniqueKey(func(item Instrument) []string {
        return []string{item.Symbol}
    }),
    snapshotcache.WithFileCache("./var/cache/instruments.snapshot.json", 24 * time.Hour),
)
```

进阶层可以直接配置 `FileStore`：

```text
write snapshot.tmp -> fsync -> rename snapshot.json
```

文件内容包含元信息和数据：

```json
{
  "cache_name": "moox_storage_datasets",
  "saved_at": "2026-06-18T12:00:00Z",
  "source": "sqlite",
  "item_count": 100,
  "content_hash": "sha256:...",
  "data": []
}
```

启动加载策略：

```text
1. 优先 Source.Fetch
2. 成功：构建索引，写入文件，发布内存快照
3. 失败：如果启用文件兜底，读取本地文件快照
4. 文件也失败：根据配置决定启动失败或空快照启动
```

`NewHTTPMap` 和 `NewHTTPMultiMap` 默认要求启动时必须得到一份可用快照；如果远端失败且没有可用文件兜底，`Start` 返回错误。

运行时刷新失败时保留旧快照，并记录最后一次错误。

文件写入是发布流程的一部分。如果 `FileStore.Save` 失败，本次刷新不发布新内存快照，避免内存快照和本地兜底文件出现不一致。

## 刷新策略

默认采用全量快照刷新：

- 元数据类数据通常规模小，全量刷新简单可靠。
- 能自然处理删除、禁用、外部直接改库等情况。
- 避免更新时间粒度、时钟漂移和增量漏数据问题。

后续可扩展增量 Source，但第一版不实现增量刷新。

刷新控制：

- 定时器使用 `time.Ticker`。
- 调用方未配置 `RefreshInterval` 时，默认每 10 秒刷新一次。
- HTTP 常用层可以通过 `WithRefreshDisabled()` 关闭后台刷新，适合测试或一次性加载场景。
- 如果上一轮刷新未结束，跳过当前 tick。
- 支持启动随机抖动。实际延迟在 `0` 到 `min(RandomStartDelay, 15s)` 之间，避免多个进程同时打数据源。
- 支持刷新超时。

## 配置示例

```yaml
snapshotcache:
  name: moox_storage_metadata
  refresh_interval: 5s
  refresh_timeout: 3s
  initial_load_timeout: 10s
  startup:
    fallback_to_file: true
    fail_if_no_snapshot: true
  file_cache:
    enabled: true
    path: ./var/cache/moox-storage/metadata
    max_stale: 24h
```

HTTP Source：

```yaml
source:
  type: http
  url: http://127.0.0.1:8080/metadata/datasets
  method: GET
  timeout: 3s
```

HTTP API 返回格式固定为：

```json
{
  "code": 0,
  "message": "ok",
  "data": []
}
```

`code` 必须为 `0`，`message` 必须是字符串，`data` 必须是数组。调用方需要先适配 API 返回格式，HTTP Source 不提供字段路径配置。

SQLite Source：

```yaml
source:
  type: sqlite
  path: ./var/storage/metadata/storage_metadata.db
  query: SELECT c_attrs_json FROM t_datasets WHERE c_status != 'deleted'
```

SQLite Source 按读取型数据源处理：数据库文件不存在时直接返回错误，不会创建空数据库。SQL/SQLite Source 支持两种结果形态：

- 单列 JSON：每行一列，列内容是一个 JSON 对象。
- 多列结果：按列名组装 JSON 对象，再解码为目标类型。

## moox 接入方式

moox 不直接暴露 `snapshotcache` 给业务层，而是在 storage 模块中实现自己的 `metadata.CachedStore`：

```text
metadata Source / metadata API
  -> snapshotcache.Cache
  -> metadata.CachedStore
  -> access / validator / router / search / viewbuilder
```

`snapshotcache` 只负责快照缓存和查询能力；`metadata.CachedStore` 负责把 moox 的 `Space`、`DataSet`、`View` 等业务结构映射为 `metadata.Store` 接口。

## 验证要求

公共库需要至少提供以下端到端验证：

- HTTP Source 拉取测试数据，构建索引，通过 `Get/List` 查询。
- HTTPMap 支持唯一 key、联合唯一 key 和重复 key 启动失败。
- HTTPMultiMap 支持一对多 key 查询。
- SQLite Source 拉取测试表数据，构建索引，通过 `Get/List` 查询。
- HTTP 拉取失败时，从本地文件快照恢复。
- 刷新失败时保留旧快照。
- 手动刷新成功后新快照可见。
