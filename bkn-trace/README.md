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
