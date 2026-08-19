# Trace 与 Conversation 列表分页设计

## 目标

让 Trace 与 Conversation 的普通列表查询先在 Core 生命周期账本选择当前页身份，随后只扩展该页的 Receipt、Evidence、Artifact 与 canonical 数据。查询不再固定加载 2,000 个 execution summary。

## 决策

1. Trace 以 Core Receipt 为身份事实源，在 MariaDB 按 `(MIN(issued_at) desc, trace_id asc)` 使用 keyset cursor 选择 `page_size + 1` 个身份。Evidence 写入失败的 degraded execution 仍可进入列表。
2. Conversation 以 canonical Conversation 与其 Core Receipt 的存在关系为身份事实源，按 `(created_at desc, conversation_id asc)` 使用相同的 keyset cursor。并发插入新记录不会改变后续 cursor 的边界。
3. 身份选择完成后，将当前页 Trace ID 或 Conversation ID 通过 `terms` 一次性下推到 Core/Evidence Projection。Core Receipt 查询按身份 `collapse`，每个身份保留独立的受控 `inner_hits` 预算，避免单个长 Trace/Conversation 吃掉整页候选；canonical 补全仍对整页执行一次集合查询。
4. Trace 使用独立 `COUNT(DISTINCT trace_id)`；Conversation 使用独立精确 `COUNT(*)`，不通过候选窗口或近似聚合计算总数。
5. Conversation、Interaction、Assembly Revision、Operation 与 Trace source module 的 canonical 数据均采用集合查询，禁止页内逐 ID SQL。

## 契约与边界

- 保持既有 HTTP 返回结构、授权边界、排序方向和 cursor 不透明性。
- 只有可完整下推到身份索引的普通列表条件启用 fast path；复杂内容筛选继续使用兼容路径，避免改变筛选语义。
- MariaDB 身份已提交但 Core Projection 尚未刷新时返回 `BKN_TRACE_SUMMARY_PROJECTION_LAG`，不返回短页、不生成或推进 cursor；调用方重试后从同一边界读取，避免跨存储可见性造成永久漏项。
- 身份查询复用现有 MariaDB 表和索引；不新增数据库表、OpenSearch mapping、Projection rebuild 文档或 alias 行为。
- 不执行部署、索引重建、数据删除、Schema migration 或生产配置修改。

## 验证

- 服务测试断言 Trace 与 Conversation cursor 下推 `(started_at, id)`，并保留独立精确 Total。
- MariaDB adapter 使用精确 count 与 `page_size+1` keyset 查询；普通 page 参数仍兼容 offset，cursor 后续页不再增长 offset。
- 服务测试断言只将当前页 ID 交给 Projection，并验证 Trace 与 Conversation cursor 能继续到下一页。
- Core Projection 测试断言页内 Trace/Conversation IDs 使用 `terms` 批量下推和按身份 collapse；长 Trace 达到截断上限时，同页后续身份仍会返回。
- Evidence Projection 测试断言 Conversation 先解析当页 Trace IDs，再按真实 `trace_id` 字段读取 Artifact，避免查询不存在的 Conversation mapping。
