# bkn-agent Trace Contract

> 状态：阶段二 L2 Evidence 模块接入合同
> 适用版本：L1 `bkn.trace.schema.version=1.0.0`；L2 evidence event `bkn.trace.schema.version=2.0.0`  
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 三段式实施计划.md`、`bkn-docs/docs/foundry/bkn-trace/design/阶段二：证据引用采集与 BKN Trace 核心能力开发计划.md`

## Module

- module name: `bkn-agent`
- owner: OpenBKN Foundry
- service identity: platform internal agent runtime
- runtime: FastAPI / LangChain / LangGraph
- contract version: L1 Trace `1.0.0` + L2 Evidence `2.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `bkn-agent.health` | `GET /api/v1/health` | optional upstream `traceparent` / `bkn-request-id` | FastAPI server span | none |
| `bkn-agent.agent.*` | agent CRUD routes | account identity, request context | FastAPI server span | none in phase one |
| `bkn-agent.chat` | `POST /api/bkn-agent/v1/chat` | account identity, agent id, optional thread id | FastAPI server span, `agent.chat` business span, LangChain spans when OTel enabled | SSE meta/token/tool/error/done；成功输出 emit `claim.created`，工具引用 emit `evidence.refs.created`，结构化输出 emit `structured_output.validated` |
| `bkn-agent.task` | `POST /api/bkn-agent/v1/run` / `POST /invoke/{agent_id}` | account identity, agent id, task context | FastAPI server span, `agent.task` business span, LangChain spans when OTel enabled | task status persisted in DB；成功输出 emit `claim.created`；结构化输出 emit `structured_output.validated`；tool result refs emit `evidence.refs.created` |

## Inbound Context

- accepted headers: `traceparent`, `bkn-request-id`, legacy `x-request-id`, `x-account-id`, `x-account-type`
- `traceparent` parsing: W3C version `00`; all-zero trace id or span id is rejected
- external trace handling: valid upstream `traceparent` is treated as `bkn.trace.entry_boundary=external`
- invalid context handling: invalid `traceparent` is discarded and a new internal trace id is generated
- request id generation: use inbound `bkn-request-id` or legacy `x-request-id` when valid; otherwise generate `req_<uuid>`
- response headers: always return `x-trace-id`, `bkn-request-id`, `x-request-id`, `traceparent`

## Outbound Calls

| target | protocol | propagated fields | baggage policy | timeout | retry |
| --- | --- | --- | --- | --- | --- |
| toolbox / MCP tools | HTTP / MCP adapter | phase-one propagation pending in toolbox client | baggage not propagated | existing tool timeout policy | existing retry policy |
| model provider | OpenAI-compatible HTTP through LangChain | OTel instrumentation when enabled | baggage not propagated | model/provider config | provider policy |
| checkpoint store | MySQL | request context not propagated | baggage not propagated | DB config | DB driver policy |
| BKN Trace evidence ingest | HTTP POST | `traceparent`, `bkn.request.id`, account identity in event batch | baggage not propagated | `BKN_TRACE_EVIDENCE_TIMEOUT_S` default 3s | no retry; fail-open with warning |

## Logs

| log type | level | required fields | indexed fields | sensitive fields | fixture |
| --- | --- | --- | --- | --- | --- |
| request context | implicit via response headers and span attributes | `trace_id`, `bkn.request.id`, `bkn.module.name` | module, operation, status | no prompt/SQL/token | `app/test/test_smoke.py` |
| error | error / HTTP error response | `trace_id`, error envelope fields | error code | no raw secret | `app/test/test_smoke.py` |
| OTel setup | info/warning | service name, endpoint summary | module | no token | covered by import path |

## Spans

| span name | kind | required attributes | parent/link rule | error mapping |
| --- | --- | --- | --- | --- |
| FastAPI server span | server | `bkn.request.id`, `trace_id`, route, status when OTel enabled | follows OTel FastAPI instrumentation | HTTP status |
| `agent.chat` | internal | `bkn.agent.id`, `bkn.thread.id`, `bkn.prompt.source`, `bkn.prompt.version`, `bkn.request.id` | child of request span when active | stream error event in phase one |
| `agent.task` | internal | `bkn.agent.id`, `task.depth`, `bkn.prompt.source`, `bkn.prompt.version`, `bkn.request.id` | child of request span when active | exception maps to task failure |

## Events

