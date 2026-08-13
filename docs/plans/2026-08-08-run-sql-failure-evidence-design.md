# run_sql 失败证据设计

## 目标

失败的 `run_sql` 与成功调用一样留下可复现输入，并提供可诊断的结构化错误事实；不增加 MCP 入参，不重构 Operation 生命周期协议。

## 现状与根因

`run_sql` 仅在 Vega 查询成功后调用 `EmitRunSQLEvents`。失败调用因此只有 Operation 状态、输入哈希和通用的 `OpenBKN operation failed`，没有 SQL Query Artifact，也没有失败阶段、稳定错误码或实际错误摘要。组装层虽能读取错误字段，但上游没有产生这些事实。

## 方案

继续使用现有 `data.query.observed` 与 2.2 Artifact 合同：

1. 每次 `run_sql` 尝试都写入 `query` Artifact，内容为 SQL 和解析出的资源 ID。
2. 成功时写入现有 `data_result` Artifact：`status=success`、行数、是否截断。
3. 失败时写入 `data_result` Artifact：`status=error`、失败阶段、稳定错误码、错误摘要、行数 0。
4. `data.query.observed` 通过可选字段携带相同的错误摘要信息并链接两个 Artifact。
5. Operation Receipt 继续作为成功、失败和 `retryable` 的权威来源。

失败阶段限定为：

- `input_validation`：SQL 为空或缺少资源占位符；
- `sql_guard`：非只读、非法语法边界；
- `vega_query`：Vega 执行失败。

稳定错误码限定为：

- `RUN_SQL_SQL_REQUIRED`
- `RUN_SQL_RESOURCE_PLACEHOLDER_REQUIRED`
- `RUN_SQL_READ_ONLY_REJECTED`
- `RUN_SQL_VEGA_QUERY_FAILED`

## 边界

- 不修改 MCP `run_sql` 请求与响应。
- 不新增失败专用事件类型或 Artifact 类型。
- 不在本阶段实现 BKN 对象反向映射和跨 Request 语义收敛。
- 不复制查询返回的原始数据行；结果 Artifact 只记录规模与状态。

## 验收标准

- 输入校验、SQL 守卫和 Vega 执行失败均能读取 SQL Query Artifact。
- 请求摘要展示稳定错误码或具体错误摘要，不再只能显示通用失败。
- 成功 `run_sql` 行为和 Artifact 保持兼容。
- Context Loader 与 Agent Observability 相关单元测试通过。
