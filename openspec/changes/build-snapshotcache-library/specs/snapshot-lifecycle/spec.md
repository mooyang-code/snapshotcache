## ADDED Requirements

### Requirement: Cache 初始加载和快照发布

Cache MUST 在启动时从配置的 Source 拉取数据，构建完整快照和索引，并且只有完整构建成功后才发布给读请求。

#### Scenario: 初始加载成功

- **WHEN** 调用方启动 Cache 且 Source 返回有效数据
- **THEN** Cache 发布包含全部数据的新 Snapshot
- **THEN** Cache 状态显示已经加载成功

#### Scenario: 构建失败不发布半成品

- **WHEN** Source 返回数据但索引构建失败
- **THEN** Cache MUST 返回启动错误
- **THEN** Cache MUST NOT 发布半构建快照

### Requirement: 手动刷新和定时刷新

Cache MUST 支持手动刷新和后台定时刷新。刷新成功后新快照对后续读请求可见，刷新失败时旧快照继续可用。

#### Scenario: 手动刷新成功

- **WHEN** 调用方调用 Refresh 且 Source 返回新数据
- **THEN** Cache 发布新 Snapshot
- **THEN** 后续 Get/List 查询使用新数据

#### Scenario: 刷新失败保留旧快照

- **WHEN** Cache 已有旧 Snapshot 且下一次刷新失败
- **THEN** Cache MUST 保留旧 Snapshot
- **THEN** Cache 状态记录最后一次错误

### Requirement: 并发读和刷新隔离

Cache SHALL 允许读请求在刷新期间继续读取上一个完整 Snapshot。

#### Scenario: 刷新期间读取旧快照

- **WHEN** 后台刷新正在进行
- **THEN** 并发 Snapshot/Get/List 调用 MUST NOT 阻塞到刷新完成
- **THEN** 读请求 MUST 看到某一个完整 Snapshot

### Requirement: 停止后台刷新

Cache MUST 提供 Stop 能力关闭后台刷新循环，并取消、等待在途后台刷新。

#### Scenario: 停止后不再定时刷新

- **WHEN** 调用方调用 Stop
- **THEN** Cache 停止后台 ticker
- **THEN** Cache MUST 取消并等待在途后台刷新返回
- **THEN** 已有 Snapshot 仍可被读取

#### Scenario: 停止后拒绝手动刷新

- **WHEN** 调用方已经调用 Stop
- **THEN** 后续 Refresh MUST 返回 ErrCacheStopped

### Requirement: 启动随机抖动

Cache SHALL 支持启动刷新前的随机抖动，避免多个进程同时访问数据源。

#### Scenario: 随机抖动范围

- **WHEN** 调用方配置 RandomStartDelay
- **THEN** 实际启动延迟 MUST 在 `0` 到 `min(RandomStartDelay, 15s)` 之间
