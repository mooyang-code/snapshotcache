## Context

`snapshotcache` 是独立 Go 公共库，服务于 moox storage 元数据缓存以及未来其他控制面配置缓存场景。它采用“远端拉取、本地文件兜底、内存索引、后台刷新”的通用模型，但不能继承 schemaID、etag、灰度、多版本规则，也不能绑定 moox 的业务概念。

当前新库没有实现代码。设计文档已沉淀在 `docs/snapshotcache-design.md`，本变更负责把该设计落地为可测试公共 API、内置 Source、文件快照和验证程序。

## Goals / Non-Goals

**Goals:**

- 提供泛型 `Cache[T]`，管理快照初始加载、手动刷新、定时刷新、原子发布和状态查询。
- 提供 `NewHTTPMap` 和 `NewHTTPMultiMap`，作为 HTTP 场景的首选用户 API。
- 提供 `Source[T]` 抽象，并内置 HTTP、SQL DB、SQLite Source。
- 提供文件快照存储，支持远端不可用时从本地文件恢复。
- 提供显式索引定义和轻量 filter query。
- 提供端到端验证程序，证明 HTTP 与 SQLite 拉取、查询和文件兜底可用。

**Non-Goals:**

- 不实现 schemaID、etag、灰度、多版本策略。
- 不提供分布式缓存、KV LRU/TTL 缓存、复杂 SQL 查询引擎。
- 不直接接入 moox 的 `metadata.Store`，moox 侧适配层属于后续工作。
- 不实现增量刷新；第一版只做全量快照刷新。

## Decisions

### 1. HTTP 场景优先使用 Map API

选择 `NewHTTPMap` 和 `NewHTTPMultiMap` 作为 HTTP 场景的入口。调用方只需要知道 API 地址和 key 函数，不需要理解 HTTP Source 初始化、文件兜底和索引对象。

`NewHTTPMap` 对应唯一 key；如果拉取到重复 key，默认首次启动失败，刷新时保留旧快照并记录错误。启用文件兜底且旧文件有效时，首次启动可以恢复旧快照。`NewHTTPMultiMap` 对应一对多 key，允许同一个 key 返回多条数据。

### 2. 使用泛型 Source 和 Cache

选择 `Source[T]` 与 `Cache[T]`，调用方用自己的结构体承载数据。这样公共库不感知业务结构，也避免旧 xData 全局 `cache.GetXXX()` 风格带来的耦合。

备选方案是让库只缓存 `map[string]any` 或 `json.RawMessage`。该方案更动态，但会把类型检查推给调用方，API 使用体验更差。

### 3. 快照采用完整构建后原子替换

刷新流程为：拉取数据、构建索引、写文件、生成新快照、原子替换。任何一步失败都保留旧快照。

备选方案是边拉取边修改现有缓存。该方案写入成本低，但容易暴露半更新状态，且并发读写复杂度更高。

### 4. 第一版采用全量刷新

全量刷新更适合元数据、配置表和控制面数据。它天然支持删除和禁用，不依赖更新时间精度，也避免时钟漂移和增量漏数据。

备选方案是按照 `mtime` 增量刷新。该方案适合大表，但需要删除 tombstone、版本水位和重放策略，第一版暂不引入。

### 5. 索引用显式 key 函数定义

索引定义由调用方提供 `Key func(T) []string`。这样可以支持普通 struct、proto 结构和自定义类型，同时避免反射字段名在重构时静默失效。

备选方案是用 struct tag 或字段名字符串自动建索引。该方案配置更短，但反射错误更晚暴露，且对嵌套结构不友好。

### 6. 文件缓存作为 Source 失败兜底，不作为主数据源

启动时优先拉远端，失败后读本地文件。运行时刷新失败不覆盖本地文件，也不替换旧快照。

备选方案是启动时先读文件再异步拉远端。该方案启动更快，但调用方可能在早期读到明显过期数据。第一版通过配置 `RemoteFirst` 统一行为。

## Risks / Trade-offs

- [泛型对象可能被调用方修改] → 文档声明 `Snapshot.Items` 视为只读；调用方需要不可变性时使用值类型或在业务适配层 clone。
- [全量刷新对大表不友好] → 第一版定位控制面快照；后续可增加增量 Source，不影响当前 API。
- [文件快照过期仍可恢复] → 提供 `MaxStale` 配置，超过后拒绝兜底。
- [定时刷新重入] → 使用原子标记或互斥锁，刷新中跳过下一轮 tick。
- [SQL Source 无法自动解码所有数据库列形态] → 第一版支持单列 JSON 与多列 JSON marshal 两种路径，复杂场景可自定义 Source。

## Migration Plan

这是新库，无兼容迁移要求。

落地步骤：

1. 建立 Go module 与公共 API。
2. 测试先行实现 Cache 生命周期与索引查询。
3. 测试先行实现文件快照存储和兜底恢复。
4. 测试先行实现 HTTP、SQL、SQLite Source。
5. 编写验证程序，端到端验证 HTTP 和 SQLite 拉取。
6. 运行 `go test ./...` 和验证程序。

回滚策略：新库尚未接入其他项目，回滚为删除或不引用该 module。

## Open Questions

无阻塞问题。moox 的 `metadata.CachedStore` 适配层不在本变更中实现。
