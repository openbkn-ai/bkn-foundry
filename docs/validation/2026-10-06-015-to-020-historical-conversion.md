# 015 to 020 Historical Conversion Validation

Scope: Foundry #2012 / #2030, one-time data and projection conversion only.
All migration-specific changes under `bkn-trace/agent-observability/src/` were
removed from the PR. The remaining Go changes are in the offline upgrade command.
No product query, HTTP API or UI compatibility branch is added.

## Candidate execution against the backed-up local instance

Instance: `kind-bkn-main-e2e`. Frozen SQL source: 3734 rows, consisting of 106
Audit rows and 3628 Evidence outbox rows. The source snapshot was reused.

Latest repeat report:
`~/.bkn/upgrades/015-to-020-historical/run-20261007T000311-8941ce44/report.md`

| Measure | Verified result |
| --- | ---: |
| Source rows | 3734 |
| Native Audit records converted and read back | 60 |
| Evidence source rows converted and read back | 179 |
| Native Evidence aggregate documents | 157 |
| Source rows not converted; originals retained | 3495 |
| Missing native HTTP status | 46 |
| Incompatible multiple-request contexts for one Trace | 3449 |
| Converted Evidence rows missing a Core receipt association | 179 |
| Core projection documents verified against authoritative state | 2989 |
| Already verified Audit records on repeat | 60 |
| Already verified aggregate documents on repeat | 157 |
| New aggregate documents on repeat | 0 |
| Aggregate conflicts | 0 |

`239 converted + 3495 retained = 3734 input rows`. The 179 association losses
are a subset of converted rows and must not be added to the retained count.
The result is `completed_with_loss`; it does not claim all source data or views
were migrated.

The Core helper reused the existing native projection builder, rebuilt the
projection into `bkn-trace-core-history-18dc1580739247b3`, validated document
versions, bodies and total count, then switched the native alias. Subsequent
runs found the existing alias correct and did not rebuild it.

The retained center contains 118 Conversations, 167 Interactions, 842 Operations
and 842 Receipts. Receipt times range from 2026-10-01 to 2026-10-05. These facts
are not the old September outbox rows, whose Conversation/Interaction/Operation
IDs have no matching center facts. No replacement lifecycle, status or receipt
was created for them.

## Ordinary native query verification

An isolated ARM64 test job used the unchanged native query libraries from this
branch, the deployment's database/OpenSearch configuration and a complete
Administrator query scope. The 8081 service image was not replaced by this job.

| Query | Result |
| --- | ---: |
| Log search: converted Audit IDs returned once each | 60 |
| Audit `audit.admin` filter: same converted IDs returned once each | 60 |
| Native aggregates preferred over earlier per-event prototype documents | 157 |
| Native Evidence events returned | 179 |
| Native business graph reads found | 157 |
| Retained receipt-based Trace list total, matching SQL identities | 764 |
| First native Trace page | 20 |

Log queries used 2026-09-01 through 2026-10-01 UTC, respecting the existing
30-day query limit. Retained Trace queries used 2026-09-12 through 2026-10-07 UTC.
The verification helper initially omitted its account ID/type and was corrected
to match the existing authenticated-handler contract; no product code changed.

The 764 Trace entries are retained center facts, not converted September
Evidence-only rows. Native Evidence/business graph availability does not imply
receipt-based Trace list membership. Source identifier-only names remain as
captured; no current directory lookup was used to rewrite historical names.

## Earlier prototype results superseded

The earlier report that read back 106 SS4O log documents and 3628 per-event
Evidence documents proved raw-index publication only. It does not count as
native conversion or product acceptance. That raw-publication execution path
and the migration-specific online query changes have been removed.

## Checks and remaining acceptance

- Python migration tests: 119 passed.
- Full agent-observability Go tests passed.
- `go vet ./...`, `go build ./cmd/...` and `git diff --check` passed.
- Repeated candidate execution produced no duplicate Audit publication, no new
  aggregates and no repeated Core projection rebuild.
- Source snapshots and completed archive files were not deleted or reimported.

The administrator guide is
`deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md`. The production
entry point executes the bundled upgrade command in the existing observability
pod. Local qualification used a temporary command-only job because the currently
running image does not yet contain this revision; its query behavior was not
used as evidence for this branch.

PR re-review, explicit merge approval, merged-image deployment and final 8081
page acceptance remain pending. The enterprise image must also bump its Core
dependency to the merged revision and package the offline upgrade command; its
current Dockerfile does not include that command. Native-library checks are not merged-image UI
acceptance, and #2012 is not closed by this report.
