# ontology-query Trace Contract

> 状态：阶段一模块接入合同  
> 适用版本：`bkn.trace.schema.version=1.0.0`  
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `bkn-ontology`
- observed service: `ontology-query`
- owner: OpenBKN Foundry / BKN ontology
- service identity: `ontology-query`
- runtime: Go HTTP service
- repository path: `adp/bkn/ontology-query`
- contract version: `1.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `bkn.object.query` | object instance query | `traceparent`、`bkn-request-id`、account/auth context | `bkn-ontology.request`、`bkn-ontology.instance.query` | `object.query.executed` |
| `bkn.relation.query` | subgraph/path query | `traceparent`、`bkn-request-id`、account/auth context | `bkn-ontology.request`、`bkn-ontology.instance.query` | `relation.query.executed` |
| `bkn.action_type.get` | action type query | `traceparent`、`bkn-request-id`、account/auth context | `bkn-ontology.request`、`bkn-ontology.schema.lookup` | `schema.read` |
| `bkn.metric.get` | metric dry-run/data query | `traceparent`、`bkn-request-id`、account/auth context | `bkn-ontology.request`、`bkn-ontology.instance.query` | `object.query.executed` |

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
| Vega backend | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| BKN backend / ontology-manager | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| Model factory | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |
| Agent operator / Action services | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing client timeout | existing retry policy |

Allowed baggage fields:

```text
bkn.account.type
bkn.runtime.env
```

## Logs

| log type | level | required fields | indexed fields | sensitive fields | example fixture |
| --- | --- | --- | --- | --- | --- |
| business | info | `trace_id`、`span_id`、`bkn.request.id`、`bkn.module.name`、`bkn.operation.name`、`bkn.status` | module、operation、status、kn id、object type | object instance full properties、row data | `fixtures/bkn-trace/positive.json` |
| error | error | business fields + `error.category`、`error.code`、`error.retryable` | category、code、retryable | raw backend response、raw query payload | `fixtures/bkn-trace/sampling.json` |
| audit | info | actor、policy、decision、resource ref | decision、object/resource class | raw resource content | `fixtures/bkn-trace/sampling.json` |

## Spans

| span name | kind | required attributes | parent/link rule | error mapping |
| --- | --- | --- | --- | --- |
| `bkn-ontology.request` | server | module、operation、status、request id、kn id | HTTP entry span | validation/authz/data/schema |
| `bkn-ontology.schema.lookup` | internal/client | kn id、object/action/metric ids、status | child of request span | schema lookup failures map to `schema` |
| `bkn-ontology.instance.query` | internal/client | kn id、object type、row count、truncated、status | child of request span | query failures map to `data` or `dependency` |

## Events

| event type | producer | payload summary | partial reason | retention class |
| --- | --- | --- | --- | --- |
| `schema.read` | ontology-query | kn id、object/action/metric ids、schema version | schema version missing / unauthorized | business event |
| `object.query.executed` | ontology-query | object type、result count、truncated、classification | result truncated / data unavailable | business event |
| `relation.query.executed` | ontology-query | relation path、node count、edge count、truncated | max path exceeded / unauthorized | business event |
| `schema.change.requested` | BKN backend, not ontology-query | change summary and actor | not emitted by query service | audit event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| knowledge network | `bkn.kn.id` | BKN/ontology resolver | schema version | account/domain policy |
| object type | `bkn.object_type.id` | BKN/ontology resolver | schema version | account/domain policy |
| object instance | `bkn.object_instance.ref` | BKN/ontology + Vega resolver | data snapshot / as_of | account/domain policy |
| property | `bkn.property.id` | BKN/ontology resolver | schema version | account/domain policy |
| relation type | `bkn.relation_type.id` | BKN/ontology resolver | schema version | account/domain policy |
| metric | `bkn.metric.id` | BKN metric resolver | metric definition version | account/domain policy |
| action type | `bkn.action_type.id` | BKN action resolver | action definition version | account/domain policy |

## Sensitive Data Rules

- never log: token、authorization、cookie、完整 SQL、完整对象实例属性、行级数据、PII、连接串、对象存储裸 URL。
- hash only: query body、large result summary、operator inputs。
- controlled reference: `bkn.object_instance.ref`、row refs、snapshot refs。
- redact: unauthorized object/resource detail、PII fields、policy restricted values。
- `data.classification`: `public|internal|confidential|pii|secret`。
- scanner patterns covered: token、authorization、cookie、SQL、PII、裸 URL、连接串。

## Sampling

- default: normal query success can follow platform sampling policy.
- forced sampling: `error`、`timeout`、`denied`、authz failure、backend dependency failure、Action execution failure.
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
| positive | `fixtures/bkn-trace/positive.json` | schema/object/relation query baseline | pass |
| negative | `fixtures/bkn-trace/negative_missing_request_id.json` | missing request id | fail |
| propagation | `fixtures/bkn-trace/propagation.json` | object query propagation | pass |
| sampling | `fixtures/bkn-trace/sampling.json` | forced sampled authz denial | pass |

## Covered GWT

- GWT-02 可信上游 Trace Context。
- GWT-04 非法 traceparent / invalid context handling。
- GWT-09 权限拒绝或脱敏。
- GWT-10 敏感数据扫描。
- GWT-13 字段索引分层。
- GWT-15 partial evidence。

## Known Gaps

- runtime `schema.read`、`object.query.executed`、`relation.query.executed` event emitters are not complete in this branch.
- current code baseline wires Vega backend outbound trace headers; ontology-manager/model-factory/agent-operator should be migrated to `common.MergeTraceHeaders` in follow-up commits.
- full registry validation and indexing policy validation currently rely on `bkn-docs` validator follow-up.
- S3 health metrics are not implemented yet.

## Owner Sign-off

- owner: OpenBKN Foundry / BKN ontology
- reviewed at: 2026-07-21
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
