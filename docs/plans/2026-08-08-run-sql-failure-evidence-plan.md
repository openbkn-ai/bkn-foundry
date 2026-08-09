# run_sql Failure Evidence Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make every failed `run_sql` attempt replayable and diagnostically useful without changing MCP inputs.

**Architecture:** Context Loader records the SQL query before returning each known failure and emits the existing artifact-linked `data.query.observed` fact with structured failure fields. Agent Observability accepts and summarizes those optional fields; the durable Operation Receipt remains authoritative for terminal and retry status.

**Tech Stack:** Go, Context Loader evidence ingestion, Agent Observability evidence contract, Go unit tests.

---

### Task 1: Define failed run_sql evidence

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/infra/bkntrace/evidence.go`
- Test: `adp/context-loader/agent-retrieval/server/infra/bkntrace/evidence_test.go`

1. Add a failing test proving a failed SQL attempt writes query and error-result Artifacts and emits one linked `data.query.observed` event.
2. Run the focused test and confirm it fails because failure evidence construction is missing.
3. Add the minimal failure descriptor and Artifact/event construction.
4. Run the focused evidence tests and confirm they pass.

### Task 2: Emit evidence from every run_sql failure boundary

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/logics/knrunsql/index.go`
- Test: `adp/context-loader/agent-retrieval/server/logics/knrunsql/index_test.go`

1. Add failing tests for empty SQL, missing resource placeholder, read-only guard rejection, and Vega failure.
2. Confirm each test fails because no failure evidence is emitted.
3. Emit the stable stage/code descriptor immediately before each error return.
4. Run package tests and verify success behavior remains unchanged.

### Task 3: Accept and summarize structured failure fields

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/service.go`
- Modify: `bkn-trace/agent-observability/src/domain/valueobject/evidencevo/summary.go`
- Test: `bkn-trace/agent-observability/src/domain/service/evidencesvc/contract_v22_test.go`
- Test: `bkn-trace/agent-observability/src/domain/valueobject/evidencevo/summary_test.go`

1. Add failing contract tests for the optional `status`, `error_stage`, `error_code`, and `safe_error_summary` fields.
2. Add a failing summary test proving a failed data query surfaces the structured error.
3. Extend the 2.2 allowlist and summary error recognition without weakening existing required Artifact links.
4. Run focused contract and summary tests.

### Task 4: Verify the changed modules

**Files:**
- No production changes expected.

1. Run `gofmt` on changed Go files.
2. Run Context Loader focused package tests.
3. Run Agent Observability focused package tests.
4. Run each changed module's standard unit-test target if practical.
5. Present the Trace-only diff separately from the unrelated Ingress worktree changes; do not commit before human review.
