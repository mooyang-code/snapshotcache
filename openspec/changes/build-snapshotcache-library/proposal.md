## Why

moox storage 的元数据读取需要从请求热路径中剥离出来，形成一个可复用的本地快照缓存能力；同类需求未来也会出现在其他配置表、元数据表和轻量控制面数据中。

现有缓存工具的“远端拉取、本地文件兜底、内存索引、后台刷新”思想值得借鉴，但其中的 schemaID、etag、灰度或 MySQL/GORM 等历史约束，不适合作为新的通用公共库继续扩展。

## What Changes

- 新建独立 Go 公共库 `github.com/mooyang-code/snapshotcache`。
- 提供 `NewHTTPMap` 和 `NewHTTPMultiMap`，让 HTTP 场景只需要配置 URL 和 key 函数。
- 提供泛型 `Cache[T]`，支持启动加载、手动刷新、后台定时刷新、状态查询和快照读取。
- 提供 HTTP、SQL DB、SQLite 和自定义 Source。
- 提供本地文件快照兜底：远端拉取失败时可从文件恢复。
- 提供内存索引和轻量 filter query：等值、多值、前缀、范围和自定义 predicate。
- 提供端到端验证程序，覆盖 HTTP 拉取、SQLite 拉取、文件兜底和查询能力。
- 不支持 schemaID、etag、灰度、多版本规则。
- 不实现分布式缓存、LRU/TTL KV 缓存或复杂 SQL 查询引擎。

## Capabilities

### New Capabilities

- `snapshot-lifecycle`: 管理快照初始加载、原子发布、定时刷新、手动刷新、停止和状态暴露。
- `snapshot-sources`: 从 HTTP、SQL DB、SQLite 和自定义 Source 拉取快照数据。
- `snapshot-query`: 为快照数据构建内存索引，并提供轻量 filter query。
- `file-fallback`: 将快照写入本地文件，并在远端不可用时从文件恢复。

### Modified Capabilities

无。

## Impact

- 新增公共库目录 `/Users/mooyang/Documents/go/src/github.com/mooyang-code/snapshotcache`。
- 新增 Go module、公共 API、内置 Source、文件快照存储、查询索引和测试。
- 新增 `docs/snapshotcache-design.md` 作为设计说明。
- 新增 `cmd/snapshotcache-verify` 或等价验证程序，用于端到端验证 HTTP 和 SQLite 拉取。
