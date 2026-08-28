# Context Loader BKN Trace access contract

> Status: BKN Trace 3.0 Managed Lifecycle Implementation Baseline
> Update time: 2026-07-31
> Authoritative basis: `bkn-docs/docs/foundry/bkn-trace/registry/BKN Trace 0.1.3 Access and Event Contract Registry.md`

## 1. Module Responsibility

- Module name: `context-loader`.
- Observation operations: `context.search_schema`, `context.query_object`, `context.query_instance_subgraph`.
- Standard fact: `retrieval.completed`.
- Context Loader is a mandatory protocol boundary for third-party Agents to access OpenBKN business tools: first register managed operations with BKN Trace Core, then execute downstream business calls, and finally terminate Attempt and Receipt.
- The module only states the retrieval facts and does not generate a Claim. The final answer, Claim and adopted support are submitted by the Agent/AI application after receiving the Operation Receipt.

## 2. Managed lifecycle

All REST and MCP business tool calls must belong to an active Conversation and an active Interaction:

```json
{
  "bkn_context": {
    "conversation_id": "conv_...",
    "interaction_id": "int_...",
    "parent_operation_id": "op_...",
    "causation_event_ids": ["evt_..."]
  }
}
```

- Conversation and Interaction must be created through `bkn_start_interaction`, `Mcp-Session-Id` cannot replace business Conversation.
- When the context is missing, invalid, unauthorized, expired or final, the Context Loader returns a stable error code, `required_action` and a security prompt, and the number of downstream business calls must be 0.
- The Context Loader uses a trusted authentication context to determine the tenant, application principal, and effective subject; the caller cannot override the Owner in JSON.
- The Context Loader derives the Operation idempotent identity from the trusted request association, tool name, and normalized input. Network retry reuses `bkn-request-id`, or carries a stable `X-OpenBKN-Client-Invocation-Id`; an existing pending Receipt returns `receipt_pending`, and downstream side effects must not be repeated.

## 3. Downstream sub-call contract

Core assigns stable `operation_id`, `attempt` and `receipt_id` to each managed business tool call. Context Loader writes these trusted identifiers into the current Context and then propagates them to BKN, ontology, Vega, model or Operator sub-calls.

- Network retries of the same logical call reuse `bkn-request-id` or `X-OpenBKN-Client-Invocation-Id`. If the response is lost, the Operation/Receipt is first queried and the downstream call is not re-executed.
- The trusted Context Loader adapter can create the next Attempt and re-invoke the business tool only when the previous Attempt failed with a retryable error. Only the first claimant executes the downstream call; other concurrent calls return `receipt_pending`.
- `created` only indicates whether the Operation was created for the first time, and `execute` indicates whether this ensure call obtained the right to execute the current Attempt; callers must not infer execution from `created`.
- Finalization of the Attempt is done by the Context Loader trusted adapter based on the actual downstream results. The third-party Agent can only query Operation/Receipt and cannot submit `payload_hash`, `outcome` or terminate Receipt directly.
- `conversation_id`, `interaction_id`, `operation_id`, `attempt` and causal ID are propagated with trusted internal calls.
- OpenBKN business causation and observed time must be stripped before calling untrusted third parties.

## 4. Receipt output contract

Different transports preserve the native shape of the business response, so Receipt uses the following stable carriers:

| Scenario | Receipt carrier | Description |
| --- | --- | --- |
| REST is executed normally for the first time | Response headers `bkn-receipt-id`, `bkn-operation-id` | The business response body remains unchanged; the caller uses the ID to query the complete Receipt |
| REST terminal replay or pending | JSON response body field `receipt` | The downstream will no longer be executed and the persistent state will be returned |
| MCP executes normally | `structuredContent.bkn_receipt` | Returned together with tool structured results |
| MCP terminal replay or pending | `receipt` field of text content JSON | The downstream is no longer executed and the persistent state is returned; the error result does not carry `structuredContent` |

`receipt_status` indicates whether the business Attempt has been completed, and `evidence_durability` indicates whether the evidence has received Core durable ACK. The two cannot be mixed:

