## 1. 项目骨架与测试基线

- [x] 1.1 创建 Go module、基础目录和公共包命名。
- [x] 1.2 先写 Cache 生命周期与查询 API 的失败测试，并运行确认 RED。
- [x] 1.3 先写文件兜底的失败测试，并运行确认 RED。
- [x] 1.4 先写 HTTP Source 与 SQLite Source 的失败测试，并运行确认 RED。

## 2. 核心快照与索引

- [x] 2.1 实现 `Source[T]`、`Cache[T]`、`Snapshot[T]`、`Status`、`Options[T]` 等公共 API。
- [x] 2.2 实现显式索引定义、唯一索引校验和重复索引名称校验。
- [x] 2.3 实现 `Get`、`List`、Eq/In/Prefix/Range/Func 过滤查询。
- [x] 2.4 实现手动刷新、完整快照构建和原子发布。
- [x] 2.5 实现后台定时刷新、刷新重入保护和 Stop。
- [x] 2.6 实现 `NewHTTPMap` 和 `NewHTTPMultiMap`，隐藏 HTTP Source 和索引初始化细节。
- [x] 2.7 修复复合 key 分隔符碰撞，确保等值索引无歧义。
- [x] 2.8 Stop 时取消并等待在途后台刷新，Stop 后拒绝手动 Refresh。
- [x] 2.9 将启动延迟改为 0 到 15 秒范围内的随机抖动。

## 3. 文件快照兜底

- [x] 3.1 实现文件快照格式、内容哈希、保存和加载。
- [x] 3.2 实现临时文件加 rename 的原子写入。
- [x] 3.3 实现远端初始加载失败时的文件兜底恢复。
- [x] 3.4 实现 MaxStale 过期控制和刷新失败保留旧快照。

## 4. 数据源实现

- [x] 4.1 实现 HTTP Source，支持统一 envelope 格式 `{code,message,data}`。
- [x] 4.2 实现 SQL Source，支持单列 JSON 解码。
- [x] 4.3 实现 SQLite Source，基于本地 SQLite 文件执行查询。
- [x] 4.4 为 Source 错误路径补齐测试。
- [x] 4.5 为唯一 key、联合唯一 key、一对多 key 和重复 key 补齐 HTTP Map 测试。
- [x] 4.6 补齐 SQL 多列查询测试。
- [x] 4.7 修复 SQLite Source 读取缺失文件时创建空库的问题。

## 5. 验证程序与文档

- [x] 5.1 编写 `cmd/snapshotcache-verify` 验证程序。
- [x] 5.2 验证程序覆盖 HTTP 拉取、索引查询和文件兜底。
- [x] 5.3 验证程序覆盖 SQLite 拉取、索引查询和手动刷新。
- [x] 5.4 补充 README 或 docs 使用示例。

## 6. 完整验证

- [x] 6.1 运行 `go test ./...`。
- [x] 6.2 运行验证程序并确认 HTTP 与 SQLite 端到端输出。
- [x] 6.3 运行 `openspec validate build-snapshotcache-library --strict`。
- [x] 6.4 更新本任务清单为完成状态。
