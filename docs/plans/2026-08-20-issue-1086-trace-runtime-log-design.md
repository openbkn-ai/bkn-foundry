# Issue 1086: Receipt-backed Trace Runtime Logs

## Intent

Restore the Trace-to-Log investigation path using the durable Receipt that BKN
Trace already writes. A Conversation may contain multiple interactions and
multiple traces, so a Conversation record must not be used to infer Trace
ownership.

## Decision

Reuse `ReceiptProjectionDocument` in the existing Core projection alias as the
only runtime-log source. Do not introduce another projection document, outbox
event, database table, payload copy, or rebuild path.

A terminal Receipt already contains the identifiers and authorization data
needed for runtime investigation:

- tenant, application principal and effective subject;
- knowledge-network IDs derived from the Receipt business references;
- request, trace, conversation, interaction and operation IDs;
- receipt status, tool name, attempt number and terminal timestamp.

The log adapter turns each completed or failed Receipt into one registered
`runtime.business` event named `operation.executed`. Pending Receipts are not
execution results and are excluded.

## Query Contract

The adapter pushes exact Receipt filters to OpenSearch for Trace, Request,
Conversation, Interaction and Operation IDs. Text fields use their `.keyword`
subfields; `operation_id` uses its keyword mapping. Tenant scope is always
required. When record-level authorization is required, the
adapter also pushes the effective-subject, application-principal and managed
knowledge-network candidates.

Results use a stable keyset order:

1. `terminal_at` descending;
2. `receipt_id.keyword` ascending.

The source consumes and returns the corresponding `search_after` tuple. It
reports an exact count only when OpenSearch reports `hits.total.relation=eq`
and no free-text filtering remains local; otherwise count accuracy is partial.

Filters that cannot match this fixed event shape return an exact empty page.
This includes Span ID: Receipt does not persist Span ID, and Span correlation
is explicitly outside Issue 1086 rather than being fabricated from another
identifier.

## Log Projection

The public log record is metadata derived from the Receipt. It supplies the
operation-audit contract (`module`, action, target and actor snapshots, auth
method and source channel), correlation IDs, outcome/severity, and Receipt
identity. It does not remove or duplicate Receipt evidence. Full input, output,
error and artifact evidence remains available through the existing Trace and
Receipt detail paths.

`GET /api/observability/v1/logs/{log_id}` resolves
`bkn-trace-runtime:<receipt_id>` through a tenant-scoped
Receipt lookup, so every list item has a corresponding detail view.

## Lifecycle and Compatibility

Receipt lifecycle writes, projection outbox delivery and authoritative rebuild
already cover these documents. Historical terminal Receipts become searchable
through the same alias; no data migration or independent backfill is required.

`conversation.created` remains unchanged and continues to describe creation of
a business conversation. It returns an exact empty page for correlation fields
that the Conversation document cannot represent, so it cannot inflate Trace
runtime counts.

## Acceptance Criteria

1. Log search by Trace or Request ID returns only matching terminal Receipts.
2. Conversation, Interaction and Operation correlation filters are exact.
3. Runtime records survive operation-audit source selection and validation.
4. Pagination is stable beyond 200 hits and detail lookup resolves list IDs.
5. Record-scope authorization is applied before the result window is selected.
6. Counts disclose partial accuracy when OpenSearch or local filtering cannot
   provide an exact total.
7. No new projection, write path, evidence copy or Span association is added.
