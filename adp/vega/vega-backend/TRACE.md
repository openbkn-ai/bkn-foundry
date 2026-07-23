# vega-backend Trace Contract

> 状态：阶段二数据证据接入合同
> 适用版本：`bkn.trace.schema.version=2.0.0`
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 设计.md`、`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 三段式实施计划.md`

## Module

- module name: `vega-data`
- observed service: `vega-backend`
- owner: OpenBKN Foundry / Vega data
- service identity: `vega-backend`
- runtime: Go HTTP service
- repository path: `adp/vega/vega-backend`
- contract version: `2.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `data.resource.query` | resource data query | `traceparent`、`bkn-request-id`、account/auth context | `vega-data.request`、`vega-data.query` | `claim.created`、`evidence.refs.created` |
| `data.catalog.get` | resource metadata list/get | `traceparent`、`bkn-request-id`、account/auth context | `vega-data.request` | `claim.created`、`evidence.refs.created` |
| `data.query.execute` | raw SQL / OpenSearch query | `traceparent`、`bkn-request-id`、resource refs | `vega-data.request`、`vega-data.query` | `data.query.executed`、`data.query.failed` |
| `data.snapshot.create` | snapshot/export follow-up | `traceparent`、`bkn-request-id`、resource refs | `vega-data.snapshot` | `snapshot.created` |

## Inbound Context

- accepted headers / metadata: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`baggage`、`x-account-id`、`x-account-type`、`x-business-domain`。
- `traceparent` parsing: global BKN middleware extracts W3C Trace Context before `TraceContextMiddleware` stores OpenBKN request context.
- external trace trust policy: external trace must pass W3C validation before being treated as parent; unknown `tracestate` should not be propagated.
- invalid context handling: invalid or missing request id is replaced by generated `req_<uuid>` value.
- request id generation: `common.SetTraceContextToCtx` generates a request id when inbound `bkn-request-id` and `x-request-id` are missing or invalid.
- tenant/account/auth context source: external endpoints use OAuth verification; internal endpoints use account headers. Request id is independent of account id and must not be placed in baggage.

## Outbound Calls

| target | protocol | propagated fields | baggage policy | timeout | retry |
| --- | --- | --- | --- | --- | --- |
| permission service | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| model factory | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| bkn-agent semantic understanding | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| local connector / database | internal driver | request id on context; no raw SQL in logs | no baggage | connector timeout | connector policy |

Allowed baggage fields:

```text
bkn.account.type
bkn.runtime.env
```

## Logs

| log type | level | required fields | indexed fields | sensitive fields | example fixture |
| --- | --- | --- | --- | --- | --- |
| business | info | `trace_id`、`span_id`、`bkn.request.id`、`bkn.module.name`、`bkn.operation.name`、`bkn.status` | module、operation、status、resource id、catalog id | full SQL、full result set、row data | `fixtures/bkn-trace/positive.json` |
| error | error | business fields + `error.category`、`error.code`、`error.retryable` | category、code、retryable | raw connector response、connection string | `fixtures/bkn-trace/sampling.json` |
| audit | info | actor、policy、decision、resource ref | decision、resource class | raw data / PII | future policy fixture |

## Spans

| span name | kind | required attributes | parent/link rule | error mapping |
| --- | --- | --- | --- | --- |
| `vega-data.request` | server | module、operation、status、request id、resource/catalog id | HTTP entry span | validation/authz/data/dependency |
| `vega-data.query` | internal/client | resource id、catalog id、query hash、row count、truncated、status | child of request span | query failures map to `data` or `timeout` |
| `vega-data.snapshot` | internal | snapshot ref、hash、classification、retention | child/link from request span | snapshot failures map to `dependency` |

## Events

