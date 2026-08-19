# Trace List Pagination Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Page Trace and Conversation lists from projection read models, then enrich only the selected page.

**Architecture:** Reuse the existing `conversation:<id>` Core projection documents as the Conversation page boundary. Add a trace-list projection document with stable `(started_at, trace_id)` ordering, and extend the Projection source with cursor-aware page/count methods. The summary service performs receipt/artifact and canonical enrichment only for returned IDs.

**Tech Stack:** Go, MariaDB projection outbox, OpenSearch `search_after`, existing BKN Trace summary service and adapter tests.

---

### Task 1: Define cursor-aware list read contracts

**Files:**
- Modify: `src/port/driven/iprojectionsource/port.go`
- Modify: `src/domain/valueobject/evidencevo/summary.go`
- Test: `src/domain/service/evidencesvc/summary_service_test.go`

**Step 1:** Write failing service tests that require a `page_size=20` query to request 21 list candidates, preserve the opaque cursor, and keep the exact total separate from candidate entries.

**Step 2:** Run the focused tests and verify they fail because the source only exposes the 2,000-entry execution scan.

**Step 3:** Add typed trace/conversation list query and page results containing IDs, sort tuple, next cursor and count metadata; keep receipt expansion out of the list-selection contract.

**Step 4:** Run focused tests and commit the contract change.

### Task 2: Produce and query stable Trace list projection documents

**Files:**
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/rebuild.go`
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/store.go`
- Modify: `src/drivenadapter/httpaccess/opensearchprojection/sink.go`
- Modify: `src/drivenadapter/httpaccess/opensearchcoreprojection/source.go`
- Test: `src/drivenadapter/httpaccess/opensearchcoreprojection/source_test.go`

**Step 1:** Write failing adapter tests for a trace-list query with `size=limit+1`, exact scope filters, sort on `started_at` then `trace_id`, and OpenSearch `search_after` on the next page.

**Step 2:** Run the adapter tests and verify failure before adding the read model.

**Step 3:** Derive `trace-list:<trace_id>` snapshots from authoritative Receipt / call-fact changes and make the mapping expose the required keyword/date fields. Do not alter rebuild supervisor or alias-switch behavior.

**Step 4:** Implement page query/count methods; only an OpenSearch `404`/transport error remains an adapter error, never an in-memory fallback scan.

**Step 5:** Run focused adapter tests and commit.

### Task 3: Page Conversations from existing Conversation projection documents

**Files:**
- Modify: `src/drivenadapter/httpaccess/opensearchcoreprojection/source.go`
- Modify: `src/domain/service/evidencesvc/summary.go`
- Test: `src/drivenadapter/httpaccess/opensearchcoreprojection/source_test.go`
- Test: `src/domain/service/evidencesvc/summary_service_test.go`

**Step 1:** Write failing tests for sorted Conversation document selection using `(updated_at, conversation_id)` with cursor and a separate count.

**Step 2:** Write a failing service test proving receipt/artifact expansion receives only selected Conversation IDs.

**Step 3:** Add the query path over existing `conversation:<id>` documents and scoped receipt expansion for those IDs.

**Step 4:** Replace the full `loadExecutionSummaries` Conversation path with list selection followed by page-local enrichment; preserve authorization and all existing filters.

**Step 5:** Run focused tests and commit.

### Task 4: Batch canonical enrichment and end-to-end pagination verification

**Files:**
- Modify: `src/port/driven/isessionstore/port.go`
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/store.go`
- Modify: `src/domain/service/evidencesvc/summary.go`
- Test: `src/drivenadapter/dbaccess/mariadb/sessionstore/store_integration_test.go`
- Test: `src/domain/service/evidencesvc/summary_service_test.go`

**Step 1:** Write failing tests proving a Trace/Conversation page performs set-based canonical lookup rather than one query per Trace/Conversation.

**Step 2:** Implement batch session/call-fact lookup methods and replace page-local loops.

**Step 3:** Add multi-page tests for stable ordering, no duplicate/missing IDs, and page-size-bounded candidate/canonical work.

**Step 4:** Run `go test ./...`, `go vet ./...`, `git diff --check`, then commit and request review. Do not deploy, rebuild an index, or clean data.
