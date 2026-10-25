# BKN Trace

[中文](README.zh.md)

[![License](https://img.shields.io/badge/license-OpenBKN-blue.svg)](../LICENSE-OPENBKN.txt)

BKN Trace is the OpenBKN Community Trace Core. It records first-hand facts
about managed Agent, SDK, and MCP execution, then provides protected APIs for
technical trace analysis and precise correlation with platform logs.

## What it records

The authoritative unit of execution is an Operation attempt. A completed or
failed attempt can retain its Conversation, Interaction, Operation and request
identifiers, execution timing and status, caller identity, protocol and tool
information, and the input, output, or diagnostic error received at the call
boundary. Recording must never alter the business tool's original result.

BKN Trace records facts rather than inferring them from a final answer. Missing
information remains explicitly missing; it is not reconstructed by a reader or
UI.

## Public Community capabilities

- Accept and persist managed execution facts and associated evidence events.
- Provide protected Trace, Conversation, Interaction, Operation, evidence, and
  technical-log read APIs.
- Return technical execution detail: recorded input/output or errors, Trace and
  Span relationships, and exact correlation identifiers for log drill-down.
- Enforce access profiles and record scope before returning trace or evidence
  data.
- Retain the Community Trace fact model and public API contracts as stable
  integration boundaries for OpenBKN clients.

## Architecture

```text
Managed MCP / SDK calls
        |
        v
Trace producers capture first-hand execution facts
        |
        v
BKN Trace Core
  - lifecycle and operation facts
  - evidence and technical correlations
  - access-controlled read APIs
        |
        +--> technical Trace analysis
        +--> precise log drill-down
```

The core is deliberately a technical fact service. It does not derive business
meaning from a final response or add a second authoritative trace store.

## Components

| Path | Responsibility |
| --- | --- |
| `agent-observability/` | Go Trace Core service, OpenAPI surface, persistence adapters, and deployment chart. |
| `otelcol-contribute-chart/` | OpenTelemetry Collector Contrib chart for OTLP collection and OpenSearch export. |
| `scripts/` | Repeatable contract and deployment-safety checks. |

See [agent-observability/README.md](agent-observability/README.md) for local
development and service configuration, and
[otelcol-contribute-chart/README.md](otelcol-contribute-chart/README.md) for
Collector deployment and validation.

## Trace root tool filtering

For the exact owner, application, and administrator scopes supported by identity
pagination, the Trace list's `tool` filter matches the canonical root tool, not
any descendant call. The root is the first authorized receipt with a usable
Trace/request identity and a nonblank tool name, ordered by stored issue time
and receipt ID. Surrounding Unicode whitespace is removed; matching remains
case-sensitive and does not normalize the name's internal text.

The same root metadata is used with and without a tool filter, including when
the earliest receipt is outside the selected time window or the latest bounded
projection. The time window still selects eligible Trace identities; it does
not redefine their root. Reads fetch only selected identities' metadata, with a
nonunique receipt lookup index registered in migration 037. No historical facts
are rewritten. Tool-only and Service+Tool identity pagination retain this fast
path; the additional scanning cases below do not replace it.

Migration 037 is additive, but the schema ledger rejects images whose migration
manifest is older than the database. After applying it, rollback must use a
compatible image that retains the identical 037 manifest, or a separately
reviewed database rollback plan. Simply restarting the original pre-037 image
is not a supported rollback. Index creation time, locking and ingestion cost on
large production tables require their own rollout review.

## Trace status, error, and shared-network filtering

The built-in Core projection implements an optional scanner for Trace list
`status` and `error_keyword` filters, including their `service`/`tool`
combinations, and shared business-network lists without other unsupported
content filters. Legacy projection adapters keep their compatibility behavior.
The scanner uses one receipt-index point-in-time (PIT) per request, reads 256
documents per search page, and processes 64 Trace identities per batch. It
authorizes complete Interactions before exposing sibling Traces, derives the
root from the complete authorized receipt lifetime, and derives status from
the events inside the requested time window. A failed receipt outside the
latest 20 cannot disappear from an otherwise eligible full-window error Trace.

`error_keyword` matches the final displayed error summary, with the existing
case-insensitive substring semantics. Normal error text retains its first 512
Unicode code points; wrapped execution stderr retains its final 512, with an
ellipsis when shortened. Text outside that summary, a later failure, or unrelated
input/output does not match. Narrow SQL reads select the first failure and
source module only within already authorized Trace/conversation/Interaction
tuples, before reading error payloads. Foreign-owner collisions cannot supply
the displayed error or root service. This scanner adds no migration or
materialized read model. Ordinary list fast paths and detail reads are unchanged.

The scanner rejects requests exceeding 65,536 distinct candidate Trace IDs,
10,000 receipts in an expansion/batch, a 32 MiB receipt-PIT response, or its
30-second query deadline. Deep page retention is limited to 10,000 rows,
including the extra cursor-boundary row. A deterministic size/count or page-retention refusal returns HTTP 422
`SUMMARY_QUERY_BUDGET_EXCEEDED`; a query deadline returns HTTP 503
`SUMMARY_QUERY_TIMEOUT`, rather than a successful partial total. Narrow
the time window or filters to reduce a scan; use the returned cursor instead of
a deep page offset to avoid the retention limit. Cursor pagination still scans
for the exact total and does not bypass the other budgets.

For non-global callers, the new scanner reports `span_count_status=unavailable`:
the existing span-statistics port has no authorization scope and could include
another owner's spans with the same Trace ID. Global administrators may retain
that statistic. The pre-existing ordinary-list/detail statistics paths are not
changed. Unsupported `agent_or_app`, general `keyword`, `knowledge_network`,
record-integrity, evidence-completeness, and agent-exclusion combinations still
use the compatibility path and may be partial. Exact Trace/conversation/
Interaction selectors are also outside this scanner's list contract.

Scanner Tool matching always uses roots from the complete authorized receipt PIT,
including after archive purge removes SQL receipts. Missing required PIT roots or
authorization scopes still fail closed. Successful scans retain their complete
result if PIT cleanup fails: an operational warning is emitted, and the PIT
expires after its one-minute keep-alive. Scan errors remain errors.

The PIT freezes receipt searches within one request only. It is not a MariaDB
or artifact snapshot, and successive page requests create separate PITs; there
is no transactionally consistent snapshot across all stores or page requests.
With OpenSearch Security enabled, grant the receipt index/alias permissions
`indices:data/read/point_in_time/create`,
`indices:data/read/point_in_time/delete`, and `indices:data/read/search`.
Production does not require list-all-PIT permission. The isolated acceptance
test uses `indices:data/read/point_in_time/readall` only to check its own PIT
cleanup through the read-only inventory. See the
[OpenSearch 2.19 PIT security model](https://docs.opensearch.org/2.19/search-plugins/searching-data/point-in-time/#security-model).

## Automatic log archive batches

One authorized `POST /api/observability/v1/log-archive-jobs` automatically processes
successive batches of up to 10,000 records using the cutoff fixed at request
start. The existing frontend can keep its single create request and refresh the
job history afterward. No frontend request loop or new scheduler is required.

Each batch has its own job, frozen candidate IDs, verified archive object and
manifest. The order is: select a complete batch, write and verify its object,
persist the manifest and cleanup candidates, delete only those IDs, refresh and
confirm their absence, then mark that batch completed and select the next one.
Partial searches or unreliable totals still return HTTP 409
`archive_incomplete_candidates`; exceeding 10,000 eligible records alone does
not. Same-kind Create and cleanup retry share a database lock; a concurrent
operation returns `409 archive_busy`. Any unresolved cleanup blocks a new run.

The create response is the final batch job; `candidate_count`, its manifest and
download refer to that batch. Jobs in the same run share `batch_progress.run_id`.
The nullable `batch_progress` object is persisted and returned by job queries:
`batch_number`, `eligible_at_selection`, `verified_count`, `cleaned_count`,
`cleaned_in_run`, `remaining_at_selection`, and `run_complete`. Cleaned counts
include only batches whose frozen IDs were confirmed absent; a partially failed
cleanup remains unconfirmed until retry. Remaining means records outside that
batch at its selection time, not a live global count or an immutable snapshot
across batches. Each batch job's range is the common eligibility window, not a
claim that the whole window is contained in that individual object.

A verification failure leaves that batch's hot data intact. A cleanup failure
stops the run in `cleanup_incomplete`; retry uses the same frozen IDs without
rewriting the verified object, then continues the remaining batches under the
same run ID and cutoff. An interrupted synchronous request retains completed
batches and any persisted cleanup state. A later create request can process
remaining records as a new run after pending cleanup is resolved. A failed
later batch never rolls back earlier completed batches. Existing job-history
limits remain unchanged; no real-time progress UI is added here.

Migration 038 adds a nullable `batch_progress` column, preserving old records.
Apply it before using the new image. The migration ledger rejects pre-038
manifests afterward: rollback requires a compatible image retaining the same
038 manifest or a separately reviewed database rollback. Database locking uses
one connection per active kind; a finite pool smaller than three is rejected
rather than risking exhaustion (the Core default is 16).

Batching limits candidate count, not bytes. Full payloads and the existing object
writer can still consume substantial memory; this is not full streaming or a
solution for a single oversized record. Trace archive freezing is unchanged,
and the broader #1724 streaming/OOM work remains open. Ordinary list/detail read
paths are unchanged.

## Historical attribution capture size

The internal historical-capture encoder keeps the existing v1 JSON bytes and
hash whenever that representation fits the caller's package budget. If base64
expansion alone makes v1 exceed the budget, it may use the explicit
`internal-capture-v2-gzip-bodies` JSON representation: each nonempty raw body is
stored as a single gzip member. Source JSON bytes, identity, recorded gaps, and
nil/empty bodies are preserved rather than re-encoded as JSON values.

The production budget remains 64 MiB. Both the encoded package and the manifest
metadata plus cumulative decompressed bodies must fit the supplied budget.
Malformed gzip, extra members/trailing bytes, invalid hashes, ambiguous layouts,
and unsupported versions are rejected. Package size refusals expose
`ErrCapturePackageBudget`, distinct from missing records and malformed content.
Historical capture encoding checks cancellation before/during compression.

The decoder continues to read old v1 packages. Revision-input writes and reads
remain strictly v1; this fallback does not change their persisted contract.
Small captures keep their prior hashes, avoiding unnecessary regeneration.
Consumers of v2 must use an updated decoder; a rollback reader predating v2
cannot reopen new compressed packages. This bounds package/decompressed sizes,
not encoder peak memory or the preceding snapshot read. It does not expand the
EE final-view budget or authorize missing historical evidence. (#2148)

## Verification

Run the BKN Trace license-header check from the Foundry repository root:

```bash
python3 bkn-trace/agent-observability/scripts/check_license_headers.py
```

Run service tests from the service directory:

```bash
GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...
```

## License

BKN Trace is licensed under the [OpenBKN License](../LICENSE-OPENBKN.txt).
Each OpenBKN-authored, commentable source and deployment file carries a
copyright notice and `SPDX-License-Identifier: LicenseRef-OpenBKN` marker.
