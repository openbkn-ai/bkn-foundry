# BOM structure-intersection evidence contract

## Scope and decision

`bom.structure_intersection/v1` covers only the common material-code set and its cardinality for the products, depth and substitute policy in a managed BOM native Function call. It excludes root rows, de-duplicates material codes, and describes the authorized BOM data read during that execution. It does not establish stock availability, kitting, contention, order acceptance, or a transactionally consistent source-data snapshot.

The existing Claim/EvidenceRef models and Ledger remain authoritative. Managed MCP finish accepts no caller-authored Claims. No free-text extraction, generic expression mapper, new table or queue is introduced.

## Producer approval and execution identity

The tool listing and execution normally resolve the same Function metadata version through `tool.SourceID`. However, Function edits can replace code under that version. A version address therefore does not identify immutable code.

The platform Function runtime returns its actual version and the SHA-256 of the code passed to `ExecuteCode`. Execution Factory exposes `function_execution` only for its own strictly validated Function endpoint and matching version. Remote Functions, OpenAPI tools, arbitrary body fields and older runtimes do not establish this proof. The hash covers code text, including this sample's embedded calculation modules; it does not attest external dependencies, runtime images or datasource contents.

Producer approval is opt-in, configured on Context Loader, never supplied in a tool request. Set Helm `observability.lifecycle.bom_shared_producers`, or the equivalent `BKN_TRACE_BOM_SHARED_PRODUCERS` JSON array:

```json
[{
  "knowledge_network_id": "supply_ontology_hand",
  "toolbox_id": "<approved toolbox ID>",
  "tool_id": "<approved published tool ID>",
  "function_version": "<approved Function UUID>",
  "code_sha256": "sha256:<64 lowercase hexadecimal characters>"
}]
```

Approve the self-contained `bom_shared_list` implementation and verify its business meaning before registering it. The listing version, actual execution version and approved code hash must all match. This is an explicit approved-producer list, not a claim that any Function returning matching JSON computes the right business result. Upgrade Factory, Context Loader and SQL Core before enabling it; the default empty list preserves existing behavior.

## Persistence and assembly

1. Context Loader checks the executed identity, complete typed result, input/output scope, sorted unique codes and count. A genuine zero intersection is valid. The full Function output remains in the existing operation-call fact.
2. The bounded summary is saved through the existing artifact endpoint as `logic_execution`. A `logic.execution.observed` event carries `complete: true`, the contract, producer identity, summary hash and exact output hash, using the existing governed publisher and receipt confirmation.
The completeness declaration is emitted only after typed-result checks and artifact persistence succeed. Record-integrity inspection accepts one receipt-bound declaration from the registered Context Loader producer; historical missing or invalid declarations remain explicit gaps rather than inspection failures.
3. Core registers a deterministic expected Claim per approved-contract attempt. Once a revision can freeze, it verifies the accepted Ledger source against that exact successful, durable receipt and persisted output.
4. Core uses the actual revision ID to create one Claim and one artifact EvidenceRef, with `computed_from` support. The source event, operation, attempt and exact receipt ID remain auditable. The new Ledger event, projection notice and revision commit in the same SQL transaction. The final artifact manifest includes the added event and artifact.
5. GET reads persisted models. Finish/event replay does not duplicate the Claim. A later evidence revision has its own event and source-revision binding, preserving earlier frozen revisions. Existing evidence snapshots include the formal Ledger event and result artifact reference for archive capture.

## Limits and failure behavior

- Approval is disabled by default. Unregistered tools retain execution provenance without automatic Claims.
- Missing/changed proof, malformed, truncated or unavailable results produce no asserted Claim. Registered attempts retain an explicit evidence gap; business completion is independent. An attempt already known to have failed does not owe a successful set/count Claim; its failure receipt remains available. If failure arrives after a pending Claim obligation was frozen, that revision conservatively retains an explicit not-evaluable gap.
- The existing per-interaction Claim capacity (default 32) bounds materialization. Additional approved results leave an explicit capacity gap instead of refusing business completion.
- The first slice requires the existing inline operation-output limit (1 MiB) and the sample loader's 5,000-row bound. Large referenced/omitted outputs are not fetched under a lifecycle row lock to manufacture a Claim; they remain not evaluable in this contract.
- Artifact submission and evidence publishing together use at most one third of the remaining Trace POST budget, preserving the rest for the inner and outer operation finish calls without increasing the total budget or adding retries. Storage, publishing and evidence limits remain in force. SQL persistence failures roll back the transaction; evidence-budget, validation or immutable-record conflicts leave explicit evidence gaps instead of changing the calculated business result.
- Atomic Claim sealing currently requires the SQL store's derived-evidence transaction port. An in-memory/test store without it explicitly reports partial evidence; it must not independently commit a Ledger event and pretend it shared the revision transaction.
- No changes to ordinary list/detail queries, click-triggered archive logic, business databases or running deployments are required by this change.

## Focused verification

The regression set covers actual-code identity versus forged/old/remote responses; normal and zero intersections; invalid version/hash/result/artifact failures; cross-module native-result hashing; real isolated MariaDB commit/rollback, late evidence, exact replay, reconstructed reads, snapshot retention and owner isolation. The captured native-result fixture retains its original input/output; its source-event completeness marker is updated to the current producer contract. No new browser timing, load or capacity certification is claimed.
