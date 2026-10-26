# Function result evidence contract

`function_result_evidence/v1` carries one explicit, business-owned conclusion from an approved managed Function. Trace stores that producer's assertion and verifies its provenance and evidence bindings. It does not independently certify the business algorithm, interpret domain fields, or infer that the final answer adopted the conclusion.

## Responsibilities and minimal declaration

The business Function computes and validates its result. It may include this optional field in `body.result`:

```json
{
  "trace_evidence": {
    "claim_type": "quality.check/v1",
    "complete": true,
    "content": {"measurement": "passed", "count": 2}
  }
}
```

`claim_type` is an opaque, nonblank type identifier of at most 128 bytes. `complete` must be the JSON boolean `true`; `content` must be non-null JSON. One declaration produces at most one Claim per operation attempt. The producer owns the meaning, scope and completeness of the conclusion. A tool without a supported conclusion omits the declaration. Context Loader does not map result fields or run business validation rules.

The supply-chain sample owns its BOM intersection calculation, product/depth/substitute rules, bounded material set, count and declaration. Those rules are maintained in `bkn-samples`, not Context Loader or Trace Core. The BOM sample is an integration fixture, not a runtime contract branch.

## Trust and approval

The platform Function runtime reports its actual version and the SHA-256 of code passed to `ExecuteCode`. Factory exposes `function_execution` only for its strictly validated platform Function endpoint and matching version. A version can have its code edited; approval therefore pins the code hash too. Body-supplied identities and remote/OpenAPI results do not establish this proof.

Approval is opt-in through Helm `observability.lifecycle.result_producers`, or `BKN_TRACE_RESULT_PRODUCERS`:

```json
[{
  "knowledge_network_id": "<network>",
  "toolbox_id": "<toolbox>",
  "tool_id": "<tool>",
  "function_version": "<actual version>",
  "code_sha256": "sha256:<64 lowercase hexadecimal characters>"
}]
```

Review the producer's business semantics before approval. Configuration is operator-owned, never taken from tool arguments. An output declaration alone grants no trust. The empty default leaves ordinary Function execution unchanged. Upgrade Factory, Context Loader and SQL Core before enabling this feature. Code hashes do not attest external dependencies, runtime images or datasource snapshots.

## Capture and sealing

Context Loader verifies the listed/executed/approved identity, successful execution, and declaration shape. It stores the opaque content as an existing `logic_execution` artifact and publishes a governed `logic.execution.observed` event binding the claim type, artifact/hash, producer and full operation-output hash. Managed `bkn_finish_interaction` still rejects caller-authored Claims.

Core verifies the accepted source against its exact successful durable receipt, owner, operation/attempt, persisted output and declaration. It assigns the actual assembly revision and deterministic Claim ID, then uses the existing Claim, EvidenceRef, `computed_from` support, SQL Ledger and projection outbox. Formal event, projection notice and revision commit atomically. Replay does not duplicate Claims; late evidence creates the appropriate revision. Integrity inspection accepts a unique trusted, receipt-bound completeness declaration; missing/invalid declarations remain explicit gaps instead of inspection failures.

## Limits and failure behavior

- Existing inline output limit: 1 MiB. Referenced/omitted outputs are not loaded under the lifecycle row lock to manufacture a Claim.
- Numeric content must survive the existing operation-output JSON normalization exactly. Values that drift leave a not-evaluable evidence gap; producers should represent precision-sensitive large integers or decimals as strings. This PR does not change the shared payload normalizer.
- Existing per-interaction Claim capacity: default 32. Overflow leaves an explicit partial-evidence reason rather than rejecting business completion.
- Artifact storage and publication together use at most one third of the remaining shared Trace POST budget; the rest remains available to the inner/outer Finish calls. Total budget and retries are unchanged.
- Missing/changed identity, absent/incomplete/malformed declaration, artifact failure and evidence-validation/conflict gaps do not change the business result. SQL failures still roll back the atomic transaction.
- A known failed attempt owes no successful conclusion. If a pending obligation was frozen before failure arrived, that revision conservatively retains its not-evaluable gap.
- Sealing requires the SQL derived-evidence transaction port. Stores without it explicitly report partial evidence.
- No new tables, queues, plugin registry, expression mapper or per-domain rule engine. Ordinary list/detail query code and click-triggered archive behavior are unchanged.

## Verification scope

Domain-neutral producer tests and the current sample native Function fixture use the same path. Regressions cover identity spoofing, missing/invalid declarations, precise numeric content, hash/type drift, zero values, slow artifacts, clock skew, caller Claim compatibility, real isolated SQL replay/rollback/late evidence, integrity-worker verdicts and owner isolation. The sample fixture executes generated native code against read-only CSV-backed BKN query adapters; it is not live sandbox, Kafka/OpenSearch, deployment or page-latency acceptance.
