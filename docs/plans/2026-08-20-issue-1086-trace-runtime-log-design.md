# Issue 1086: Trace Runtime Log Projection Design

## Intent

Restore the Trace-to-Log investigation path without treating a Conversation as
the owner of a Trace. A conversation can contain many interactions and many
traces; the durable unit of runtime observability is an operation attempt.

## Decision

Add a BKN Trace runtime-log projection whose document identity is the operation
attempt. It contains the stable correlation and authorization metadata required
to search logs by Trace, Request, Conversation, Interaction, Operation, and
Span. The projection is written with the existing Core projection outbox and is
recreated by projection rebuild.

The runtime-log OpenSearch document is a search index, not a second evidence
store. Its payload contains only identity, ownership, timing, status, tool and
protocol metadata, plus a stable reference to the authoritative operation fact,
receipt, and evidence artifacts. The existing Trace detail endpoint remains the
full-evidence view for the selected Trace. No business payload is lost: it
remains available through that existing Trace evidence path, without duplicating
up to 1 MiB inline payloads per operation in the log index.

`conversation.created` remains a business audit event. It is not a runtime
Trace event and is never used to infer a Trace ID.

## Data Model

One document is emitted for each `(operation_id, attempt)` and has a stable
document ID such as `runtime_operation_log:<operation_id>:<attempt>`.

Required fields:

- owner scope: tenant, business domain, application principal, effective
  subject, and knowledge-network IDs;
- correlation: request, trace, span, conversation, interaction, operation and
  receipt IDs;
- execution facts: tool, source module, protocol, attempt status, retryable,
  started, finished and observed timestamps;
- retrieval references: operation-attempt identity, receipt ID and artifact
  references/counts.

The document excludes `OperationCallFact.Input`, `Output`, and `Error` bodies.
Their existing payload envelopes and referenced artifacts remain authoritative
and are loaded only for the selected detail/Trace view.

## Query and Count Contract

The new runtime-log source pushes its supported correlation filters directly to
OpenSearch: trace ID, request ID, conversation ID, interaction ID, operation
ID, span ID, owner scope and time window. Its `Count` therefore describes the
same result set as its records.

Sources that cannot represent a selected correlation filter must return an
exact empty page rather than a broad page to be rejected by `logsvc`. The common
log service must never expose a raw-source total after local filtering; when a
source cannot provide a final total it returns the visible lower bound with
`accuracy=partial`.

## Projection Lifecycle

The write path emits an updated runtime-log projection whenever an operation
attempt is created or reaches a terminal state. Its outbox event uses the
operation row version, so a terminal update replaces the earlier document.

The MariaDB authoritative rebuild scanner joins the operation call fact to its
conversation and receipt to recreate the same document and owner snapshot. It
includes runtime-operation-log documents in its authoritative count. A rebuild
therefore repairs historical records that already have operation facts and
Trace IDs without any manual database migration or backfill.

## API Behaviour

`GET /api/observability/v1/logs?trace_id=...` returns one or more
`operation.executed` runtime records for the Trace when its operation facts
exist. Every result returns correlation identifiers matching the Trace detail.

The list and log-detail endpoints return compact runtime-record metadata and
the correlation IDs needed to navigate to the Trace. The existing Trace detail
endpoint resolves the authoritative operation fact, receipt and evidence
artifacts to expose the complete execution context to callers already
authorized for that Trace.

## Compatibility and Failure Behaviour

Existing Conversation audit logs and their identifiers remain unchanged. The
runtime source has a separate source ID and cursor domain. Missing or malformed
legacy operation facts do not create a fabricated association; they simply do
not yield runtime-log documents after rebuild.

During the alias-based rebuild, the old index continues serving until the new
version validates and switches. Capacity planning must account for both index
versions temporarily, but the new documents avoid duplicating payload bodies.

## Acceptance Criteria

1. A Trace with persisted operation facts is discoverable through log search by
   its Trace ID and returns matching correlation fields.
2. Request-ID search returns the same operation records.
3. Two Trace IDs within one Conversation return only their own records.
4. The runtime source pushes correlation filters into OpenSearch and reports a
   count matching the final result set.
5. A projection rebuild retains historical runtime associations.
6. Trace detail retains access to full input, output, error and artifact
   evidence without copying those bodies into the search index.
