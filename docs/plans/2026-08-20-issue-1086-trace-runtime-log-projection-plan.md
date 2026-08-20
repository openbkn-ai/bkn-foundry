# Issue 1086 Receipt-backed Runtime Log Implementation Plan

**Goal:** Make terminal BKN Trace Receipts discoverable through the existing
log APIs by their persisted correlation identifiers, without adding storage or
duplicating evidence.

**Architecture:** Register a read-only log source over the existing Core
Receipt projection. Keep Receipt lifecycle, outbox and rebuild behavior
unchanged.

## 1. Lock the Receipt query contract

- Add adapter tests for exact Trace, Request, Conversation, Interaction and
  Operation filters.
- Require completed/failed Receipt status and use `terminal_at` as event time.
- Test tenant/business-domain isolation and record-scope authorization
  candidates.
- Return exact empty results for incompatible fixed filters, including Span.

Verification:

```bash
go test ./src/drivenadapter/httpaccess/opensearchruntimeaudit -count=1
```

## 2. Implement stable pagination and truthful counts

- Honor the source limit with the existing 1–200 normalization.
- Sort by `terminal_at desc, receipt_id.keyword asc`.
- Pass `PageBefore.SearchAfter` to OpenSearch and project hit sort values back
  into `CursorPosition`.
- Honor `hits.total.relation`; mark totals partial when free-text filtering is
  completed by `logsvc`.

## 3. Satisfy operation-audit and detail contracts

- Admit the dedicated Receipt runtime source in operation-audit mode.
- Project required module, action, target, actor, auth and channel fields from
  the Receipt and its owner snapshot.
- Implement scoped `Get` for `bkn-trace-runtime:<receipt_id>`.
- Keep `operation.executed` restricted to terminal Receipts.

Verification:

```bash
go test ./src/domain/service/logsvc ./src/drivenadapter/httpaccess/opensearchruntimeaudit -count=1
```

## 4. Keep the existing index contract explicit

- Retain keyword-capable mappings for exact correlation fields and Receipt
  status.
- Retain date mapping for `terminal_at`.
- Do not add a discriminator, projection type, outbox mutation, rebuild scanner
  or database migration.

Verification:

```bash
go test ./src/drivenadapter/httpaccess/opensearchprojection -count=1
```

## 5. Verify and publish

- Run focused regression tests, then `go test ./...` and `go vet ./...`.
- Run `git diff --check` and inspect the final staged scope.
- Push the existing feature branch, update PR #1088 to describe Receipt reuse,
  reply to the pagination review thread, and trigger a new review with
  `/review`.
