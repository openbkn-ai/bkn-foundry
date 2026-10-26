# Function result evidence

The product captures the existing structured result of a managed platform Function. Users and sample tools do not supply Claims, declare evidence, change their return schema or cooperate on each interaction.

## Exact meaning

A server-owned `function.result/v1` Claim asserts only: **this identified Function execution returned this structured result snapshot**. Its EvidenceRef points to the captured result, exact Operation/Receipt and actual assembly revision. Trace verifies provenance and evidence consistency. It does not independently certify the algorithm, datasource completeness, stock/kitting/order acceptance, or whether the final answer adopted the result.

Unresolved business assertions remain unsupported/not evaluable. No conclusions are extracted from free-form answers, and caller-authored Claims remain forbidden on managed `bkn_finish_interaction`.

The supply-chain sample is an unchanged test case. Tests independently check its material set/count and then verify the generic Claim/Evidence bindings. No BOM-specific fields, rules, count limits or per-sample contract branches are implemented in product code.

## Existing result and trust

Context Loader uses the existing `status_code`, optional successful `body.exit_code`, `body.result` object/array, and server-owned `function_execution` identity. Empty objects/arrays and genuine zero values are valid snapshots. Missing/null/free-text results cannot establish this structured-result Claim. Any business field named `trace_evidence` or `claim_type` is ordinary data; it cannot choose the formal Claim type or grant trust.

The platform Function runtime reports actual version and the SHA-256 of code passed to `ExecuteCode`. Factory exposes this identity only for its strictly validated platform Function endpoint and matching version. A metadata version can have its code edited, so identity includes the code hash. Body-supplied identities and remote/OpenAPI responses do not establish this proof.

Capture reuses the existing governed Evidence publisher and capture policy. There is no additional per-tool/version/hash approval list or new rollout switch. The actual listed/executed version must match and the runtime code hash is recorded as evidence; a legitimate code update records its new hash instead of requiring operator reapproval. Remote/old runtimes that cannot establish trusted execution identity produce no asserted result Claim and retain an explicit evidence gap. Roll out compatible SQL Core and Factory before the CL producer starts emitting this contract. Keep compatible Core readers for already persisted records if rolling the producer back. Code hashes do not attest external dependencies, runtime images or datasource snapshots.

## Capture and sealing

Context Loader stores the opaque existing result as an existing `logic_execution` artifact. A governed `logic.execution.observed` event under `function_result_evidence/v1` binds the captured artifact/hash, actual producer and full operation-output hash. Its `complete` field means the result snapshot was captured, not that the business data or final answer was independently proven complete.

Core verifies the accepted source against its exact successful durable receipt, owner, operation/attempt, persisted result and hashes. It assigns a deterministic Claim ID and the actual revision, then uses existing Claim, EvidenceRef, `computed_from` support, SQL Ledger and projection outbox models. Formal event, projection notice and revision commit atomically. Replay does not duplicate Claims; late evidence creates the appropriate revision. Integrity inspection checks a unique trusted, receipt-bound capture event; missing/invalid captures remain explicit gaps.

## Minimal persistence and transaction reads

Eligible outputs are already stored inline under the existing 1 MiB limit; they do not already have an output artifact to reuse. `Claim.ContentArtifactRef` is required by the existing model, and operation-output evidence references do not replace the immutable content artifact needed by Claim reads/archive. Keep one result artifact through the existing deterministic writer, deduplicated on retry; do not introduce an alias, new storage contract or changed read/archive model solely to remove it.

Within one SQL transaction, only the current interaction’s metadata is retained, including in batch workers. Call summaries are reused and invalidated when call facts change. Empty interactions skip summary discovery; all Claims reuse the same owner lookup. Causal metadata is read once under the existing conversation/interaction writer locks and updated with this transaction's accepted inserts; each candidate still passes cycle and capacity checks. No cross-request cache, TTL, new worker or relaxed ownership/idempotency/atomicity is introduced.

## Limits and failures

- Existing inline output limit: 1 MiB. Referenced/omitted outputs are not loaded under a lifecycle row lock to manufacture a Claim.
- The existing JSON output normalizer must preserve content exactly. Precision drift leaves a not-evaluable gap; exact large integers/decimals should be strings. This PR does not expand the shared payload normalizer.
- Existing per-interaction Claim capacity: default 32. One result Claim per verified successful attempt; overflow leaves an explicit partial-evidence reason instead of refusing business completion.
- Artifact storage/publication use at most one third of the remaining shared Trace POST budget. The rest remains available to the inner/outer Finish calls, with unchanged total budget and retry policy.
- Redaction that changes a result prevents an exact-original-result Claim and records `function_result_redacted` on the internal Function receipt only; it never enters Agent-carried `partial_reasons` or widens the external finish schema. The business result is preserved and secrets are not stored as evidence.
- Missing/changed execution identity, invalid result, artifact failure and evidence-validation/conflict gaps do not change the business result. SQL failures still roll back the atomic transaction.
- Known failed attempts owe no successful result Claim. Failure after a frozen pending obligation remains not evaluable in that revision. Stores without the SQL derived-evidence port explicitly report partial evidence.
- No Sample changes, new tables, queues, plugin registry, expression mapper or rule engine. Ordinary list/detail query code and click-triggered archive behavior are unchanged.

## Verification scope

Domain-neutral output and unchanged supply-chain results use the same path. Tests cover identity spoofing, absent/null/text/zero/empty/array results, ignored embedded declarations, hash drift, redaction, slow artifacts, clock skew, caller Claim compatibility, SQL replay/rollback/late evidence, persisted integrity verdicts and owner isolation. The 8081 supply-chain data is read-only; new CL/Core acceptance runs in an isolated test environment. No deployment, page-latency, live sandbox or Kafka/OpenSearch durability acceptance is claimed.

The same capture/sealing path is checked with these distinct examples; none adds a domain branch or Sample declaration:

| Example | Input/result boundary | Verification |
| --- | --- | --- |
| Supply-chain intersection | Read-only 8081 dataset: 1,110 rows, 38 common materials | Unmodified Sample Function result, isolated CL/MCP → SQL chain |
| Analytical records | Two rows and `count: 2` | Deterministic read-only Function, isolated CL/MCP → SQL chain |
| Exact monetary amount | Decimal computation serialized as `"30.30"` | Deterministic read-only Function, isolated CL/MCP → SQL chain |
| Empty result | `[]` | Successful empty snapshot still produces one Claim and EvidenceRef |
| Two independent tools | Two distinct tool IDs/results in one interaction | Real SQL regression: two separate Claims/refs; repeated finish creates no duplicates |

The CL/MCP examples use controlled Factory identity and permission adapters, the real artifact validator with in-memory backing, and a disposable SQL Core. They verify the result-snapshot chain, not deployed Factory/sandbox/Kafka/OpenSearch durability or the correctness of a business conclusion. Focused negative cases cover capture-disabled, redacted and unverifiable results without changing the Agent finish schema or the business return value.