| event type | producer | payload summary | partial reason | retention class |
| --- | --- | --- | --- | --- |
| SSE `meta/token/tool_call/structured/done/error` | chat stream | user-visible stream protocol | not BKN Trace event envelope yet | transient stream |
| `claim.created` | `bkn-agent` | answer or structured output claim; stores `claim_hash`, agent/thread/task/prompt/schema refs; no raw answer | `source_refs_pending` when no tool/source refs observed | evidence event |
| `evidence.refs.created` | `bkn-agent` | tool call refs by sanitized tool name and hash-derived ref id | omitted when no tool refs observed | evidence event |
| `structured_output.validated` | `bkn-agent` | claim id, response format schema hash, validation result, `validation_path=native|fallback` | none | evidence event |
| `tool.budget.exhausted` | `bkn-agent` | max tool call cap and sanitized tool name | `tool_budget_exhausted` | evidence event |
| `agent_as_tool.invoked` | `bkn-agent` | parent thread id, child task id, child agent id, depth, message hash | none | evidence event |
| `task.status.changed` | pending follow-up | task old/new status and failure summary | not implemented in this PR | business event |
| `tool.called` / `tool.failed` | pending follow-up | tool id/name and hash-only args/result | toolbox client propagation pending | business event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| agent | `bkn.agent.id` | agent DAO | agent update time | account-scoped APIs |
| thread | `bkn.thread.id` | thread DAO/checkpointer | thread update time | owner scoped |
| task | `bkn.task.id` | task DAO | task update time | owner scoped |
| prompt | `bkn.prompt.version` | prompt DAO | prompt version | account override rules |

## Sensitive Data Rules

- never log: authorization, cookie, access token, full prompt, full model output, full tool args/result
- hash only: prompt/tool/model payload evidence, user message, model answer, structured output
- controlled reference: future evidence snapshot refs
- redact: HTTP error responses use short error summaries
- `data.classification`: not emitted by bkn-agent phase-one code yet

## Evidence Submission

- default mode: disabled when `BKN_TRACE_EVIDENCE_INGEST_URL` is empty; bkn-agent still constructs events in code paths but does not submit.
- enabled mode: POST phase-two batch to `BKN_TRACE_EVIDENCE_INGEST_URL`.
- fail behavior: fail-open with warning; bkn-agent response, task execution and tool execution must not depend on BKN Trace availability.
- account context: event batch includes `bkn.account.id` / `bkn.account.type`; `business_domain` is temporarily set to account id until upstream business domain propagation is available.
- payload boundary: never submit raw prompt, raw user message, raw answer, raw SQL, row data, token, cookie, authorization, or object storage URL.

## Sampling

- default: OTel sampler/provider default
- forced sampling: not implemented in bkn-agent phase-one code
- not sampled behavior: response headers and error trace id still returned
- dropped counters: pending BKN Trace governance metrics

## Retention And Alerts

- log retention class: runtime log retention by deployment
- event retention class: not emitted yet
- audit retention class: HTTP auth failures return trace id; audit event emission pending
- health metrics: pending BKN Trace governance metrics
- alert thresholds: pending platform-level alerting

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| positive | `fixtures/bkn-trace/positive.json` + `app/test/test_smoke.py::test_health` | generated request id / trace headers | pass |
| propagation | `fixtures/bkn-trace/propagation.json` + `app/test/test_smoke.py::test_trace_context_propagates_request_id_and_traceparent` | inbound `traceparent` and `bkn-request-id` propagation | pass |
| negative | `fixtures/bkn-trace/negative_invalid_traceparent.json` + `app/test/test_smoke.py::test_invalid_traceparent_is_not_reused` | invalid all-zero traceparent rejected | fail for contract fixture; pass for pytest rejection behavior |
| sampling | `fixtures/bkn-trace/sampling.json` + `app/test/test_smoke.py::test_auth_fail_closed_without_identity` | forced retained error / error body trace id equals `x-trace-id` | pass |
| phase2 chat L2 | `fixtures/bkn-trace/phase2/chat_l2_positive.json` + `app/test/test_evidence.py` | answer claim + tool evidence refs | pass |
| phase2 structured L2 | `fixtures/bkn-trace/phase2/structured_output_l2_positive.json` + `app/test/test_structured_output.py` | structured output claim + schema hash + validation path | pass |
| phase2 agent-as-tool | `fixtures/bkn-trace/phase2/agent_as_tool_l2_positive.json` + `app/test/test_limits_and_gates.py` | child task invocation + depth relation | pass |

## Known Gaps

- toolbox/MCP outbound propagation is not fully implemented yet.
- source refs from context-loader/BKN/Vega/Action are represented only as tool-call refs until downstream modules emit L2/L3 business refs.
- task status changed events are still DB state transitions, not BKN Trace events.
- evidence submission requires `BKN_TRACE_EVIDENCE_INGEST_URL`; default deployment keeps it empty until bkn-trace ingestion service address is configured.
- forced sampling and dropped counters are not implemented yet.
- `business_domain` is temporarily derived from account id; dedicated business-domain propagation is a follow-up.
