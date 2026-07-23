# bkn-backend Trace Contract

> 状态：阶段二 L2 schema evidence 局部接入
> 适用版本：`bkn.trace.schema.version=2.0.0`
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 设计.md`

## Module

- module name: `bkn-backend`
- owner: OpenBKN Foundry / BKN Engine
- service identity: `bkn-backend`
- runtime: Go HTTP service
- repository path: `adp/bkn/bkn-backend`
- contract version: `2.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `bkn.schema.object_type.list` | `GET /knowledge-networks/:kn_id/object-types` | `traceparent`、`bkn-request-id`、account/auth context | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.object_type.get` | `GET /knowledge-networks/:kn_id/object-types/:ot_ids` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.relation_type.list` | `GET /knowledge-networks/:kn_id/relation-types` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.relation_type.get` | `GET /knowledge-networks/:kn_id/relation-types/:rt_ids` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.action_type.list` | `GET /knowledge-networks/:kn_id/action-types` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.action_type.get` | `GET /knowledge-networks/:kn_id/action-types/:at_ids` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.metric.list` | `GET /knowledge-networks/:kn_id/metrics` | same | existing server span | `claim.created`、`evidence.refs.created` |
| `bkn.schema.metric.get` | `GET /knowledge-networks/:kn_id/metrics/:metric_ids` | same | existing server span | `claim.created`、`evidence.refs.created` |

## Inbound Context

- accepted headers: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`x-account-id`、`x-account-type`、`x-business-domain`。
- `traceparent` parsing: existing global middleware extracts W3C Trace Context into OTel context before handlers create server spans.
- request id rule: evidence emission uses inbound `bkn-request-id` first, then `x-request-id`; when both are absent it generates `req_<xid>` only for evidence payload completeness.
- account context source: external APIs use OAuth visitor; internal APIs use account headers through `visitor.GenerateVisitor(c)`; baggage is not an authorization source.

## Phase 2 Evidence Event Rules

- Successful schema read paths emit one `claim.created` event and one `evidence.refs.created` event after the service read succeeds and before the HTTP response is written.
- Object type and relation type reads emit `schema_ref`; action type reads emit `action_ref`; metric reads emit `metric_ref`.
- `claim.created.payload.claim_type` is `finding`; `claim_hash` is computed from safe result summary only.
- `claim_id` / `claim_hash` must include a hash of the sorted `ref_id + ref_type + summary_hash` set so same-count list results with different schema refs remain distinguishable.
- `evidence_refs[].summary` may contain IDs, `kn_id`, `branch`, type fields, counts, booleans and timestamps. It must not contain names, comments, property names, mapping rule details, action intent text, metric formula content, SQL, row data, prompt, tool input/output, token, cookie or authorization values.
- `summary_hash` is always present and computed from the safe summary.
- `version_status` is currently `unversioned`; refs must include `partial_reason` such as `schema_ref_unversioned`、`action_ref_unversioned`、`metric_ref_unversioned` until schema/snapshot versioning is connected.
- Evidence event submission is controlled by `BKN_TRACE_EVIDENCE_INGEST_URL`; default is disabled and no business response changes.
- Submission is asynchronous and fail-open; ingestion failures do not change API status or response body.

## Sensitive Data Rules

- never emit: token、authorization、cookie、完整 SQL、完整 prompt、完整 action intent、完整 metric formula、完整 mapping rules、字段名/字段说明、对象/关系/指标/行动名称、row data、PII、连接串、对象存储裸 URL。
- hash only: request ID list, result summary, schema/action/metric safe summary.
- controlled reference: `object_type:<id>`、`relation_type:<id>`、`action_type:<id>`、`metric:<id>`。
- `data.classification`: current schema evidence events are `internal`.

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| phase2 positive | `fixtures/bkn-trace/phase2/bkn_schema_l2_positive.json` | object/relation/action/metric schema read L2 finding and evidence refs | pass |

## Covered GWT

- GWT-BKN-01 Given legal trace/request/account context, When object/relation/action/metric schema read succeeds, Then bkn-backend emits `claim.created` and `evidence.refs.created` tied to the same `trace_id`、`span_id`、`bkn.request.id`.
- GWT-BKN-02 Given returned schema contains names, comments, property names, mapping rules, action intent or metric formula content, When evidence events are built, Then payload contains only refs/hash/counts/status and no raw schema content.
- GWT-BKN-03 Given `BKN_TRACE_EVIDENCE_INGEST_URL` is not configured, When schema read succeeds, Then business response remains unchanged and no event submission occurs.
- GWT-BKN-04 Given evidence ingest is unavailable, When asynchronous event submission fails, Then API status and response body remain unchanged.

## Known Gaps

- This contract covers schema read evidence from bkn-backend only; object instance data-chain evidence remains in ontology-query/context-loader/Vega paths.
- Schema version and snapshot refs are not available yet, so refs are `unversioned`.
- Cross-module Evidence Graph assembly, snapshot persistence and Studio visualization are owned by BKN Trace core service.

## Owner Sign-off

- owner: OpenBKN Foundry / BKN Engine
- reviewed at: 2026-07-23
- reviewer: pending
- compatibility risk: low; event emission is disabled by default and fail-open when enabled.
