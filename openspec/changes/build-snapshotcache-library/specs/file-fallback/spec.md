## ADDED Requirements

### Requirement: 成功刷新后写入文件快照

当配置文件缓存时，Cache MUST 在成功拉取并构建快照后把快照写入本地文件。

#### Scenario: 写入文件快照

- **WHEN** Source 拉取成功且 FileStore 已启用
- **THEN** Cache MUST 写入包含元信息和数据的本地快照文件

#### Scenario: 文件写入失败

- **WHEN** Source 拉取和索引构建成功但 FileStore 写入失败
- **THEN** Cache MUST 返回刷新错误
- **THEN** Cache MUST NOT 发布新内存快照

### Requirement: 启动失败时从文件恢复

当远端 Source 初始拉取失败且启用文件兜底时，Cache MUST 尝试从本地文件恢复快照。

#### Scenario: 远端失败文件存在

- **WHEN** Source 初始拉取失败且本地文件快照有效
- **THEN** Cache MUST 从文件加载 Snapshot
- **THEN** Cache 状态 MUST 标记数据来自文件兜底

#### Scenario: 远端失败文件不存在

- **WHEN** Source 初始拉取失败且本地文件不存在
- **THEN** Cache MUST 根据启动策略返回错误或发布空快照

### Requirement: 文件过期控制

FileStore SHALL 支持最大陈旧时间，超过最大陈旧时间的文件不能作为有效兜底。

#### Scenario: 文件超过最大陈旧时间

- **WHEN** 本地文件快照超过 MaxStale
- **THEN** FileStore MUST 拒绝加载该文件

### Requirement: 原子写文件

FileStore MUST 使用临时文件加 rename 的方式写入快照，避免进程崩溃留下半文件。

#### Scenario: 原子写入

- **WHEN** FileStore 保存快照
- **THEN** FileStore MUST 先写临时文件
- **THEN** FileStore MUST 通过 rename 发布最终文件
