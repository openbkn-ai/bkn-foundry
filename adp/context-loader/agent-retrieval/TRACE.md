# Context Loader BKN Trace 接入合同

> 状态：BKN Trace 3.0 受管生命周期实施基线
> 更新时间：2026-07-31
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/BKN Trace 0.1.3 接入与事件合同注册表.md`

## 一、模块责任

- 模块名：`context-loader`。
- 观测操作：`context.search_schema`、`context.query_object`、`context.query_instance_subgraph`。
- 标准事实：`retrieval.completed`。
- Context Loader 是第三方 Agent 访问 OpenBKN 业务工具的强制协议边界：先向 BKN Trace Core 注册受管 Operation，再执行下游业务调用，最后终结 Attempt 和 Receipt。
- 模块只陈述检索事实，不生成 Claim。最终回答、Claim 和 adopted support 由 Agent/AI 应用在收到 Operation Receipt 后提交。

## 二、受管生命周期

所有 REST 和 MCP 业务工具调用都必须属于 active Conversation 和 active Interaction，并提供一个 Interaction 内唯一的 `operation_key`：

```json
{
  "bkn_context": {
    "conversation_id": "conv_...",
    "interaction_id": "int_...",
    "operation_key": "agent-defined-logical-call-key",
    "parent_operation_id": "op_...",
    "causation_event_ids": ["evt_..."]
  }
}
```

- Conversation 和 Interaction 必须通过 BKN Trace 3.0 生命周期 API 或对应 MCP 工具创建，`Mcp-Session-Id` 不能替代业务 Conversation。
- 缺少、无效、越权、过期或终态上下文时，Context Loader 返回稳定错误码、`required_action` 和安全提示，下游业务调用次数必须为 0。
- Context Loader 使用可信认证上下文确定 tenant、business domain、application principal 和 effective subject；调用方不能在 JSON 中覆盖 Owner。
- `operation_key` 与规范化输入共同保证幂等。相同 key、不同输入返回 `idempotency_conflict`；已有 pending Receipt 返回 `receipt_pending`，不得重复执行下游副作用。

## 三、下游子调用合同

Core 为每次受管业务工具调用分配稳定 `operation_id`、`attempt` 和 `receipt_id`。Context Loader 把这些可信标识写入当前 Context，再传播给 BKN、ontology、Vega、模型或 Operator 子调用。

- 同一逻辑调用的网络重试复用 `operation_key`，响应丢失后先查询 Operation/Receipt，不重新执行下游调用。
- 只有上一 Attempt 是 retryable failed 时，才能调用 `bkn_retry_operation` 创建 `ready` 状态的下一 Attempt；随后必须以同一 `operation_key` 重调原业务工具，由 Core 原子领取执行权并转为 `pending`。只有首次领取者执行下游，其他并发调用返回 `receipt_pending`。
- `created` 只表示 Operation 是否首次创建，`execute` 表示本次 ensure 是否获得当前 Attempt 的执行权；调用方不得用 `created` 推断是否执行。
- Attempt 的终结由 Context Loader 可信适配器根据实际下游结果完成。第三方 Agent 只能查询 Operation/Receipt，不能提交 `payload_hash`、`outcome` 或直接终结 Receipt。
- `conversation_id`、`interaction_id`、`operation_id`、`attempt`、business domain 和因果标识随可信内部调用传播。
- 调用不可信第三方前必须剥离 OpenBKN 业务因果、业务域和 observed time。

## 四、Receipt 输出合同

不同传输保留业务响应的原生形状，因此 Receipt 使用以下稳定载体：

| 场景 | Receipt 载体 | 说明 |
| --- | --- | --- |
| REST 首次正常执行 | 响应头 `bkn-receipt-id`、`bkn-operation-id` | 业务响应体保持不变；调用方用 ID 查询完整 Receipt |
| REST terminal replay 或 pending | JSON 响应体字段 `receipt` | 下游不再执行，返回持久化状态 |
| MCP 正常执行 | `structuredContent.bkn_receipt` | 与工具结构化结果一并返回 |
| MCP terminal replay 或 pending | `structuredContent.receipt` | 下游不再执行，返回持久化状态 |

`receipt_status` 表示业务 Attempt 是否完成，`evidence_durability` 表示证据是否收到 Core durable ACK，两者不得混用：

- 没有 Evidence Ledger durable ACK 时，成功调用只能完成为 `receipt_status=completed`、`evidence_durability=pending`。
- 只有 Evidence 事件已写入 Core Evidence Ledger 和 Projection Outbox，并收到 `durable_ack=true`，才能提交 `evidence_durability=durable`。
- 失败 Attempt 使用 `receipt_status=failed`、`evidence_durability=failed`。
- 当前 #541 生命周期接入不伪造 durable ACK；#544 的 3.0 Evidence Producer 接入后负责把真实 ACK 和 evidence refs 传给 Attempt 完成接口。

## 五、事实与引用

`retrieval.completed.payload` 精确为 `query_hash/candidate_count/truncated/version_status/source_refs`。

- schema 检索保留受控对象、属性、关系、指标、行动引用。
- 对象查询只保留请求中的对象类型和业务属性引用，不使用返回行生成 row ref。
- 子图查询只根据请求的对象类型和关系类型路径形成引用，不从返回节点/边内容推断。
- 引用只含 `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash（可选）`，不得含 query、条件值、属性值、名称、物理名或行内容。

## 六、事件身份与可靠性

- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)`。
- `observed_at/emitted_at` 复用调用方 envelope 的稳定时间。
- 发射函数显式返回真实 event ID；HTTP 对象查询同时返回 `bkn-evidence-event-id`。
- 现有 2.1 fire-and-forget 发送器不构成 3.0 durable ACK，不能据此把 Receipt 标记为 durable。
- 没有有效入站 OTel Span 时，为满足 Core 关联字段约束，Context Loader 从 `request_id` 稳定派生合成 trace/span ID；该 ID 只用于生命周期关联，不表示 OTel 后端必然存在对应 Span。
- 3.0 Evidence Producer 必须使用 #533 durable Outbox，并在 Core 返回 durable ACK 后才标记本地事件已交付。

