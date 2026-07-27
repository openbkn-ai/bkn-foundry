# bkn-agent BKN Trace 接入规范

> 状态：2.2 业务内容制品与核心业务事件实施基线
> 更新时间：2026-07-27
> 依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 1. 模块职责

- 模块名：`bkn-agent`；运行形态：FastAPI/LangChain/LangGraph。
- 代码路径：`infra/bkn-agent`。
- L1 Trace 使用 `1.0.0`，Evidence event 与 Evidence Artifact 写入 `2.2.0`。
- bkn-agent 是 BKN Trace 的一个观测对象，不定义 BKN Trace 的产品边界。
- 本模块只陈述自己拥有的交互、工具采用、结论及行动建议事实；BKN、Vega、模型和 execution 各自陈述其事实。

## 2. 入口操作

| 操作 | 入口 | 事件 |
| --- | --- | --- |
| `bkn.agent.chat` | `POST /api/bkn-agent/v1/chat` | interaction、tool、模型事实回执、claim、refs |
| `bkn.agent.task` | `POST /api/bkn-agent/v1/run`、`POST /invoke/{agent_id}` | 与 chat 相同的 2.2 因果链 |
| `bkn.agent.tool.call` | toolbox、MCP、agent-as-tool、内置工具 | `tool.called -> tool.result.observed` |
| `bkn.agent.action.recommend` | 预留的显式 Action 语义入口 | 当前只有受控 helper，尚未接入 chat/run 生产图 |

健康检查和普通 CRUD 只保留技术 Trace，不伪造业务事件。

## 3. Trace Context 与因果字段

入站接受 `traceparent`、`bkn-request-id`、`x-request-id`、`x-account-id`、`x-account-type`、`x-tenant-id`、`x-business-domain` 和重放用 `bkn-event-observed-at`。为兼容旧调用方，入站暂时接受 `bkn-trace-observed-at`，但所有出站调用只传播统一头 `bkn-event-observed-at`。tenant 与 business domain 至少提供一个，均不得由 account id 推导；两者同时缺失时不提交 2.2 event batch 或 Artifact。合法 W3C traceparent 才复用；无上游 trace 时，业务 Trace Context 复用当前 OTel Server Span 的 trace/span 身份，确保 Span、Evidence、Artifact、响应头与下游传播只有一个 `trace_id`；仅在 OTel 上下文不可用时才生成本地 trace/request 标识。

每个业务事件贯穿：

```text
trace_id
span_id
bkn.request.id
interaction_id
operation_id（operation 事件）
causation_event_id（存在直接原因时）
claim_id（claim/refs/action）
attempt（默认 1）
```

- interaction ID 由 trace/request/agent/mode/operation 稳定派生。
- 同一交互内的 operation ID 使用稳定调用序号区分。
- event ID 由 trace/request/interaction/operation/event type/claim/attempt 稳定派生。
- 同一请求上下文重建相同事件时，ID、时间和 payload 必须完全一致。跨进程重放必须由调用方恢复原始 `bkn-event-observed-at`；仅复用 ID 而生成新时间不合法。

## 4. 工具调用

1. 调用前生成 `tool.called`，只记录 tool id/name、args hash、visibility、version status。
2. 向 toolbox/MCP/下游传播 interaction、operation、called event 和 attempt。
3. 调用后生成 `tool.result.observed`，成功只记录 result hash/length/count；失败只记录 error category/hash。
4. 本地 `tool.result.observed` 只说明 Agent 看到了调用结果，不自动代表最终采用。
5. 下游 producer 必须先记录事实，再通过 `bkn-evidence-event-id` 与结构化 refs 回执稳定事实 ID；Agent 只将这些回执登记为候选来源。Agent 入站兼容旧的 `bkn-fact-event-id`，出站与新实现只使用标准字段。
6. 不从工具原文、自然语言、字段名或重新哈希的业务 ID 猜测来源和引用。
7. bkn-agent 的普通事件和日志不保存工具参数、工具结果、下游错误原文或行级数据；查询条件、数据结果及逻辑执行全文由拥有该事实的 BKN/Vega/execution producer 写入对应 Artifact，Agent 不重复复制。

外部 MCP 和非原生 LangChain 工具使用统一 interceptor；OpenBKN toolbox 在 HTTP 执行边界原生记录，避免重复事件。

## 5. 结论与引用

