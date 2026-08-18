# Trace Canonical Enrichment Batch Read Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove the per-trace database lookup made while enriching one Trace summary page, without changing the public Trace API or authorization behavior.

**Architecture:** Add one session-store batch read keyed by distinct non-empty trace IDs. The Trace summary service groups returned operation call facts by trace ID and keeps the first non-empty source module using the existing fact ordering. MariaDB executes one bounded `IN` query; the in-memory adapter mirrors its result and ordering.

**Tech Stack:** Go, Go `testing`, MariaDB session-store adapter, in-memory session-store adapter.

---

### Task 1: Specify the batch read contract with a failing test

**Files:**
- Modify: `bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore/store_test.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary_service_test.go`

1. Add a test that stores facts for multiple trace IDs and asks the new batch read for duplicate and blank IDs.
2. Assert that only requested trace IDs are returned and that facts within each trace remain chronological.
3. Add a Trace-summary regression test with multiple trace IDs to preserve the existing canonical source-module selection.
4. Run the focused test command and confirm the test fails because the batch API does not yet exist.

### Task 2: Add the minimal port and adapter implementation

**Files:**
- Modify: `bkn-trace/agent-observability/src/port/driven/isessionstore/port.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore/store.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/store.go`

1. Add `ListOperationCallFactsByTraceIDs(traceIDs []string)` to the transaction port.
2. Implement it in memory with an empty-input fast path, one fact scan, and deterministic per-trace chronological ordering.
3. Implement it in MariaDB with an empty-input fast path and one parameterized `trace_id IN (...)` query; do not interpolate values.
4. Retain the existing single-trace method for other callers.

### Task 3: Switch Trace summary enrichment to the batch contract

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary.go`

1. Collect distinct non-empty trace IDs once per summary page.
2. Call the batch contract once, group facts by trace ID, then apply the existing first non-empty `SourceModule` behavior.
3. Leave conversation enrichment, API shape, authorization, summary ordering, and fallback behavior unchanged.

### Task 4: Verify, document, and submit the isolated fix

**Files:**
- Modify: this plan with verification evidence/status.

1. Run targeted unit tests for the evidence service and both session-store adapters.
2. Run `gofmt`, `git diff --check`, and the relevant package tests again.
3. Commit only this issue's code/tests/plan, push the branch, create an English PR, and request review.

## Verification

- [x] Red: `GOCACHE=/tmp/openbkn-go-cache go test ./src/drivenadapter/memoryaccess/sessionstore -run TestListOperationCallFactsByTraceIDsReturnsRequestedFactsInTraceOrder -count=1` failed before implementation because the batch port did not exist.
- [x] Green: `GOCACHE=/tmp/openbkn-go-cache go test ./src/domain/service/evidencesvc ./src/drivenadapter/memoryaccess/sessionstore ./src/drivenadapter/dbaccess/mariadb/sessionstore -count=1` passed after implementation.
- [x] Formatting and whitespace: `gofmt` and `git diff --check` passed.
