## ADDED Requirements

### Requirement: HTTP Map 简化查询

库 MUST 提供面向 HTTP 数据源的 Map API，使调用方只需提供 URL 和 key 函数即可完成启动、刷新和查询。

#### Scenario: 唯一 key 查询

- **WHEN** 调用方使用 `NewHTTPMap` 和 `UniqueKey` 启动缓存
- **THEN** `Get(key...)` MUST 返回匹配项

#### Scenario: 联合唯一 key 查询

- **WHEN** 调用方的 `UniqueKey` 返回多个字段
- **THEN** `Get(part1, part2, ...)` MUST 使用多个字段组成的 key 查询匹配项

#### Scenario: 唯一 key 重复且没有文件兜底

- **WHEN** HTTP 数据中有多条记录映射到同一个 `UniqueKey`
- **THEN** 首次 `Start` MUST 返回错误且不发布快照
- **THEN** 已有快照的 `Refresh` MUST 返回错误并保留旧快照

#### Scenario: 一对多 key 查询

- **WHEN** 调用方使用 `NewHTTPMultiMap` 和 `Key` 启动缓存
- **THEN** `List(key...)` MUST 返回该 key 下的全部匹配项

### Requirement: 显式索引定义

库 MUST 支持调用方通过显式 key 函数定义索引，索引名称在同一个 Cache 中 MUST 唯一。

#### Scenario: 复合 key 无分隔符碰撞

- **WHEN** 两个 item 的 key 分别为 `["a\\u0000b"]` 和 `["a", "b"]`
- **THEN** Cache MUST 将它们视为两个不同 key

#### Scenario: 唯一索引查询

- **WHEN** 调用方定义唯一索引并启动 Cache
- **THEN** Get 使用索引名和 key MUST 返回匹配项

#### Scenario: 重复索引名称

- **WHEN** 调用方配置重复索引名称
- **THEN** Cache MUST 返回配置错误

### Requirement: 非唯一索引和列表查询

库 SHALL 支持非唯一索引，并允许查询返回多个匹配项。

#### Scenario: 非唯一索引匹配多条

- **WHEN** 多个 item 映射到同一个非唯一索引 key
- **THEN** List 使用该索引过滤 MUST 返回所有匹配项

### Requirement: 轻量 filter query

库 MUST 支持 Eq、In、Prefix、Range 和 Func 类型的过滤条件。

#### Scenario: Eq 条件使用索引

- **WHEN** Query 使用已定义索引的 Eq 条件
- **THEN** List SHOULD 优先通过索引缩小候选集
- **THEN** 返回结果 MUST 满足条件

#### Scenario: Func 条件过滤

- **WHEN** Query 包含自定义 predicate
- **THEN** List MUST 只返回 predicate 判断为 true 的 item

### Requirement: 快照读取

库 MUST 提供 Snapshot 方法返回当前完整快照。

#### Scenario: 读取当前快照

- **WHEN** Cache 已成功加载数据
- **THEN** Snapshot MUST 返回当前数据、加载时间、来源名称和内容哈希