| 事件 | 精确语义 |
| --- | --- |
| `claim.created` | 最终结论哈希、`result_artifact_ref` 及模型明确采用的非空 `source_event_ids/operation_ids` |
| `evidence.refs.created` | 只使用下游事实回执或模型回执中的结构化 allowlist refs |
| `business.refs.resolved` | 只使用结构化回执，不解析 label 或原文 |

- 无 adopted source 或 operation 时不得生成孤立 claim。
- 有业务引用时 `resolver_status=resolved`；无引用时才可为 `unresolved` 且 refs 为空。
- 业务引用仅含注册字段，不含 label、物理表名、字段名、原始业务 ID、行内容或裸 URL。
- bkn-agent 不从自然语言猜测对象、属性、关系、数据或 Action 引用。
- 下游返回的全限定 `ref_id` 必须原样进入采用后的 claim binding，不截断、不缩写、不重哈希。当前不生成兼容短 ref；未来若需要短 ref，必须与同一 `source_event_id` 和知识网络 ref 同时保留，禁止根据文本或工具名反推。

### 5.1 mf-model-api 事实回执合同

每次真实模型调用独立创建 operation，并传播 interaction、operation、causation、attempt。仅当下游事实回执对应的工具结果确实以 `ToolMessage` 进入本次模型上下文时，请求才使用 `bkn-candidate-source-event-ids` 发送候选 event ID；通过本地内容哈希做精确关联，不解析文本语义。数量受 `BKN_TRACE_MODEL_SOURCE_LIMIT` 限制且硬上限为 100，单个 ID 只接受受控字符集和 128 字符上限；不发送工具原文或引用内容。

mf-model-api 必须先持久化 `model.call.observed`，再通过响应 header 返回 `bkn-evidence-event-id`、`bkn-adopted-source-event-ids` 及可选 refs。兼容响应 body 仅接受 `additional_kwargs.bkn_trace` 下的 `source_event_id/adopted_source_event_ids/evidence_refs/business_refs`，并兼容读取旧的 `bkn-fact-event-id`；标准 header 优先于兼容字段和 body。

- 每次模型 operation 固化自己的请求候选集；采用集合只能是该次请求候选集的子集，交互内已知但未进入本次上下文的 ID 同样被忽略。
- 有工具候选但采用列表为空或无效：只保留模型事实，内部状态为 `partial`，工具 refs 不进入 claim。
- 纯模型回答：只绑定模型事实与模型 operation。
- mf-model-api 未返回稳定事实 ID：不生成 claim，不由 Agent 伪造模型事实。

## 6. Action helper 与生产边界

受控 helper 可以表达：

```text
action.recommended -> action.approval_requested
```

`claim.created` 确认后才允许生成 recommended；recommended 确认后才允许生成 approval_requested；两者均确认后才允许生成 operator 执行头。每个阶段使用不同且可重放的 `observed_at`。operator-integration 只接续 approved/rejected/executed/result_recorded。

helper 不会自动把普通工具调用解释为 Action；缺 claim、operation、causation、target refs 或 policy 时返回空，不制造孤立事件。

当前 LangGraph chat/run 没有明确的“最终 claim 后生成行动建议并进入审批”的语义节点，因此 helper **未接入生产调用图**，普通 tool call 也不会携带 `bkn-claim-id` 或 `bkn-action-*`。在新增显式 Action 节点/API 前，不能宣称 bkn-agent 到 operator 的生产 Action 闭环已完成。

## 7. 事件精确字段

| 事件 | payload |
| --- | --- |
| `agent.interaction.started` | `intent_hash`、`mode`、`agent_id`、可选 `question_artifact_ref` |
| `tool.called` | `tool_id`、`tool_name`、`args_hash`、`visibility`、`version_status` |
| `tool.result.observed` | tool、status、result/error hash、长度/计数、visibility/version status |
| `claim.created` | claim id/type/hash、source event ids、operation ids、可选 `result_artifact_ref`、visibility/version status |
| `evidence.refs.created` | `claim_id`、`evidence_refs` |
| `business.refs.resolved` | `claim_id`、`resolver_status`、`business_refs` |
| `action.recommended` | action instance/type、target refs、reason hash、`status=recommended` |
| `action.approval_requested` | action instance、policy ref、`status=approval_requested` |

所有 `*_hash` 使用 `sha256:<64 位小写十六进制>`。

## 8. 敏感数据边界

完整用户问题和最终业务结果写入独立 Evidence Artifact，并由 `bkn.account`、业务域和授权查询共同控制可见性；Studio 对有权用户展示制品内容。event、普通日志和 span 只保存引用、哈希和诊断字段，避免重复扩散业务正文。