| event type | producer | payload summary | partial reason | retention class |
| --- | --- | --- | --- | --- |
| `data.query.executed` | vega-backend | resource id、catalog id、query hash、row count、truncated | result truncated / connector unavailable | business event |
| `data.query.failed` | vega-backend | resource id、catalog id、query hash、error code、retryable | timeout / dependency / validation | forced retention on error |
| `snapshot.created` | vega-backend | snapshot ref、hash、format、classification | snapshot unavailable | evidence ref |
| `claim.created` | vega-backend | resource id、catalog id、query hash、returned count、evidence refs hash | data refs unversioned | business event |
| `evidence.refs.created` | vega-backend | `resource_ref` and `row_ref` controlled refs with summary hash | resource/row refs unversioned | business event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| resource | `bkn.resource.id` | Vega resolver | resource version / schema version | account/domain policy |
| catalog | `bkn.catalog.id` | Vega resolver | catalog version | account/domain policy |
| query | `query.hash` | Vega query resolver | query template id / hash | account/domain policy |
| row/snapshot | `snapshot.ref` / row refs | Vega snapshot resolver | snapshot/as_of | account/domain policy |

## Sensitive Data Rules

- never log: token、authorization、cookie、完整 SQL、完整结果集、行级数据、PII、连接串、对象存储裸 URL。
- hash only: raw SQL、OpenSearch DSL、query body、large result summary。
- runtime evidence event payload only contains safe query shape hash、row hash、counts、resource/catalog ids and controlled refs; full SQL、filter values、output field names and row data are not emitted.
- controlled reference: row refs、snapshot refs、large result artifacts。
- redact: unauthorized resource detail、PII fields、secret connection metadata。
- `data.classification`: `public|internal|confidential|pii|secret`。
- scanner patterns covered: token、authorization、cookie、SQL、PII、裸 URL、连接串。
- current code baseline: raw query and logic view SQL logs use `query.SafeQuerySummary` to emit `query_hash` and `query_length` instead of raw SQL.

## Sampling

- default: normal query success can follow platform sampling policy.
- forced sampling: `error`、`timeout`、`denied`、connector failure、query validation failure。
- not sampled behavior: keep required business log and dropped counters.
- dropped counters: S3 follow-up.

## Retention And Alerts

- log retention class: diagnostic/business logs.
- event retention class: business event; error and denied paths forced retention.
- audit retention class: policy decision and resource refs only, no raw source data.
- health metrics: missing request id rate、missing traceparent rate、orphan span rate、event validation failure rate、sensitive field rejection count、dropped count。
- alert thresholds: configured by deployment; sensitive field rejection and validation failure should alert immediately in CI.

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| positive | `fixtures/bkn-trace/positive.json` | query hash success baseline | pass |
| negative | `fixtures/bkn-trace/negative_sensitive_sql.json` | full SQL leakage rejection | fail |
| propagation | `fixtures/bkn-trace/propagation.json` | snapshot/resource propagation | pass |
| sampling | `fixtures/bkn-trace/sampling.json` | forced sampled timeout | pass |
| phase2 vega data evidence | `fixtures/bkn-trace/phase2/vega_data_evidence_l2_positive.json` | resource metadata and row refs baseline | pass |

## Covered GWT

- GWT-01 无上游上下文。
- GWT-02 可信上游 Trace Context。
- GWT-08 工具或依赖失败。
- GWT-10 敏感数据扫描。
- GWT-12 强制保留。
- GWT-13 字段索引分层。
- GWT-15 partial evidence。

## Known Gaps

- legacy phase-one event names `data.query.executed/data.query.failed` are superseded by phase-two `claim.created` / `evidence.refs.created` for implemented successful resource metadata and resource data query paths.
- snapshot refs and immutable evidence artifact storage remain a follow-up; current data evidence emits resource/row refs and summary hashes only.
- outbound permission/model-factory/bkn-agent clients should be migrated to `common.MergeTraceHeaders` in follow-up commits.
- full registry validation and indexing policy validation currently rely on `bkn-docs` validator follow-up.
- S3 health metrics are not implemented yet.

## Owner Sign-off

- owner: OpenBKN Foundry / Vega data
- reviewed at: 2026-07-23
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
