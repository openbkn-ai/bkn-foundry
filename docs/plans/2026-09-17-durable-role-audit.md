# Durable Role Audit Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ensure a committed role-management mutation always has a durable, queryable audit event, even when appending the tamper-evident chain is temporarily unavailable.

**Architecture:** A role mutation writes an `AuditLog` row marked `pending` in the same database transaction as the role, binding, or policy change. The row is immediately returned by the existing audit read API. A background chain worker subsequently appends pending rows in order, retrying safely; chain contention must never remove the committed audit fact. The existing post-handler middleware continues to record failed requests, but skips a successful role mutation that already persisted its transactional audit event.

**Tech Stack:** Go, Gin, GORM, Casbin/gorm-adapter, SQLite unit tests; MariaDB-compatible schema migration via GORM AutoMigrate.

---

### Task 1: Model the durable pending audit event

**Files:**
- Modify: `bkn-safe/server/internal/model/model.go`
- Modify: `bkn-safe/server/internal/audit/audit.go`
- Test: `bkn-safe/server/internal/audit/audit_test.go`

**Step 1: Write the failing test**

Add a test that enqueues an audit entry through a transaction and verifies that it is visible to `Store.List` before it has a chain sequence, with a `pending` chain state.

**Step 2: Run the test to verify it fails**

Run: `cd bkn-safe/server && go test ./internal/audit -run TestEnqueueMakesPendingAuditEventQueryable -count=1`

Expected: FAIL because pending events and transaction enqueueing do not exist.

**Step 3: Write the minimal implementation**

Add an indexed `chain_state` column to `AuditLog`, an `Enqueue`/`EnqueueBatch` API that accepts the caller's transaction-bound `*gorm.DB`, assigns the immutable event facts and timestamp, and inserts a pending row without requiring a chain append.

**Step 4: Run the test to verify it passes**

Run the command from Step 2 and confirm PASS.

### Task 2: Chain pending events without losing the source record

**Files:**
- Modify: `bkn-safe/server/internal/audit/chain.go`
- Modify: `bkn-safe/server/internal/audit/chain_test.go`
- Test: `bkn-safe/server/internal/audit/chain_test.go`

**Step 1: Write the failing test**

Add a test that persists a pending row, forces an initial chain append failure, then runs the pending append path and verifies: the original audit row remains queryable, is chained exactly once after retry, and `Verify` succeeds.

**Step 2: Run the test to verify it fails**

Run: `cd bkn-safe/server && go test ./internal/audit -run TestAppendPendingRetainsEventAfterChainFailure -count=1`

Expected: FAIL because the store only creates chained rows and has no pending-event worker path.

**Step 3: Write the minimal implementation**

Refactor chain append so it can atomically assign sequence/hash fields to existing pending rows. Select pending rows in creation/id order, append them under the existing cross-replica sequence retry rules, and mark them `chained` in the same transaction. Add bounded polling/retry worker lifecycle methods on `audit.Store` and start it from application boot.

**Step 4: Run the test to verify it passes**

Run the command from Step 2 and confirm PASS.

### Task 3: Carry request audit facts into role transactions

**Files:**
- Modify: `bkn-safe/server/internal/httpapi/audit.go`
- Modify: `bkn-safe/server/internal/httpapi/adminwrite_svc.go`
- Modify: `bkn-safe/server/internal/httpapi/authz.go`
- Modify: `bkn-safe/server/internal/authz/transaction.go`
- Test: `bkn-safe/server/internal/httpapi/admin_test.go`

**Step 1: Write the failing test**

Add one end-to-end HTTP test that, as a security-role user, creates and edits a custom role, grants/revokes a permission, then binds/unbinds a member. Assert exactly one durable audit event per successful request by its request ID, including while the chain worker is intentionally unavailable.

**Step 2: Run the test to verify it fails**

Run: `cd bkn-safe/server && go test ./internal/httpapi -run TestRoleManagementSuccessPersistsAuditBeforeChainAppend -count=1`

Expected: FAIL because successful role operations are only audited after their business write and cannot atomically persist an audit event.

**Step 3: Write the minimal implementation**

Have audit middleware place one mutable request-audit operation object in the request context. Add transaction-scoped role policy/binding methods to `authz.PolicyTransaction`. Refactor each role mutation to enqueue its success event using the same GORM/Casbin transaction and mark the request operation handled. After the handler returns, middleware must skip duplicate success logging for handled operations, but retain existing 4xx/5xx audit behavior.

**Step 4: Run the test to verify it passes**

Run the command from Step 2 and confirm PASS.

### Task 4: Verify migration, race behavior, and regressions

**Files:**
- Modify: `bkn-safe/server/internal/database/database_test.go` if model migration assertions require it
- Test: `bkn-safe/server/internal/audit/chain_test.go`
- Test: `bkn-safe/server/internal/httpapi/admin_test.go`

**Step 1: Write failing edge-case tests**

Add tests proving that: pending rows survive a chain retry; two workers cannot chain one event twice; a failed role request still produces exactly one 4xx record; and cancellation after a committed role write does not lose its pending audit event.

**Step 2: Run focused packages**

Run: `cd bkn-safe/server && go test ./internal/audit ./internal/authz ./internal/httpapi -count=1`

Expected: PASS after the implementation.

**Step 3: Run full service verification**

Run: `cd bkn-safe && make test && make lint`

Expected: PASS with no changed API contract and no uncommitted generated artifacts.

### Task 5: Prepare the reviewable change

**Files:**
- Modify: `docs/plans/2026-09-17-durable-role-audit.md`
- Create/update: PR description following `.github/pull_request_template.md`

**Step 1: Review the diff**

Run: `git diff --check && git diff --stat && git status --short`

**Step 2: Commit logically separated changes**

Run conventional commits after fresh tests, for example `fix(bkn-safe): persist role audit events transactionally`.

**Step 3: Push and open the PR**

Use `Closes #1621`, complete the repository PR template, add `by-agent`, and request the bkn-safe code owner review. Do not apply the migration to any deployed database; the owner confirms the rollout independently.
