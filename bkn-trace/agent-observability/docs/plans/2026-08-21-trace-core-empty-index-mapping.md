# Trace Core Empty Index Mapping Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep `bkn-trace-core` healthy when a newly installed projection index contains no conversation documents.

**Architecture:** The conversation-audit source queries `created_at`, `external_conversation_key`, and `generation`, but the bootstrap projection mapping currently defines only receipt fields. Add those fields to the versioned projection mapping. For an existing alias, submit a separate, additive mapping containing only those three conversation fields. Receipt fields such as `operation_id` have changed mapping shape across released versions, so including them in an alias-wide update can make OpenSearch reject startup. The targeted update repairs existing empty indexes without rebuilding aliases, deleting data, or redefining receipt fields.

**Tech Stack:** Go, OpenSearch 2.x mappings, Go unit tests.

---

### Task 1: Lock the empty-index mapping contract

**Files:**
- Modify: `src/drivenadapter/httpaccess/opensearchprojection/sink_test.go`

**Step 1: Write the failing test**

Add a focused assertion that the mapping sent by `PrepareVersion` declares the three fields needed by `opensearchconversationaudit.buildQuery`: `created_at` as a date, `external_conversation_key` as a keyword-searchable string, and `generation` as a numeric value.

**Step 2: Run test to verify it fails**

Run: `GOCACHE=/tmp/openbkn-go-build-cache go test ./src/drivenadapter/httpaccess/opensearchprojection -run TestPrepareVersion -count=1`

Expected: FAIL because the bootstrap mapping does not yet declare the conversation fields.

**Step 3: Implement the minimal mapping change**

Add only the required conversation fields to `receiptProjectionIndexMapping` in `sink.go`. Preserve the legacy dynamic `text + keyword` mapping shape for existing string fields, retain all receipt fields and the existing alias/bootstrap behavior.

**Step 4: Run test to verify it passes**

Run the focused package test again.

### Task 2: Verify the source query against the mapped empty index

**Files:**
- Modify: `src/drivenadapter/httpaccess/opensearchconversationaudit/source_test.go`
- Modify: `src/drivenadapter/httpaccess/opensearchprojection/sink_test.go` if a reusable mapping assertion helper is needed

**Step 1: Add a regression test**

Use the generated bootstrap mapping with a mock OpenSearch request. Verify that a pre-existing projection alias receives an additive `/_mapping` update containing only the three conversation fields and does not redefine `operation_id` or any other receipt field. This covers both new installs and already-deployed empty indexes without requiring a synthetic conversation document.

**Step 2: Run the focused tests**

Run: `GOCACHE=/tmp/openbkn-go-build-cache go test ./src/drivenadapter/httpaccess/opensearchconversationaudit ./src/drivenadapter/httpaccess/opensearchprojection -count=1`

**Step 3: Run module verification**

Run: `make lint && make test && make build && helm lint charts/agent-observability`.

### Task 3: Deliver the patch

**Files:**
- Modify: the two source/test files above
- Add: this implementation plan

**Step 1:** Review `git diff` and confirm no unrelated files changed.

**Step 2:** Commit with `fix(trace): map conversation fields for empty projection index`.

**Step 3:** Push the branch and open a reviewable PR with the root cause, regression coverage, and local verification results.
