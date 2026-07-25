# context-loader Trace Contract

> 状态：BKN Trace 2.1 事实 producer 基线
> 适用版本：默认写入 `bkn.trace.schema.version=2.1.0`（历史 fixture 保留 `1.0.0/2.0.0`）
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `context-loader`
- owner: OpenBKN Foundry / context-loader
- service identity: `context-loader`
- runtime: Go HTTP / MCP / toolbox service
- repository path: `adp/context-loader/agent-retrieval`
- contract version: `2.1.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `context.search_schema` | schema search HTTP/MCP/tool call | trace/request + business causality + account/auth | `context-loader.request`、`context-loader.search` | `retrieval.completed`，条件 refs |
| `context.query_object` | object query HTTP/MCP/tool call | trace/request + business causality + account/auth | `context-loader.request`、`context-loader.source.resolve` | `retrieval.completed`，条件 refs |
| `context.query_instance_subgraph` | instance subgraph HTTP/MCP/tool call | trace/request + business causality + account/auth | `context-loader.request`、`context-loader.source.resolve` | `retrieval.completed`，条件 refs |
| `context.load_refs` | source refs load | `traceparent`、`bkn-request-id`、business refs | `context-loader.source.resolve` | `context.refs.resolved` |
| `context.resolve_source` | source resolver call | `traceparent`、`bkn-request-id`、resource refs | `context-loader.source.resolve` | `context.refs.resolved` |

## Inbound Context

- accepted headers / metadata: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`bkn-interaction-id`、`bkn-operation-id`、`bkn-causation-event-id`、可选 `bkn-claim-id/bkn-attempt`、受控 baggage 和 account/auth headers。
- 业务因果 ID 必须分别符合 `int_`、`op_`、`evt_`、`claim_` 前缀及低基数字符集；非法值在 HTTP 边界丢弃，不进入 event 或下游 header。
- 只有认证上下文存在时才提交 evidence batch；合法外形的 header 不构成授权依据。
- `traceparent` parsing: HTTP trace middleware extracts W3C Trace Context into OTel context; invalid external trace must not be propagated as an internal parent.
- external trace trust policy: external trace can be linked or used as parent only after format validation and boundary classification.
- invalid context handling: invalid or missing request id is replaced by a generated `req_<uuid>` value.
- request id generation: `SetTraceContextToCtx` generates a request id when inbound `bkn-request-id` and `x-request-id` are missing or invalid.
- tenant/account/auth context source: auth middleware reads account headers or public token introspection result; request id is independent of account id and must not be placed in baggage.

## Outbound Calls

| target | protocol | propagated fields | baggage policy | timeout | retry |
| --- | --- | --- | --- | --- | --- |
| BKN backend / ontology | internal HTTP | trace/request + three business causality IDs + optional claim/attempt + account | allowlist only | existing client timeout | existing retry policy |
| Vega/data | internal HTTP | trace/request + three business causality IDs + optional claim/attempt + account | allowlist only | existing client timeout | existing retry policy |
| Operator integration / toolbox | internal HTTP | trace/request + three business causality IDs + optional claim/attempt + account | allowlist only | existing client timeout | existing retry policy |
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
| `retrieval.completed` | context-loader | query hash、candidate count、truncated、受控 source refs | ref unversioned | business fact |
| `evidence.refs.created` | context-loader | 仅在收到已存在的上游 claim id 时写入受控 evidence refs | ref unversioned | evidence event |
| `business.refs.resolved` | context-loader | 收到上游 claim id 后写入；无可解析业务 ref 时显式 `unresolved` | resolver partial | evidence event |
| `tool.called` | context-loader | tool id/name、args hash、result count、duration | source unavailable / result truncated | business event |
| `tool.failed` | context-loader | tool id/name、error code、retryable | dependency timeout / validation failed | forced retention on error |
| `context.refs.resolved` | context-loader | source ref count、classification、truncated | missing version / unauthorized / truncated | business event |

## BKN Trace 2.1 Producer Rules

- context-loader 只声明自己观察到的 `retrieval.completed`，不得生成 `claim.created`。
- 无上游 `claim_id` 时只写事实事件，供上游 Agent 后续通过 `source_event_ids` 引用。
- 只有收到已存在、可信传播的上游 `claim_id` 时，才追加 refs 事件；无可解析业务 ref 时必须写 `resolver_status=unresolved` 和空 `business_refs`，不能静默省略。
- `evidence_refs/business_refs/source_refs` 仅允许 `ref_id/ref_type/source_system/summary_hash/validity/version_status/visibility`，不得包含 `summary` 对象。
- `retrieval.completed` payload 精确为 `query_hash/candidate_count/truncated/version_status/source_refs`；refs 事件 payload 额外且仅允许 `claim_id` 与注册的 refs/status 字段。
- 所有 `*_hash` 必须为 `sha256:` 加 64 位小写十六进制；不得用说明性占位文本进入运行事件。
- query、condition、filters、properties、实例 identity、路径条件和返回内容只进入 hash，不得原样写入 payload/ref/log/span。
- `context.query_object` 的 `truncated=true` 只来自明确下一页信号：响应存在 `search_after`，或 `total_count > offset + returned_count`；不得用 `len(data) >= limit` 猜测截断。
- `context.query_instance_subgraph` 的 source refs 最多保留 100 条，超过后事实事件 `truncated=true`；关系类型只从明确关系容器提取，不从普通实例字段启发式推断。
- refs 当前标记 `version_status=unversioned`；后续接入 BKN schema version / snapshot 后才能改为 `versioned`。
- 内部可信调用传播三个业务因果 header 及可选 claim/attempt；调用第三方地址前必须使用 `StripBusinessTraceHeaders` 剥离 OpenBKN 业务上下文。
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
| legacy 2.0 positive | `fixtures/bkn-trace/phase2/search_schema_l2_positive.json` | historical compatibility baseline | pass |
| legacy 2.0 positive | `fixtures/bkn-trace/phase2/query_object_instance_l2_positive.json` | historical compatibility baseline | pass |
| legacy 2.0 positive | `fixtures/bkn-trace/phase2/query_instance_subgraph_l2_positive.json` | historical compatibility baseline | pass |
| phase2 negative | `fixtures/bkn-trace/phase2/negative_raw_query_payload.json` | raw query/prompt payload rejection | fail |
| 2.1 positive | `fixtures/bkn-trace/phase2/retrieval_completed_2_1_positive.json` | fact-only 与 claim 后引用解析时序 | pass |

## Covered GWT

- GWT-02 可信上游 Trace Context。
- GWT-05 baggage 违规。
- GWT-06 MCP/toolbox 工具调用。
- GWT-08 工具或依赖失败。
- GWT-10 敏感数据扫描。
- GWT-13 字段索引分层。
- GWT-21 Given 合法 trace/request/business causality/account context 且无 claim，When 检索成功，Then 只发射 `retrieval.completed`，不得制造 claim。
- GWT-21A Given 上游已创建 claim 并可信传播 claim id，When 执行引用解析，Then 事实、evidence refs、business refs 形成显式因果链。
- GWT-22 Given schema 检索 query 与候选结果含业务名称/comment/字段，When 生成 L2 证据事件，Then payload 只包含 hash/ref/count，不包含原始 query、完整 schema、字段说明或结果行。
- GWT-23 Given 未配置 `BKN_TRACE_EVIDENCE_INGEST_URL`，When `search_schema` 成功执行，Then 业务响应不受影响且不上报事件。
- GWT-24 Given Trace 后端暂时不可用，When 异步上报失败，Then 只产生 warning，不改变 `search_schema` 的响应状态。
- GWT-25 Given 合法入站 context 且无 claim，When `query_object_instance` 成功，Then 模块只发射带受控 row refs 的 `retrieval.completed`。
- GWT-26 Given 对象实例查询条件、属性列表、实例 identity 或返回行包含用户数据，When 生成 L2 证据事件，Then payload 只包含 condition/properties/identity/row hash，不包含原始过滤值、属性名、实例名或行级数据。
- GWT-27 Given 合法入站 context 且无 claim，When `query_instance_subgraph` 成功，Then 模块只发射带受控 row/schema refs 的 `retrieval.completed`。
- GWT-28 Given 子图 relation paths、实例 identity 或返回节点/边包含用户数据，When 生成 L2 证据事件，Then payload 只包含 path/identity/entry hash，不包含原始路径条件、实例主键值或行级数据。

## Known Gaps

- runtime 2.1 emitter covers `context.search_schema`、`context.query_object` 和 `context.query_instance_subgraph`；`run_sql` remains follow-up.
- cross-module global Evidence Graph assembly is owned by BKN Trace core service, not by context-loader.
- full registry validation and indexing policy validation currently rely on `bkn-docs` validator follow-up.
- audit-grade evidence snapshot, versioned schema refs, source data snapshot refs, and Studio visualization belong to later BKN Trace phases.
- S3 health metrics are not implemented yet.

## Owner Sign-off

- owner: OpenBKN Foundry / context-loader
- reviewed at: 2026-07-23
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
