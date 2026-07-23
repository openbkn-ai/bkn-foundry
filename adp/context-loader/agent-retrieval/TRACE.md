# context-loader Trace Contract

> 状态：阶段二 L2 证据事件局部接入
> 适用版本：`bkn.trace.schema.version=2.0.0`（阶段一日志/传播 fixture 仍保留 `1.0.0`）
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `context-loader`
- owner: OpenBKN Foundry / context-loader
- service identity: `context-loader`
- runtime: Go HTTP / MCP / toolbox service
- repository path: `adp/context-loader/agent-retrieval`
- contract version: `2.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `context.search_schema` | schema search HTTP/MCP/tool call | `traceparent`、`bkn-request-id`、account/auth context | `context-loader.request`、`context-loader.search` | `claim.created`、`evidence.refs.created`、`context.refs.resolved` |
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
| `claim.created` | context-loader | schema search result-set finding hash、kn id、query hash、result counts | schema refs unversioned | evidence event |
| `evidence.refs.created` | context-loader | object/relation/action/metric schema refs、summary hash、visibility、version status | schema ref unversioned | evidence event |
| `tool.called` | context-loader | tool id/name、args hash、result count、duration | source unavailable / result truncated | business event |
| `tool.failed` | context-loader | tool id/name、error code、retryable | dependency timeout / validation failed | forced retention on error |
| `context.refs.resolved` | context-loader | source ref count、classification、truncated | missing version / unauthorized / truncated | business event |

## Phase 2 Evidence Event Rules

- `context.search_schema` 在成功返回后发射一组局部 L2 事件：`claim.created` 表示“本次 schema 检索产生了一个候选上下文 finding”，`evidence.refs.created` 记录该 finding 使用的 schema/action/metric refs。
- `claim.created.payload.claim_type` 固定为 `finding`，`claim_hash` 只基于结果数量、`kn_id`、`query_hash` 等摘要字段计算，不包含原始 query、schema 名称、comment、字段说明或完整结果。
- `evidence.refs.created.payload.evidence_refs` 只包含 `ref_id`、`ref_type`、`source_system`、`summary_hash`、`validity`、`version_status`、`visibility`、`partial_reason`；`summary_hash` 来自安全摘要，不包含原始 schema 内容。
- `version_status` 当前为 `unversioned`，并必须携带 `partial_reason=["schema_ref_unversioned"]`；后续接入 BKN schema version / snapshot 后才能改为 `versioned`。
- 证据事件上报由 `BKN_TRACE_EVIDENCE_INGEST_URL` 控制，默认关闭；开启后异步提交，不阻塞 schema 检索主路径。
- 上报失败只记录 warning，不改变业务响应；BKN Trace 核心服务负责后续 Evidence Graph 汇聚、查询、快照和 Studio 可视化。

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
| phase2 positive | `fixtures/bkn-trace/phase2/search_schema_l2_positive.json` | schema search L2 finding and evidence refs | pass |
| phase2 negative | `fixtures/bkn-trace/phase2/negative_raw_query_payload.json` | raw query/prompt payload rejection | fail |

## Covered GWT

- GWT-02 可信上游 Trace Context。
- GWT-05 baggage 违规。
- GWT-06 MCP/toolbox 工具调用。
- GWT-08 工具或依赖失败。
- GWT-10 敏感数据扫描。
- GWT-13 字段索引分层。
- GWT-21 Given 合法入站 trace/request/account context，When `search_schema` 成功返回候选对象/关系/行动/指标，Then 模块发射 `claim.created` 和 `evidence.refs.created`，且事件可通过同一 `trace_id`、`span_id`、`bkn.request.id` 关联。
- GWT-22 Given schema 检索 query 与候选结果含业务名称/comment/字段，When 生成 L2 证据事件，Then payload 只包含 hash/ref/count，不包含原始 query、完整 schema、字段说明或结果行。
- GWT-23 Given 未配置 `BKN_TRACE_EVIDENCE_INGEST_URL`，When `search_schema` 成功执行，Then 业务响应不受影响且不上报事件。
- GWT-24 Given Trace 后端暂时不可用，When 异步上报失败，Then 只产生 warning，不改变 `search_schema` 的响应状态。

## Known Gaps

- runtime L2 emitter currently covers `context.search_schema`; `query_object_instance`、`query_instance_subgraph`、`run_sql` and resolver-level source refs remain follow-up.
- cross-module global Evidence Graph assembly is owned by BKN Trace core service, not by context-loader.
- full registry validation and indexing policy validation currently rely on `bkn-docs` validator follow-up.
- audit-grade evidence snapshot, versioned schema refs, source data snapshot refs, and Studio visualization belong to later BKN Trace phases.
- S3 health metrics are not implemented yet.

## Owner Sign-off

- owner: OpenBKN Foundry / context-loader
- reviewed at: 2026-07-23
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
