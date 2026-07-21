# bkn-agent Trace Contract

> 状态：阶段一模块接入合同
> 适用版本：`bkn.trace.schema.version=1.0.0`  
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `bkn-agent`
- owner: OpenBKN Foundry
- service identity: platform internal agent runtime
- runtime: FastAPI / LangChain / LangGraph
- contract version: `1.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `bkn-agent.health` | `GET /api/v1/health` | optional upstream `traceparent` / `bkn-request-id` | FastAPI server span | none |
| `bkn-agent.agent.*` | agent CRUD routes | account identity, request context | FastAPI server span | none in phase one |
| `bkn-agent.chat` | `POST /api/bkn-agent/v1/chat` | account identity, agent id, optional thread id | FastAPI server span, `agent.chat` business span, LangChain spans when OTel enabled | SSE meta/token/tool/error/done stream events; BKN Trace event envelope is phase-two work |
| `bkn-agent.task` | `POST /api/bkn-agent/v1/run` / `POST /invoke/{agent_id}` | account identity, agent id, task context | FastAPI server span, `agent.task` business span, LangChain spans when OTel enabled | task status is persisted in DB; BKN Trace event envelope is phase-two work |

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
| `task.status.changed` | pending phase-two emitter | task old/new status and failure summary | emitter not implemented in phase one code | business event |
| `tool.called` / `tool.failed` | pending toolbox propagation | tool id/name and hash-only args/result | toolbox client propagation pending | business event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| agent | `bkn.agent.id` | agent DAO | agent update time | account-scoped APIs |
| thread | `bkn.thread.id` | thread DAO/checkpointer | thread update time | owner scoped |
| task | `bkn.task.id` | task DAO | task update time | owner scoped |
| prompt | `bkn.prompt.version` | prompt DAO | prompt version | account override rules |

## Sensitive Data Rules

- never log: authorization, cookie, access token, full prompt, full model output, full tool args/result
- hash only: future prompt/tool/model payload evidence
- controlled reference: future evidence snapshot refs
- redact: HTTP error responses use short error summaries
- `data.classification`: not emitted by bkn-agent phase-one code yet

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

## Known Gaps

- toolbox/MCP outbound propagation is not fully implemented yet.
- structured BKN Trace event envelope emission is not implemented yet.
- forced sampling and dropped counters are not implemented yet.
- full event emitter fixtures are contract-level until the phase-two BKN Trace event envelope exists.
