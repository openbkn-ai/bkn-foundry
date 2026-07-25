# bkn-agent BKN Trace 接入规范

> 状态：2.1 核心业务事件实施基线
> 更新时间：2026-07-25
> 依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 1. 模块职责

- 模块名：`bkn-agent`；运行形态：FastAPI/LangChain/LangGraph。
- 代码路径：`infra/bkn-agent`。
- L1 Trace 使用 `1.0.0`，Evidence event 默认写入 `2.1.0`。
- bkn-agent 是 BKN Trace 的一个观测对象，不定义 BKN Trace 的产品边界。
- 本模块只陈述自己拥有的交互、工具采用、结论及行动建议事实；BKN、Vega、模型和 execution 各自陈述其事实。

## 2. 入口操作

| 操作 | 入口 | 事件 |
| --- | --- | --- |
| `bkn.agent.chat` | `POST /api/bkn-agent/v1/chat` | interaction、tool、采用来源、claim、refs |
| `bkn.agent.task` | `POST /api/bkn-agent/v1/run`、`POST /invoke/{agent_id}` | 与 chat 相同的 2.1 因果链 |
| `bkn.agent.tool.call` | toolbox、MCP、agent-as-tool、内置工具 | `tool.called -> tool.result.observed` |
| `bkn.agent.action.recommend` | 已有 claim 上形成明确 Action 建议 | `action.recommended -> action.approval_requested` |

健康检查和普通 CRUD 只保留技术 Trace，不伪造业务事件。

## 3. Trace Context 与因果字段

入站接受 `traceparent`、`bkn-request-id`、`x-request-id`、`x-account-id`、`x-account-type`。合法 W3C traceparent 才复用；非法或缺失时生成本地 trace/request 标识。

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
- 同一请求上下文重建相同事件时，ID、时间和 payload 必须完全一致。

## 4. 工具调用

1. 调用前生成 `tool.called`，只记录 tool id/name、args hash、visibility、version status。
2. 向 toolbox/MCP/下游传播 interaction、operation、called event 和 attempt。
3. 调用后生成 `tool.result.observed`，成功只记录 result hash/length/count；失败只记录 error category/hash。
4. 只有 Agent 实际采用的成功结果才进入 `source_event_ids/operation_ids`，并供最终 claim 使用。
5. 不保存工具参数、工具结果、下游错误原文或行级数据。

外部 MCP 和非原生 LangChain 工具使用统一 interceptor；OpenBKN toolbox 在 HTTP 执行边界原生记录，避免重复事件。

## 5. 结论与引用

| 事件 | 精确语义 |
| --- | --- |
| `claim.created` | 最终结论哈希及非空 `source_event_ids/operation_ids` |
| `evidence.refs.created` | 只引用实际采用的 tool result event |
| `business.refs.resolved` | 只从结构化结果的注册字段提取 hash-derived refs |

- 无 adopted source 或 operation 时不得生成孤立 claim。
- 有业务引用时 `resolver_status=resolved`；无引用时才可为 `unresolved` 且 refs 为空。
- 业务引用仅含注册字段，不含 label、物理表名、字段名、原始业务 ID、行内容或裸 URL。
- bkn-agent 不从自然语言猜测对象、属性、关系、数据或 Action 引用。

## 6. Action 建议责任

bkn-agent/第三方 Agent 可复用 helper 负责：

```text
action.recommended -> action.approval_requested
```

这两个事件必须绑定已有 claim、同一 interaction/operation/action instance，并先可靠写入 BKN Trace。随后调用 `action_execution_headers` 生成受控执行头，其中包含 approval request event ID。operator-integration 只接续 approved/rejected/executed/result_recorded。

helper 不会自动把普通工具调用解释为 Action；缺 claim、operation、causation、target refs 或 policy 时返回空，不制造孤立事件。

## 7. 事件精确字段

| 事件 | payload |
| --- | --- |
| `agent.interaction.started` | `intent_hash`、`mode`、`agent_id` |
| `tool.called` | `tool_id`、`tool_name`、`args_hash`、`visibility`、`version_status` |
| `tool.result.observed` | tool、status、result/error hash、长度/计数、visibility/version status |
| `claim.created` | claim id/type/hash、source event ids、operation ids、visibility/version status |
| `evidence.refs.created` | `claim_id`、`evidence_refs` |
| `business.refs.resolved` | `claim_id`、`resolver_status`、`business_refs` |
| `action.recommended` | action instance/type、target refs、reason hash、`status=recommended` |
| `action.approval_requested` | action instance、policy ref、`status=approval_requested` |

所有 `*_hash` 使用 `sha256:<64 位小写十六进制>`。

## 8. 敏感数据边界

禁止进入 event、普通日志、span 和 Studio 响应：Authorization、Cookie、token、API key、完整 prompt/用户问题/模型输出、工具参数/结果、SQL/参数、审批意见、PII、行级数据、对象存储裸 URL。

OpenInference 默认隐藏输入、输出、消息文本、模型参数、工具 schema 和 prompt。BKN Trace 不可用时 fail-open，不影响 Agent 响应或任务状态。

## 9. Given-When-Then 验收

- Given chat/run 开始，When 建立交互，Then 只保存 intent hash，不保存用户问题。
- Given 工具成功，When Agent 采用结果形成结论，Then claim 可回到 tool result 与 operation。
- Given 工具返回结构化 BKN/Vega 标识，When 解析引用，Then只生成 hash-derived refs，不保存原始 ID/行。
- Given 没有采用来源，When Agent 形成文本输出，Then 不生成孤立 claim。
- Given 同一上下文重放，When 重建事件，Then事件完整内容一致。
- Given 已有 claim 和明确 Action，When 请求审批，Then Agent 生成 recommended/requested，execution 不重复生成。
- Given Trace ingest 未配置或失败，When Agent 执行，Then业务结果不受影响。

## 10. Fixture 与测试

- 2.1 正向 fixture：`fixtures/bkn-trace/phase2/chat_l2_positive.json`。
- 核心测试：`app/test/test_evidence.py`。
- 工具传播测试：`app/test/test_toolbox_tools.py`、`app/test/test_limits_and_gates.py`。
- 语法门禁：`python3 -m py_compile app/evidence.py app/observability.py app/core/*.py`。

## 11. 当前边界

- 模型事实由 mf-model-api 生成；当前 Agent 只有在真实采用来源可见时才生成 claim。
- Action helper 已提供，但现有通用工具调用不会自动产生行动建议；需要上层 Agent/工作流在 claim 后显式调用。
- `business_domain` 暂由 account id 派生，独立业务域传播后续统一。
- `BKN_TRACE_EVIDENCE_INGEST_URL` 为空时仅保留技术 Trace。
- task status、强制采样、丢弃计数和完整健康指标后续接入治理层。

## 12. 责任确认

- 责任方：OpenBKN Foundry / bkn-agent。
- 评审日期：待定。
- 兼容风险：中；业务事件严格收敛到 2.1 注册表，旧私有事件不再进入核心索引。
