# 015 to 020 Historical Conversion Validation

Updated 2026-10-08. This replaces the earlier request-context conversion results.
The former 3504 synthetic conversations/traces were incorrect target mappings,
not original business executions; their backed-up, manifest-listed target IDs
have been removed locally. Do not use the earlier counts as acceptance evidence.

## Current local result

The original source authority is the pre-upgrade 2026-09-26 application SQL
backup, the frozen 106 Audit / 3628 outbox rows, and retained Agent messages.
Conversion changes only offline upgrade scripts/commands. Product runtime
source, API and UI have no migration-specific changes.

The complete write/readback run is `run-20261007T232502-26975e2d` and the complete
repeat is `run-20261007T232742-3202018e`, under the private local directory
`.local-backups/015-history-repair-20261008/tool-state/` in the workspace.

| Measure | Result |
| --- | ---: |
| Original selected source records | 9757 |
| Source records converted / native readback verified | 9757 |
| Whole source rows unconverted | 0 |
| Original native history source records, including 27 Agent threads | 6023 |
| Original Audit / outbox observations | 106 / 3628 |
| Historical Conversations / Interactions | 149 / 206 |
| Historical Operations / Receipts / CallFacts, each | 1404 |
| Original Ledger / explicit message-derived Ledger | 1015 / 22 |
| Original assembly revisions / idempotency records | 174 / 184 |
| Original EE historical projections / explanations | 137 / 49 |
| Additional message-derived native historical projections | 22 |
| Recovered question / answer Artifacts | 68 / 48 |
| Evidence aggregates preserving every outbox observation | 808 |
| Core projection documents, including existing October data | 7363 |
| Repeat: new native rows / new or updated Evidence aggregates | 0 / 0 |
| Repeat: conflicting aggregates | 0 |

These are selected-source counts, not a claim that a 9/26 backup contains every
record through September 30. A successful row conversion also does not recover
absent bodies: 117 original question references and 82 answer references lack a
recoverable body in the available sources. Original IDs/references remain.
The available September SS4O storage contains 2381 technical log documents and
zero technical Span documents; existing technical logs are preserved. Span
references are retained, but a Span tree cannot be recovered from absent bodies.

## Data and relationship acceptance

All 122 original Conversations, 184 Interactions and 1377 Operations / Receipts /
CallFacts per table match their selected source IDs, relationships, ordinals,
state, times and Request / Trace / Span identities. All 174 assembly records
and 184 idempotency records match, using business identity rather than new
surrogate auto-increment IDs. All 359 parent references resolve. 83 interactions
retain multiple Traces. Question text is never used to join or deduplicate.

The 3628 observations contain 3217 unique identity/relationship matches and 411
independent observations. They preserve original requests and Traces in 808
native aggregates; independent observations do not create user conversations.
All 11 events across the seven requested `conv_req_*` samples were read back by
exact event/document identity, with unchanged Request and Trace. The 630 sample
maps to the genuine 21-round `conv_d0a163b794618db8782d6298b9599350`.

The two requested UUID Thread samples are real source threads, not an invalid
ID prefix: `a1295718-216e-4079-ba1b-24df5149d92e` has one human-message round;
`515c9b93-7b65-4fc5-b5dd-0e2b1b14d795` has no human messages and zero rounds.
Their source Agent is an internal knowledge-network optimizer. The native
business list excludes internal optimizer/claim Agents; the log relationship
still opens the native conversation detail.

Before correction, current MariaDB and OpenSearch target content was backed up.
Cleanup used exact prior migration manifest IDs, not broad time predicates.
October native facts passed complete per-row hash comparisons; all 335
protected Artifact documents passed full-content hash comparisons.

## Actual 8081 pages

