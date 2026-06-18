## ADDED Requirements

### Requirement: 自定义 Source

库 MUST 暴露 `Source[T]` 接口，允许调用方用任意数据源提供快照数据。

#### Scenario: 使用自定义 Source

- **WHEN** 调用方传入实现 `Fetch(ctx)` 的自定义 Source
- **THEN** Cache MUST 使用该 Source 的返回数据构建 Snapshot

### Requirement: HTTP Source

库 MUST 提供 HTTP Source，从配置的 HTTP API 拉取统一 envelope 格式并解码 `data` 数组为目标类型。

#### Scenario: HTTP 返回统一 envelope

- **WHEN** HTTP API 返回 `{"code":0,"message":"ok","data":[...]}`
- **THEN** HTTP Source MUST 解码 `data` 为 `[]T`

#### Scenario: HTTP 返回非统一 envelope

- **WHEN** HTTP API 返回直接数组、缺少 `code/message/data` 字段，或 `code` 不等于 0
- **THEN** HTTP Source MUST 返回错误

#### Scenario: HTTP 返回非 2xx 状态码

- **WHEN** HTTP API 返回非 2xx 状态码
- **THEN** HTTP Source MUST 返回错误

### Requirement: SQL Source

库 MUST 提供基于 `database/sql` 的 SQL Source，通过查询结果构建 `[]T`。

#### Scenario: SQL 查询返回单列 JSON

- **WHEN** SQL 查询结果包含单列 JSON 文本或字节
- **THEN** SQL Source MUST 将每行 JSON 解码为 T

#### Scenario: SQL 查询返回多列数据

- **WHEN** SQL 查询结果包含多列数据
- **THEN** SQL Source MUST 按列名组装对象并解码为 T

#### Scenario: SQL 查询失败

- **WHEN** SQL 查询返回错误
- **THEN** SQL Source MUST 返回错误

### Requirement: SQLite Source

库 MUST 提供 SQLite Source，能够从本地 SQLite 文件执行查询并构建快照数据。

#### Scenario: SQLite 查询返回数据

- **WHEN** SQLite 文件存在且查询返回单列 JSON
- **THEN** SQLite Source MUST 返回解码后的 `[]T`

#### Scenario: SQLite 文件不可用

- **WHEN** SQLite 文件路径不可打开
- **THEN** SQLite Source MUST 返回错误

#### Scenario: SQLite 文件不存在

- **WHEN** SQLite 文件路径不存在
- **THEN** SQLite Source MUST 返回错误
- **THEN** SQLite Source MUST NOT 创建空数据库文件