## 七、验收

- Given 缺少或无效受管上下文，When 调用任一业务工具，Then 返回稳定生命周期错误且下游调用次数为 0。
- Given 相同 `operation_key + normalized_input_hash` 重放，When Receipt 已终态，Then 返回原 Receipt 且下游调用次数仍为 1。
- Given 下游返回错误或 panic，When Context Loader 完成 Attempt，Then Receipt 进入 failed，不遗留永久 pending，也不向调用方泄露 panic 细节。
- Given retryable failed Attempt，When 显式创建下一 Attempt 并以同一 `operation_key` 重调业务工具，Then 新 Attempt 只执行一次；并发重放只返回 pending Receipt。
- Given 第三方 Agent 获取 pending Receipt，When 枚举生命周期工具，Then 不存在允许其自行声明平台输出和终态的 finalize 工具。
- Given 未收到 Core durable ACK，When 成功完成 Attempt，Then Receipt 为 completed + pending。
- Given 没有有效 OTel Span 且同一 request 重放，When 完成 Attempt，Then使用相同合成 trace ID；不同 request 使用不同 ID。

## 八、已知限制

3.0 Evidence Producer、真实 evidence refs、持久 Outbox 和 durable ACK 由 #533/#544 继续实现；在这些依赖落地前，业务执行可以终态，但证据完整性必须保持 assembling/pending，不得显示 complete。

- 成功 Interaction 如果包含 `evidence_durability=pending` 的 Receipt，必须在终结清单中提供 `assembler_deadline`。到期后由 #544 的 assembler 根据 durable evidence 结果收敛为 complete/partial/failed。
- #544 落地前没有持续运行的 assembler，未提供 `assembler_deadline` 的 Interaction 会保持 assembling；这是当前实施阶段的已知中间态，不应误判为 Core 卡死，也不得人为改写为 complete。
- 业务工具领取执行权后如果进程硬崩，受信适配器来不及写入失败终态，Receipt 会保持 pending；第三方 Agent 无权自行终结。当前由交互租约回收将 Interaction 标记为 abandoned，持久恢复与证据收敛由 #533/#544 接续实现。
- REST 与 MCP 遇到业务代码 panic 时均记录 failed 且默认不可重试。是否重试必须由受信适配器基于明确的临时故障信号判断，不能仅因发生 panic 自动放行。