无论是否为业务内容，Authorization、Cookie、token、API key、密码、私钥和对象存储裸 URL 都不得进入 Artifact、event、日志、span 或 Studio 响应。当前阶段不对一般业务问题和结果做额外内容分类或默认脱敏。

OpenInference 默认隐藏输入、输出、消息文本、模型参数、工具 schema 和 prompt。BKN Trace 不可用时 fail-open，不影响 Agent 响应或任务状态。

## 9. Given-When-Then 验收

- Given chat/run 开始，When 建立交互，Then 先保存 `question` Artifact，再提交只含哈希与 `question_artifact_ref` 的启动事件。
- Given Agent 形成有事实来源的最终结论，When 创建 claim，Then 先保存 `result` Artifact，再提交只含哈希与 `result_artifact_ref` 的 claim。
- Given Artifact 保存失败，When 准备提交启动或结论事件，Then跳过该必需引用不完整的 2.2 业务事件，业务响应和技术 Trace 不受影响。
- Given 工具成功且下游返回稳定事实回执，When 模型明确采用该候选形成结论，Then claim 可回到下游事实、模型事实及对应 operation。
- Given 下游事实进入模型上下文，When 调用最终模型，Then请求携带有上限的候选 event IDs，且 claim 只绑定 mf-model-api 回显采用的候选和模型事实。
- Given 有工具候选但模型回显采用列表为空或非法，When 创建结论，Then只绑定模型事实并保持 partial，不绑定工具 refs。
- Given 没有工具调用，When mf-model-api 返回稳定事实 ID，Then纯模型 claim 只绑定模型事实。
- Given 工具返回结构化 BKN/Vega 标识，When 解析引用，Then只生成 hash-derived refs，不保存原始 ID/行。
- Given mf-model-api 未返回稳定事实 ID，When Agent 形成文本输出，Then不生成孤立 claim。
- Given 同一上下文重放，When 重建事件，Then事件完整内容一致。
- Given 已有 claim 和明确 Action，When 请求审批，Then Agent 生成 recommended/requested，execution 不重复生成。
- Given 普通工具调用，When 向 operator 传播，Then不携带 claim/Action headers，不冒充 Action。
- Given Trace ingest 未配置或失败，When Agent 执行，Then业务结果不受影响。

## 10. Fixture 与测试

- 2.2 正向 fixture：`fixtures/bkn-trace/phase2/chat_l2_positive.json`。
- 核心测试：`app/test/test_evidence.py`。
- Artifact 契约与 task/chat 集成测试：`app/test/test_evidence_artifacts.py`、`app/test/test_evidence_artifact_integration.py`。
- 可靠性与模型采用测试：`app/test/test_evidence_reliability.py`。
- 工具传播测试：`app/test/test_toolbox_tools.py`、`app/test/test_limits_and_gates.py`。
- 语法门禁：`python3 -m py_compile app/evidence.py app/observability.py app/core/*.py`。

## 11. 可靠性与当前边界

- 提交按交互串行，等待父事实成功确认后才继续子事实；非 2xx/超时有界重试，进程正常关闭时有界 drain，最终失败记录安全错误类型。
- 当前没有审计级 durable outbox。进程在内存重试或 drain 完成前崩溃仍可能丢失 evidence，这是未消除的生产风险。
- 模型事实依赖 mf-model-api 实现上述稳定回执合同；依赖未部署时，Agent 有意不生成伪 claim。
- Action helper 已提供可靠顺序门禁，但 chat/run 生产图缺显式 Action 节点，生产闭环仍阻塞。
- 跨进程完整重放依赖上游恢复原始 `bkn-event-observed-at`；本模块未提供请求 envelope 持久化仓库。
- `BKN_TRACE_EVIDENCE_INGEST_URL` 控制核心事件写入；`BKN_TRACE_ARTIFACT_INGEST_URL` 控制业务内容制品写入。Artifact 未确认时，依赖该必需引用的 2.2 启动事件或 claim 不提交。
- task status、强制采样、丢弃计数和完整健康指标后续接入治理层；本批只提供失败日志与提交返回状态。

## 12. 责任确认

- 责任方：OpenBKN Foundry / bkn-agent。
- 评审日期：待定。
- 兼容风险：中；生产者升级到 2.2，消费者必须支持 Artifact 引用字段与独立制品授权查询。
