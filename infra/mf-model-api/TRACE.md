# mf-model-api Trace Contract

> 状态：阶段二 L2 模型调用 evidence 接入合同
> 适用版本：`bkn.trace.schema.version=2.0.0`
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 设计.md`

## Module

- module name: `mf-model-api`
- observed service: `mf-model-api`
- owner: OpenBKN Foundry / Model Factory
- runtime: Python FastAPI service
- repository path: `infra/mf-model-api`
- contract version: `2.0.0`

## Entry Operations

| operation | trigger | required context | emitted events |
| --- | --- | --- | --- |
| `model.chat.completions` | `POST /chat/completions` OpenAI-compatible model call | `traceparent`、`bkn-request-id`、account/business-domain headers | `claim.created`、`evidence.refs.created` |

## Inbound Context

- accepted headers: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`x-account-id`、`x-account-type`、`x-business-domain`。
- request id rule: evidence uses `bkn-request-id` first, then `x-request-id`; when missing or invalid it generates `req_<random>` for evidence completeness.
- external trace trust policy: only valid W3C `traceparent` is reused. Invalid context is not propagated into evidence.
- auth context source: account headers and resolved user id only; token, authorization and cookie must never enter evidence payload.

## Phase 2 Evidence Event Rules

- Successful and failed OpenAI-compatible model calls emit one `claim.created` and one `evidence.refs.created` event when `BKN_TRACE_EVIDENCE_INGEST_URL` is configured.
- Event submission is asynchronous and fail-open; BKN Trace ingestion failure does not change the model API response.
- `claim.created.payload.claim_type` is `finding`，通过 `subject_refs.operation=model.chat.completions` 和 `producer_module=mf-model-api` 标识模型调用结论。
- `claim_hash` is computed from safe summary only: model id/name/provider, operation, status, parameter hash, prompt hash, output hash, usage counters and error category.
- `evidence_refs.created.payload.evidence_refs` contains:
  - `source_ref` for model: model id/name/provider reference.
  - `source_ref` for message context: message hash and parameter hash only.
  - `source_ref` for model result: result hash, status, usage counters and error category only.

## Sensitive Data Rules

- never emit: API key、authorization、cookie、完整 prompt/messages、完整 output/answer、tool schema/input/output、PII、token header、provider raw error body。
- hash only: messages、model output、generation parameter shape、tool presence.
- allowed counters: `input_unit_count`、`output_unit_count`; avoid any field name containing `token` because generic token scanners treat it as credential-like.
- controlled reference: `source:model:<id>`、`source:message_hash:<hash>`、`source:model_result:<hash>`。
- `data.classification`: current model evidence events are `internal`.

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| phase2 positive | `fixtures/bkn-trace/phase2/mf_model_call_l2_positive.json` | model call L2 finding and hash-only evidence refs | pass |

## Covered GWT

- GWT-MF-01 Given legal trace/request/account context, When a model call succeeds, Then mf-model-api emits `claim.created` and `evidence.refs.created` tied to the same `trace_id`、`span_id`、`bkn.request.id`.
- GWT-MF-02 Given messages/output/tool schema contain business or personal content, When evidence events are built, Then payload contains only hashes, refs, counters and status.
- GWT-MF-03 Given `BKN_TRACE_EVIDENCE_INGEST_URL` is not configured, When model call succeeds or fails, Then business response remains unchanged and no event submission occurs.
- GWT-MF-04 Given BKN Trace ingestion is unavailable, When async event submission fails, Then model API response remains unchanged.

## Known Gaps

- Current implementation covers OpenAI-compatible `/chat/completions` path first; Claude/Baidu adapter branches should be aligned in a follow-up.
- Prompt/output immutable snapshot storage is not yet connected; current refs are hash-only and `unversioned`.
- Model provider latency bucket and retry attempt count are not yet emitted.

## Owner Sign-off

- owner: OpenBKN Foundry / Model Factory
- reviewed at: 2026-07-25
- reviewer: pending
- compatibility risk: low; event emission is disabled by default and fail-open when enabled.
