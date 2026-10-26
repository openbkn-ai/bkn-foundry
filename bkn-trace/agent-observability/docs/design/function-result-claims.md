# Function result evidence

The product captures the existing structured result of a managed platform Function. Users and sample tools do not supply Claims, declare evidence, change their return schema or cooperate on each interaction.

## Exact meaning

A Context Loader–owned `function.result/v1` Claim asserts only: **this identified Function execution returned this structured result snapshot**. Its EvidenceRef points to the captured result, exact Operation/Receipt and actual assembly revision. Trace verifies provenance and evidence consistency. It does not independently certify the algorithm, datasource completeness, stock/kitting/order acceptance, or whether the final answer adopted the result.

Unresolved business assertions remain unsupported/not evaluable. No conclusions are extracted from free-form answers, and caller-authored Claims remain forbidden on managed `bkn_finish_interaction`.

The supply-chain sample is an unchanged test case. Tests independently check its material set/count and then verify the generic Claim/Evidence bindings. No BOM-specific fields, rules, count limits or per-sample contract branches are implemented in product code.

## Existing result and trust

Context Loader uses the existing `status_code`, optional successful `body.exit_code`, `body.result` object/array, and server-owned `function_execution` identity. Empty objects/arrays and genuine zero values are valid snapshots. Missing/null/free-text/scalar results cannot establish this structured-result Claim. They do not make the execution receipt incomplete. Any business field named `trace_evidence` or `claim_type` is ordinary data; it cannot choose the formal Claim type or grant trust.

The platform Function runtime reports actual version and the SHA-256 of code passed to `ExecuteCode`. Factory exposes this identity only for its strictly validated platform Function endpoint and matching version. A metadata version can have its code edited, so identity includes the code hash. Body-supplied identities and remote/OpenAPI responses do not establish this proof.

Capture reuses the existing governed Evidence publisher and capture policy. There is no additional per-tool/version/hash approval list or new rollout switch. The actual listed/executed version must match and the runtime code hash is recorded as evidence; a legitimate code update records its new hash instead of requiring operator reapproval. Remote/old runtimes that cannot establish trusted execution identity produce no result Claim while preserving the existing execution-receipt semantics. Upgrade Core readers before the CL producer; compatible Factory identity is necessary only to obtain result snapshots. Keep compatible Core readers for already persisted records if rolling the producer back. Code hashes do not attest external dependencies, runtime images or datasource snapshots. Ingest authentication uses the existing shared token and producer ID; Core re-verifies CL-provided evidence against CL-provided persisted output. The trust boundary includes Context Loader, rather than proving independence from it. Capture applies to both read-only and side-effecting Functions; a snapshot does not certify the side effect.

## Capture and sealing

Context Loader stores the opaque existing result as an existing `logic_execution` artifact. A governed `logic.execution.observed` event under `function_result_evidence/v1` binds the captured artifact/hash, actual producer and full operation-output hash. Its `complete` field means the result snapshot was captured, not that the business data or final answer was independently proven complete.

New calls retain `managed_function_execution/v1`; optional capture does not replace the receipt contract. Core verifies the accepted source against its exact successful durable receipt, owner, operation/attempt, persisted result and hashes. It assigns a deterministic Claim ID and the actual revision, then uses existing Claim, EvidenceRef, `computed_from` support, SQL Ledger and projection outbox models. Formal event, projection notice and revision commit atomically. Replay does not duplicate Claims. Later revisions retain the already sealed immutable Core Claim event, including its original source revision and artifact bindings, and materialize only newly verified snapshots. Retention reads only the known prior event IDs through existing bounded reads and validates scope/ownership; unavailable or invalid sealed records abort the new revision instead of silently hiding the Claim. Only successfully materialized snapshots enter the expected Claim list. Unsupported, redacted or unverified snapshots and derivation-capacity limits do not add receipt partial reasons or downgrade interaction evidence/record integrity. Without a snapshot, Claim assembly remains `not_applicable` when there are no other expected Claims. Declared events that fail delivery/confirmation, missing receipts and other genuine evidence gaps retain existing pending/partial behavior. Older records explicitly declaring `function_result_evidence/v1` retain their existing integrity checks.

## Minimal persistence and transaction reads

Eligible outputs are already stored inline under the existing 1 MiB limit; they do not already have an output artifact to reuse. `Claim.ContentArtifactRef` is required by the existing model, and operation-output evidence references do not replace the immutable content artifact needed by Claim reads/archive. Keep one result artifact through the existing deterministic writer, deduplicated on retry; do not introduce an alias, new storage contract or changed read/archive model solely to remove it.

Within one SQL transaction, only the current interaction’s metadata is retained, including in batch workers. Call summaries are reused and invalidated when call facts change. Empty interactions skip summary discovery; all Claims reuse the same owner lookup. Causal metadata is read once under the existing conversation/interaction writer locks and updated with this transaction's accepted inserts; each candidate still passes cycle and capacity checks. No cross-request cache, TTL, new worker or relaxed ownership/idempotency/atomicity is introduced.

## Limits and failures

- Existing inline output limit: 1 MiB. Referenced/omitted outputs are not loaded under a lifecycle row lock to manufacture a Claim.
- Existing JSON processing may convert numbers to `float64` before capture. Hashes bind the normalized persisted representation and cannot recover lost original precision. Detectable normalization mismatches omit the optional snapshot. Exact large integers/decimals need string encoding; this is a known output limitation, not a new tool declaration or protocol.
- Existing per-interaction Claim capacity: default 32, including caller-owned Claims. Snapshots are added in deterministic order up to the remaining capacity; excess optional snapshots are omitted without downgrading receipt completeness. No claim is implied for an omitted snapshot.
- Artifact storage/publication use at most one third of the remaining shared Trace POST budget. The rest remains available to the inner/outer Finish calls, with unchanged total budget and retry policy.
- Redaction that changes a result prevents a snapshot Claim without adding receipt/Agent partial reasons. The business result is preserved and secrets are not stored as evidence.
- Missing/changed execution identity, invalid result, pre-publication artifact failure and derivation validation/conflicts omit new optional snapshots. Runtime artifact-write/publication errors produce a diagnostic warning with a fixed stage and interaction/operation IDs, without error text or payload; normal unsupported-result skips remain quiet. Existing publisher-drop warnings are not duplicated. Once an event is declared, normal confirmation still applies. SQL failures still roll back the atomic transaction.
- Known failed attempts owe no result Claim. Stores without the SQL derived-evidence port omit optional snapshots. Declared pending events remain genuine evidence obligations.
- A new snapshot event ID includes its initial sealing revision and Claim ID. Later revisions reference that same event, so they do not multiply existing Claim events; existing causal limits still apply to new events. The artifact writer deduplicates content.
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

The CL/MCP examples use controlled Factory identity and permission adapters, the real artifact validator with in-memory backing, and a disposable SQL Core. They verify the result-snapshot chain, not deployed Factory/sandbox/Kafka/OpenSearch durability or the correctness of a business conclusion. Focused negative cases cover capture-disabled, redacted, scalar/null and unverifiable results without changing the receipt contract, Agent finish schema or business return value. SQL cases cover 33 calls capped at 32 snapshots, complete stored integrity, and genuine declared-event loss remaining partial.

This is an incremental result-snapshot capability referenced by #1332. Final-answer adoption and the conclusion-to-evidence acceptance criteria remain open; this slice does not close the issue.
