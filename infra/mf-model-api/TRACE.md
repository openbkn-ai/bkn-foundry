# mf-model-api BKN Trace Integration Contract

> Status: BKN Trace 2.1 producer implementation baseline
> Updated: 2026-07-25
> Authoritative source: `bkn-docs/docs/foundry/bkn-trace/registry/%E6%A0%B8%E5%BF%83%E4%B8%9A%E5%8A%A1%E4%BA%8B%E4%BB%B6%E6%B3%A8%E5%86%8C%E8%A1%A8.md`

## 1. Module Responsibility

- Module name and observability service: `mf-model-api`.
- OpenAI, Claude, Baidu, Baidu Tianchen, and other actual model branches uniformly record `model.call.observed`,
  covering both non-streaming and streaming responses.
- This module records only model-call facts. It does not create conclusions, evidence references, or business
  references on behalf of Agents or AI applications.
- After successfully building the model-call fact, the service returns a stable event ID through the
  `bkn-evidence-event-id` response header. It also returns the request's safely validated
  `bkn-candidate-source-event-ids` that actually entered the model context as `bkn-adopted-source-event-ids`, so
  upper layers can bind sources when creating conclusions.

## 2. Context and Replay

Upstream callers must pass a valid trace/request together with `bkn-interaction-id`, `bkn-operation-id`,
`bkn-causation-event-id`, and `bkn-event-observed-at`. `bkn-attempt` defaults to `1`.

- `bkn-event-observed-at` is the UTC RFC3339Nano time created by the first business call. Retries and cross-process
  replay must reuse it.
- If any business-causality field or reliable observed time is missing, do not create a business fact; keep only
  technical logs/traces.
- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)` uses the full 64-character hexadecimal hash.
- Do not rebuild an event by reusing only `operation` with the current time. The producer does not persist envelopes,
  so that restart scenario is explicitly unsupported.

## 3. Events and Sensitive Boundaries

The payload allows exactly:

```text
model_name
model_provider
status
input_token_count
output_token_count
prompt_hash
output_hash
error_category (on error)
error_hash (on error)
```

Full prompts/messages, model output, tool schemas/parameters/results, provider errors, PII, API keys,
Authorization, and Cookies must not enter events, normal logs, or spans. Successes and failures record only hashes,
counts, and safe enums.

## 4. Reliability

- Non-2xx ingest HTTP responses are treated as failures and retried up to three times.
- Asynchronous tasks keep strong references. Final failures are recorded as warnings, and model business responses
  remain fail-open.
- There is currently no persistent outbox, so unsubmitted events may be lost when the process exits. Fully reliable
  replay is the caller's responsibility: the caller must save and reuse the envelope, then retry actively.

## 5. Acceptance

- Fixture: `fixtures/bkn-trace/phase2/mf_model_call_l2_positive.json`.
- Given any actual model provider, when a non-streaming or streaming call finishes, then the same producer contract
  emits a success or failure fact.
- Given replay with the same complete envelope, then the event ID and observed time are unchanged.
- Given a missing `bkn-event-observed-at`, when the model call finishes, then no conflicting new event is produced.
- Given non-2xx ingest, when reporting, then the service retries three times and records the final failure.

## 6. Known Limitations

The model module does not store prompt/output snapshots and has no persistent outbox. Conclusion binding, business
semantic parsing, and snapshots are owned by upper-layer Agents/AI applications and BKN Trace core.
