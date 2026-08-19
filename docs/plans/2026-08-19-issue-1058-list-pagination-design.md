# Trace 与 Conversation 列表分页设计

## 目标

让 Trace 与 Conversation 的列表查询只读取当前页所需候选，并只对当前页执行
canonical 补全；查询成本不再随历史 receipt 总量线性增长。

## 决策

Projection 现有文档是 receipt 粒度，而 Conversation 列表是 receipt 的服务端聚合。
因此不能把固定扫描上限直接改为 `page_size`：这样会截断同一 Conversation 的 receipt，
破坏汇总字段和稳定 cursor。

本次采用两个分页读模型：

1. Trace 列表在 Projection 查询中使用 `(started_at, trace_id)` 的稳定 cursor / `search_after`
   边界，查询 `page_size + 1` 个 Trace 候选。canonical identity、span 统计与 artifact
   扩展只作用于返回页。
2. Trace 列表读模型由 Receipt / 调用事实的权威变化派生为
   `trace-list:<trace_id>` 快照；它不是新的事实来源。每次相关事实变化更新同一
   Trace 快照，以便按 `(started_at, trace_id)` 查询。
3. Conversation 列表写入并读取 Conversation 粒度的列表投影，使用
   `(started_at, conversation_id)` 分页。当前页之外不读取 receipt，也不做 canonical
   session 补全。
4. `total` 通过独立 count / aggregation 取得；不得以读取全部候选计算。

## 契约与边界

- 保持既有 HTTP 返回结构、过滤语义和 cursor 的不透明性。
- cursor 必须绑定稳定排序字段，防止重复和遗漏。
- 不改变 Projection rebuild、alias 切换、MariaDB migration、部署或清理逻辑。
- 读模型只能包含已有 Projection 可安全重建的派生数据；原始事实仍以 Core / Evidence
  为权威来源。

## 验证

- 单元及 adapter 测试断言 OpenSearch 查询携带 `search_after`、`size=page_size+1` 与
  稳定 tiebreaker。
- 服务测试断言页面外 Trace / Conversation 不触发 canonical 查询或 artifact 扩展。
- 多页测试断言无重复、无遗漏、顺序稳定；count 与页面扫描解耦。
