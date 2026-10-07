# 015 to 020 Historical Conversion Validation

Scope: Foundry #2012 / #2030, one-time offline data and projection conversion.
There is no migration-specific change under `agent-observability/src/`, and no
historical query, API or UI compatibility branch.

## Conversion against the backed-up local instance

The frozen source contains 3734 records: 106 Audit rows and 3628 Evidence outbox
rows. It was reused without changing or deleting source records or archives.

The corrected conversion and terminal-projection run is
`~/.bkn/upgrades/015-to-020-historical/run-20261007T020015-da02b7ce/report.md`.
The subsequent unchanged-input repeat is
`~/.bkn/upgrades/015-to-020-historical/run-20261007T043239-30612cb9/report.md`.
Both finish with `complete=true`, `state=completed` and no unconverted rows.
The latest repeat preflights the complete target before writing and verifies
17899 unique Core records in four dependency-complete transport batches, with
zero new Core records and zero created or updated aggregates.

| Measure | Verified result |
| --- | ---: |
| Source records | 3734 |
| Audit records converted and read back | 106 |
| Evidence source records converted and read back | 3628 |
| Unconverted records | 0 |
| Native Evidence aggregates | 3504 |
| Original observed events retained in those aggregates | 3628 |
| Derived native receipt terminal-projection events | 3628 |
| Converted Core Conversations | 3504 |
| Converted Core Interactions | 3511 |
| Converted Core Operations / Receipts / call facts, each | 3628 |
| Native Core records imported and read back | 17899 |
| Core projection documents, including existing records | 17260 |
| Repeat: already verified Audit records | 106 |
| Repeat: already verified Core records | 17899 |
| Repeat: already verified aggregates | 3504 |
| Repeat: created / updated aggregate documents | 0 / 0 |
| Repeat: conflicting aggregates | 0 |
| Source rows with field defaults, truncation or identifier conversion | 3674 |

Source-row counts and derived native records are different measures. Neither
Core rows nor terminal-projection events inflate the 3734-record input count.

## Necessary native target conversion

Missing HTTP status defaults to 200 for stored successes, 500 for failures and
403 for denials; recognized stored HTTP status names map to their numeric code.
The stored result is preserved. Oversized labels are truncated to native limits;
oversized object identifiers map to stable type/hash identifiers. Originals and
field transformations remain in the private snapshot and per-record report.

The old outbox has 808 distinct Trace IDs reused across multiple requests and
contexts. The tool splits those contexts into 3504 deterministic native Trace
identities, updates all related identities together, and creates native Core
records through the ordinary store. Field defaults include completed observation
when no terminal result was stored, unknown authentication and MCP protocol.
Captured failures remain failed; missing full question/answer remains absent.
Captured observation payload remains available in native call details.

Each native Evidence aggregate retains every original observation and includes
its converted receipt's ordinary `retrieval.completed` projection. This is the
same data representation used by the existing Core projection builder. It fixes
explicit Trace-ID queries, which prefer stored Evidence over Core projections,
without adding any query fallback. Previously written aggregates can be updated
only if their complete body matches a known representation derived from the
same frozen source; updates also require OpenSearch sequence/primary-term CAS.

## Unmodified native query qualification

A local acceptance checker uses the unchanged native query libraries and the
instance's actual MariaDB/OpenSearch targets with Administrator scope. It does
not change the running query service or install a historical reader.

| Query | Verified result |
| --- | ---: |
| Ordinary log search: each converted Audit ID returned once | 106 |
| `audit.admin` filter: the same converted Audit IDs returned once | 106 |
| Native aggregate reads, terminal/root summaries and request-chain reachability | 3504 |
| Evidence events returned, original plus terminal projections | 7256 |
| Explicit Trace-ID terminal/root, conversation and business graph samples | 12 |
| Native conversation list total, including existing data | 3614 |
| Native Trace list total, including existing data | 4268 |

Log records have nonempty actor/object snapshots and actions. Aggregate counts
match native normalization. Native list totals match native database identities
for the same September 1 to October 7 query range. A narrower September 12 range
correctly excludes earlier history; its smaller count is not a migration loss.

## 8081 page verification and remaining release work

The existing Trace detail page successfully reads historical Trace
`0019705187e8a7e733143149ea6d6eb4`: `completed`, tool
`bkn.schema.object_type.get`, request
`req_01a0a498-2187-7c0f-9afb-42a1e3a154bd`, and a completed MCP operation with the
captured business-reference payload. The displayed historical time is September
15, 2026. Missing technical Spans and full question/answer are shown as unavailable.

The ordinary log page displays September history when the September 1 to
October 1 range is selected; filtering Execution Factory shows 48 records.
The audit workbench with the same month displays 107 management records
(106 converted source Audit records plus one pre-existing native record),
including the same September 27 toolbox updates and September 12 operations.
Native Audit comparison above verifies exact IDs;
the page's overall count also includes ordinary Core conversation projections
and pre-existing logs, so it is not the 106-row source Audit count.

The business provenance page currently reports that the enterprise implementation
is not deployed. Consequently **final three-page acceptance is not complete**.
The running observability image is a local qualification image, and bkn-agent
is a community image. After this revision is reviewed and approved for merge,
package the offline command into the EE image, align its Core dependency, deploy
formal merged images, repeat the native checks and finish business provenance
page acceptance and formal-image revalidation before closing #2012.

## Development verification

- 134 Python tests pass, including missing status, oversized fields, reused
  Trace/request/operation identities, native terminal projection, exact prior
  aggregate/CAS protection and publication failure followed by fresh readback.
  Added regressions cover preflight-before-write, valid owner-context splits,
  native action-type network scope, and dependency-complete size-bounded batches.
- `go test ./...`, `go vet ./...` and `go build ./cmd/...` pass for
  agent-observability.
- CI-matching `golangci-lint` 2.12.2 reports zero issues.
- `git diff --check` passes; the net runtime-source migration diff is empty.

Engineer instructions: [upgrade guide](../../deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md).
