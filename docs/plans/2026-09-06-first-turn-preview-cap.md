# First-Turn Conversation Preview Cap Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ensure a business-provenance conversation row retains the first managed interaction's question and result when newer receipt activity exceeds the per-conversation receipt cap.

**Architecture:** Keep the existing bounded receipt projection for trace summaries. For the conversation-list preview only, identify the canonical first interaction for each selected conversation and include it in Core projection authorization independently of the collapsed recent-receipt set. The terminal artifact lookup remains restricted to question/result types and selected first interactions.

**Tech Stack:** Go, agent-observability domain service, OpenSearch Core/evidence projection adapters, Go unit tests.

---

### Task 1: Reproduce the receipt-cap regression

**Files:**
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection/source_test.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary_service_test.go`

**Step 1: Write the failing test**

Create a Core projection fixture with one conversation whose first interaction has terminal question/result artifacts and seven receipts, while two later interactions contribute twenty newer receipts. Assert that the authorized interaction set includes the canonical first interaction and that its terminal artifacts survive the projection.

**Step 2: Run test to verify it fails**

Run: `go test ./bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection -run FirstInteraction -count=1`

Expected: FAIL because the collapsed receipt query supplies only the two recent interactions.

**Step 3: Add a list-level regression test**

Assert the conversation summary selects the first lifecycle interaction's coherent question/result pair, not an empty pair or a later interaction.

### Task 2: Preserve first-turn authorization without widening scans

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection/source.go`

**Step 1: Limit preview IDs to canonical first interactions**

Derive one lowest-ordinal interaction ID per selected managed conversation while retaining all interactions for the displayed count.

**Step 2: Complete receipt authorization for those IDs**

When the Core projection receives selected terminal interaction IDs, query their receipts within the caller-owned candidate budget and merge only scope-authorized IDs with the collapsed recent receipt set. Do not alter the global per-conversation recent receipt cap.

**Step 3: Run focused tests to verify green**

Run:
`go test ./bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection ./bkn-trace/agent-observability/src/domain/service/evidencesvc -count=1`

Expected: PASS.

### Task 3: Verify no broader regression

**Files:**
- No additional production files expected.

**Step 1: Run module checks**

Run: `make test && make lint && make license-check && make build` from `bkn-trace/agent-observability`.

**Step 2: Inspect the diff**

Confirm it touches only the summary selection and Core projection authorization path; it must not change HTTP schemas, persistence, scan-cap constants, or Studio.

**Step 3: Commit**

Run:
`git add docs/plans/2026-09-06-first-turn-preview-cap.md bkn-trace/agent-observability/src/domain/service/evidencesvc/summary.go bkn-trace/agent-observability/src/domain/service/evidencesvc/summary_service_test.go bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection/source.go bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection/source_test.go`

Commit message: `fix(bkn-trace): preserve first-turn conversation previews`
