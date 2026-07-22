# context-loader Trace Contract

> 状态：阶段一模块接入合同  
> 适用版本：`bkn.trace.schema.version=1.0.0`  
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `context-loader`
- owner: OpenBKN Foundry / context-loader
- service identity: `context-loader`
- runtime: Go HTTP / MCP / toolbox service
- repository path: `adp/context-loader/agent-retrieval`
- contract version: `1.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `context.search_schema` | schema search HTTP/MCP/tool call | `traceparent`、`bkn-request-id`、account/auth context | `context-loader.request`、`context-loader.search` | `context.refs.resolved` |
| `context.query_object` | object query HTTP/MCP/tool call | `traceparent`、`bkn-request-id`、account/auth context | `context-loader.request`、`context-loader.source.resolve` | `tool.called`、`tool.failed` |
| `context.load_refs` | source refs load | `traceparent`、`bkn-request-id`、business refs | `context-loader.source.resolve` | `context.refs.resolved` |
| `context.resolve_source` | source resolver call | `traceparent`、`bkn-request-id`、resource refs | `context-loader.source.resolve` | `context.refs.resolved` |

## Inbound Context

- accepted headers / metadata: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`baggage`、`x-account-id`、`x-account-type`、`user_id`。
- `traceparent` parsing: HTTP trace middleware extracts W3C Trace Context into OTel context; invalid external trace must not be propagated as an internal parent.
- external trace trust policy: external trace can be linked or used as parent only after format validation and boundary classification.
- invalid context handling: invalid or missing request id is replaced by a generated `req_<uuid>` value.
- request id generation: `SetTraceContextToCtx` generates a request id when inbound `bkn-request-id` and `x-request-id` are missing or invalid.
- tenant/account/auth context source: auth middleware reads account headers or public token introspection result; request id is independent of account id and must not be placed in baggage.

## Outbound Calls

| target | protocol | propagated fields | baggage policy | timeout | retry |
| --- | --- | --- | --- | --- | --- |
| BKN backend / ontology | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| Vega/data | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| Operator integration / toolbox | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| MCP tools | MCP metadata / returned headers | `bkn-request-id`、account headers、allowed baggage | allowlist only | caller controlled | caller controlled |

Allowed baggage fields:

```text
bkn.account.type
bkn.runtime.env
```

Trust policy:

- `bkn.account.type` is an observability classification only, not an authentication or authorization source.
- inbound client-provided `bkn.account.type` in `baggage` is not trusted and is dropped during trace context sanitization.
- outbound `bkn.account.type` is derived from the server-side `AccountAuthContext` produced by header auth or token introspection.
- downstream services must use account/auth headers and local policy context for access decisions, never `baggage`.

## Logs

| log type | level | required fields | indexed fields | sensitive fields | example fixture |
| --- | --- | --- | --- | --- | --- |
| business | info | `trace_id`、`span_id`、`bkn.request.id`、`bkn.module.name`、`bkn.operation.name`、`bkn.status` | module、operation、status、tool name、object type | source row、full result、signed URL | `fixtures/bkn-trace/positive.json` |
| error | error | business fields + `error.category`、`error.code`、`error.retryable` | category、code、retryable | raw tool output、raw HTTP body | `fixtures/bkn-trace/sampling.json` |
| audit | info | actor、policy、decision、resource ref | decision、resource class | raw resource content | 后续权限拒绝 fixture 补齐 |

## Spans

| span name | kind | required attributes | parent/link rule | error mapping |
| --- | --- | --- | --- | --- |
| `context-loader.request` | server | module、operation、status、request id | HTTP/MCP entry span | HTTP 4xx/5xx maps to validation/authz/dependency/tool |
| `context-loader.search` | internal/client | kn id、object type、result count、duration | child of request span | search failure maps to schema/data/tool |
| `context-loader.source.resolve` | internal/client | resource ref、row count、truncated、partial reason | child or linked async span | resolver failure maps to data/dependency |

## Events

| event type | producer | payload summary | partial reason | retention class |
| --- | --- | --- | --- | --- |
| `tool.called` | context-loader | tool id/name、args hash、result count、duration | source unavailable / result truncated | business event |
| `tool.failed` | context-loader | tool id/name、error code、retryable | dependency timeout / validation failed | forced retention on error |
| `context.refs.resolved` | context-loader | source ref count、classification、truncated | missing version / unauthorized / truncated | business event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| knowledge network | `bkn.kn.id` | BKN/ontology resolver | schema version | account/domain policy |
| object type | `bkn.object_type.id` | BKN/ontology resolver | schema version | account/domain policy |
| property | `bkn.property.id` | BKN/ontology resolver | schema version | account/domain policy |
| relation type | `bkn.relation_type.id` | BKN/ontology resolver | schema version | account/domain policy |
| resource | `bkn.resource.id` | Vega/data resolver | resource version / snapshot | account/domain policy |
| tool | `bkn.tool.name` | toolbox registry | tool contract version | account/domain policy |

## Sensitive Data Rules

- never log: token、authorization、cookie、完整 prompt、完整 SQL、完整工具输入输出、完整 source row、PII、对象存储裸 URL、连接串。
- hash only: tool args、tool result、query text、large result summary。
- controlled reference: source refs、row refs、snapshot refs、large result artifacts。
- redact: unauthorized source detail、PII fields、secret connection metadata。
- `data.classification`: `public|internal|confidential|pii|secret`。
- scanner patterns covered: token、authorization、cookie、prompt、SQL、PII、裸 URL、连接串。
- telemetry span policy: HTTP headers are sanitized before being written to span attributes; request body is not recorded as raw content and is replaced by a redaction marker.

## Sampling

- default: normal success path can use sampled or not sampled based on platform policy.
- forced sampling: `error`、`timeout`、`denied`、`tool.failed`、source resolver failure must be retained.
- not sampled behavior: keep required business log and dropped counters.
- dropped counters: `dropped span/event/log count` must be emitted by later telemetry integration.

## Retention And Alerts

- log retention class: diagnostic/business logs.
- event retention class: business event; error and denied paths forced retention.
- audit retention class: policy decision and resource refs only, no raw source data.
- health metrics: missing request id rate、missing traceparent rate、orphan span rate、event validation failure rate、sensitive field rejection count、dropped count。
- alert thresholds: configured by deployment; sensitive field rejection and validation failure should alert immediately in CI.

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| positive | `fixtures/bkn-trace/positive.json` | schema search success baseline | pass |
| negative | `fixtures/bkn-trace/negative_baggage.json` | forbidden baggage field | fail |
| propagation | `fixtures/bkn-trace/propagation.json` | inbound/outbound request context | pass |
| sampling | `fixtures/bkn-trace/sampling.json` | forced sampled tool error | pass |

## Covered GWT

- GWT-02 可信上游 Trace Context。
- GWT-05 baggage 违规。
- GWT-06 MCP/toolbox 工具调用。
- GWT-08 工具或依赖失败。
- GWT-10 敏感数据扫描。
- GWT-13 字段索引分层。

## Known Gaps

- full `tool.called/tool.failed/context.refs.resolved` runtime event emitter is not complete in this branch.
- full registry validation and indexing policy validation currently rely on `bkn-docs` validator follow-up.
- partial evidence resolver and audit-grade evidence snapshot belong to later BKN Trace phases.
- S3 health metrics are not implemented yet.

## Owner Sign-off

- owner: OpenBKN Foundry / context-loader
- reviewed at: 2026-07-21
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