| Native page sample | Observed result |
| --- | --- |
| September log search | 256 facts: historical Audit plus native conversation facts and an existing management record. |
| September audit workbench | 107 management facts, excluding conversation-start records. |
| Shared management event | Both pages show event `eac43a52-f910-577b-ba86-bfb605ac91f5`, Request `req_9adfa453-7926-4db5-bb65-a38668b479b7`, Administrator, toolbox ID, update outcome and original September 27 time. |
| Failed Vega audit detail | Original `HTTP_400`, status 400, POST, failed result and Request are displayed. |
| Conversation `conv_86d23498696e9e7ec822d568d02a4ddc` | Two rounds, source football question/answer and 1m37s total. Detail has original full text, 28 calls in round one and 8 calls (5 success / 3 failure) in round two. |
| Conversation `conv_d0a163b794618db8782d6298b9599350` | Detail retains 21 rounds; later F15 question/answer, original time, 39.6s and nested calls are visible. First-round body is absent in source. Keyword list counts 20 request-bearing rounds: source round 10 has no receipts. |
| Trace `8efd5409ce8558b35abea3d4b49bc9cf` | Completed, get_kn_detail / context-loader, 853ms; operation `op_cc8da07c0258513d0628edb2df071fbf`, Request `req_0fc342f4-aa5e-4032-bdd9-19aad8ca569e`, source input and timestamp displayed. Zero technical Spans reflects available storage. |
| Independent Trace `5f6293127416dd9fbe65eec56f689b01` | Original request accessible; no original Core operation/terminal receipt or technical Span. Native UI shows unavailable call/Span content, without invented executions. |

## Message conversion qualification and native page limits

The corrected data was applied and read back in the complete run above: 29
native JSON-string message Artifacts, 22 matching start Ledger records and 22
ordinary EE Graph/Markdown projections. This correction created 73 native target
rows/documents; the subsequent complete repeat created zero. All 22 questions
and all seven available final answers match their source text and native hashes.
All 137 original graphs and 49 original explanation records remain unchanged.

The internal Thread `a1295718-216e-4079-ba1b-24df5149d92e` was rechecked in 8081.
Its one round and three calls are visible, and expanding recorded input shows
the original full human question. Its top-level question/result summary remains
unavailable: the source messages have no technical Request/Trace identity and
the current native summary path requires those receipt fields. No identity is
invented to populate that summary. Fifteen message rounds have no source final
answer; no completed result or end time is fabricated.

The historical provenance switch remains false, as requested. The newly stored
22 graphs are qualified data, but the current classic detail does not select
that stored graph mode. Enabling the switch would affect all classic details,
requires existing signing configuration, and does not backfill missing graphs.
September now has 159 ready graphs for 206 interactions; October still has zero
for 167. This is not unrestricted page acceptance. The read-only configuration
assessment and local acceptance report document these native page limits.
No product query, UI or legacy compatibility patch is introduced.

## Verification and workflow

Python: all 233 regression tests passed after the final correction.
Go: the full Foundry agent-observability suite and go vet passed. The EE offline
converter and businessprovenance tests, plus converter go vet, passed. Local
httptest listening was authorized for the full Go test run.
Earlier native write/readback and repeat qualification used actual isolated and
8081 targets, not fixture-only claims.

One local write/readback attempt encountered MariaDB OOMKilled (768 MiB limit).
The local limit was increased to 1536 MiB, rollout became healthy, and the same
frozen input resumed and repeated successfully. This is local deployment
headroom, not a product schema/runtime change.

Engineer guide: `deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md`.
The customer cutoff, source backup and TLS/CA configuration are configurable.
Source tables and completed archives are unchanged. No commit, push, PR update
or merge is authorized until the user requests it after local acceptance.

Final protection readback: all 3734 frozen Audit/outbox source rows remain
field-equivalent; all 335 protected Artifact documents retain their complete
source hashes. October native facts retain their per-row hashes. A preflight
failure run (`run-20261007T232208-40904cec`) is retained; its cause was not
reproduced, and both subsequent complete runs passed without bypassing checks.