- Without Evidence Ledger durable ACK, successful calls can only be completed with `receipt_status=completed`, `evidence_durability=pending`.
- `evidence_durability=durable` cannot be submitted until the Evidence event has been written to the Core Evidence Ledger and Projection Outbox and received `durable_ack=true`.
- Failed Attempts use `receipt_status=failed`, `evidence_durability=failed`.
- Currently #541 lifecycle access does not forge durable ACK; #544's 3.0 Evidence Producer is responsible for passing the real ACK and evidence refs to the Attempt completion interface after access.

## 5. Facts and references

`retrieval.completed.payload` is exactly `query_hash/candidate_count/truncated/version_status/source_refs`.

- Schema retrieval retains controlled objects, attributes, relationships, metrics, and action references.
- Object queries only retain the object type and business attribute references in the request, and do not use the returned rows to generate row refs.
- Subgraph queries only form references based on the requested object type and relationship type path, and are not inferred from the returned node/edge content.
- References must contain only `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash (optional)` and must not contain query, condition value, attribute value, name, physical name or row content.

## 6. Event identity and reliability

- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)`.
- `observed_at/emitted_at` reuse the stable time from the caller envelope.
- The emission function explicitly returns the real event ID; the HTTP object query also returns `bkn-evidence-event-id`.
- Existing 2.1 fire-and-forget transmitters do not constitute 3.0 durable ACKs and cannot be used to mark Receipts as durable.
- When there is no valid inbound OTel Span, in order to satisfy the Core correlation field constraints, the Context Loader stably derives the synthesized trace/span ID from `request_id`; this ID is only used for lifecycle correlation and does not mean that the corresponding Span must exist in the OTel backend.
- 3.0 Evidence Producer must use #533 durable Outbox and only mark local events as delivered after Core returns durable ACK.

## 7. Acceptance

- Given a missing or invalid managed context, when any business tool is called, then a stable lifecycle error is returned and the number of downstream calls is 0.
- Given the same managed call correlation and normalized input are replayed, when the Receipt is terminal, then the original Receipt is returned and the number of downstream calls remains 1.
- Given the downstream returns an error or panic, when Context Loader completes the Attempt, then the Receipt becomes failed, no permanent pending state remains, and panic details are not leaked to the caller.
- Given a retryable failed Attempt, when the trusted adapter creates the next Attempt and re-invokes the business tool, then the new Attempt executes only once; concurrent replays return only the pending Receipt.
- Given a third-party Agent obtains a pending Receipt, when lifecycle tools are enumerated, then there is no finalize tool that lets it declare platform output or terminal state by itself.
- Given Core durable ACK has not been received, when the Attempt completes successfully, then the Receipt is completed + pending.
- Given there is no valid OTel Span and the same request is replayed, when the Attempt completes, then the same synthetic trace ID is used; different requests use different IDs.

## 8. Known limitations

3.0 Evidence Producer, real evidence refs, durable Outbox and durable ACK continue to be implemented by #533/#544; before these dependencies are implemented, business execution can be terminated, but the integrity of the evidence must remain assembling/pending, and complete must not be displayed.

- Successful Interaction must provide `assembler_deadline` in the finalization manifest if it contains a Receipt with `evidence_durability=pending`. After expiration, the assembler of #544 converges to complete/partial/failed based on durable evidence results.
- Before #544 lands, there is no continuously running assembler, and Interactions without `assembler_deadline` remain assembling; this is a known intermediate state in the current implementation phase and must not be misdiagnosed as Core being stuck or manually rewritten as complete.
- If the process crashes after the business tool receives execution rights, the trusted adapter will not have time to write the failed final status, and the Receipt will remain pending; the third-party Agent does not have the right to terminate itself. Interaction is currently marked as abandoned by interaction lease recycling, and persistent recovery and evidence convergence are continued by #533/#544.
- REST and MCP both record failed when encountering business code panic and cannot retry by default. Whether to retry must be judged by the trusted adapter based on a clear temporary failure signal, and cannot be automatically released just because a panic occurs.
