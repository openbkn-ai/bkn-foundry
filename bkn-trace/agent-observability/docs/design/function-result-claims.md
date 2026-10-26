# Function result evidence

The product captures the existing structured result of an approved managed Function. Users and sample tools do not supply Claims, declare evidence, change their return schema or cooperate on each interaction.

## Exact meaning

A server-owned `function.result/v1` Claim asserts only: **this identified Function execution returned this structured result snapshot**. Its EvidenceRef points to the captured result, exact Operation/Receipt and actual assembly revision. Trace verifies provenance and evidence consistency. It does not independently certify the algorithm, datasource completeness, stock/kitting/order acceptance, or whether the final answer adopted the result.

Unresolved business assertions remain unsupported/not evaluable. No conclusions are extracted from free-form answers, and caller-authored Claims remain forbidden on managed `bkn_finish_interaction`.

The supply-chain sample is an unchanged test case. Tests independently check its material set/count and then verify the generic Claim/Evidence bindings. No BOM-specific fields, rules, count limits or per-sample contract branches are implemented in product code.

## Existing result and trust

Context Loader uses the existing `status_code`, optional successful `body.exit_code`, `body.result` object/array, and server-owned `function_execution` identity. Empty objects/arrays and genuine zero values are valid snapshots. Missing/null/free-text results cannot establish this structured-result Claim. Any business field named `trace_evidence` or `claim_type` is ordinary data; it cannot choose the formal Claim type or grant trust.

The platform Function runtime reports actual version and the SHA-256 of code passed to `ExecuteCode`. Factory exposes this identity only for its strictly validated platform Function endpoint and matching version. A metadata version can have its code edited, so identity includes the code hash. Body-supplied identities and remote/OpenAPI responses do not establish this proof.

Capture remains an operator opt-in through Helm `observability.lifecycle.result_producers`, or `BKN_TRACE_RESULT_PRODUCERS`:

```json
[{
  "knowledge_network_id": "<network>",
  "toolbox_id": "<toolbox>",
  "tool_id": "<tool>",
  "function_version": "<actual version>",
  "code_sha256": "sha256:<64 lowercase hexadecimal characters>"
}]
```

This is deployment-level producer approval, not a per-user/per-interaction declaration. The empty default preserves ordinary behavior. Upgrade Factory, Context Loader and SQL Core before enabling capture. Code hashes do not attest external dependencies, runtime images or datasource snapshots.

## Capture and sealing

Context Loader stores the opaque existing result as an existing `logic_execution` artifact. A governed `logic.execution.observed` event under `function_result_evidence/v1` binds the captured artifact/hash, actual producer and full operation-output hash. Its `complete` field means the result snapshot was captured, not that the business data or final answer was independently proven complete.

Core verifies the accepted source against its exact successful durable receipt, owner, operation/attempt, persisted result and hashes. It assigns a deterministic Claim ID and the actual revision, then uses existing Claim, EvidenceRef, `computed_from` support, SQL Ledger and projection outbox models. Formal event, projection notice and revision commit atomically. Replay does not duplicate Claims; late evidence creates the appropriate revision. Integrity inspection checks a unique trusted, receipt-bound capture event; missing/invalid captures remain explicit gaps.

## Limits and failures

- Existing inline output limit: 1 MiB. Referenced/omitted outputs are not loaded under a lifecycle row lock to manufacture a Claim.
- The existing JSON output normalizer must preserve content exactly. Precision drift leaves a not-evaluable gap; exact large integers/decimals should be strings. This PR does not expand the shared payload normalizer.
- Existing per-interaction Claim capacity: default 32. One result Claim per approved successful attempt; overflow leaves an explicit partial-evidence reason instead of refusing business completion.
- Artifact storage/publication use at most one third of the remaining shared Trace POST budget. The rest remains available to the inner/outer Finish calls, with unchanged total budget and retry policy.
- Redaction that changes a result prevents an exact-original-result Claim and records `function_result_redacted`; the business result is preserved and secrets are not stored as evidence.
- Missing/changed execution identity, invalid result, artifact failure and evidence-validation/conflict gaps do not change the business result. SQL failures still roll back the atomic transaction.
- Known failed attempts owe no successful result Claim. Failure after a frozen pending obligation remains not evaluable in that revision. Stores without the SQL derived-evidence port explicitly report partial evidence.
- No Sample changes, new tables, queues, plugin registry, expression mapper or rule engine. Ordinary list/detail query code and click-triggered archive behavior are unchanged.

## Verification scope

Domain-neutral output and unchanged supply-chain results use the same path. Tests cover identity spoofing, absent/null/text/zero/empty/array results, ignored embedded declarations, hash drift, redaction, slow artifacts, clock skew, caller Claim compatibility, SQL replay/rollback/late evidence, persisted integrity verdicts and owner isolation. The 8081 supply-chain data is read-only; new CL/Core acceptance runs in an isolated test environment. No deployment, page-latency, live sandbox or Kafka/OpenSearch durability acceptance is claimed.
