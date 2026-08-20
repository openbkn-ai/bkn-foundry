# Trace Schema Version Ledger Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fail Trace Core startup deterministically when the MariaDB schema and image manifest do not match.

**Architecture:** Replace concatenated schema replay with an ordered embedded migration manifest, persisted in a Trace-owned ledger table and serialized with a MariaDB advisory lock. Bootstrap validates explicit configuration before opening stores.

**Tech Stack:** Go, MariaDB, `database/sql`, existing embedded migration SQL, Go testing.

---

### Task 1: Model the migration manifest

**Files:**
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/schema.go`
- Test: `src/drivenadapter/dbaccess/mariadb/sessionstore/schema_test.go`

1. Add failing tests for ordered versions, non-empty checksums and stable latest version.
2. Run the focused package test and observe failure.
3. Replace `SchemaSQL()` as migration source with typed `Migrations()` entries for v013-v016; retain `SchemaSQL()` only for existing schema-content callers if needed.
4. Re-run focused tests.

### Task 2: Apply and validate the ledger

**Files:**
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/store.go`
- Test: `src/drivenadapter/dbaccess/mariadb/sessionstore/store_test.go`
- Test: `src/drivenadapter/dbaccess/mariadb/sessionstore/store_integration_test.go`

1. Add failing unit tests for migration-state decisions (behind, ahead, checksum mismatch, migration disabled).
2. Run tests and observe failures.
3. Add ledger DDL, `GET_LOCK`/`RELEASE_LOCK`, state loading, checksum validation and missing-version application.
4. Add an integration test which runs `Migrate` twice and asserts ledger rows.
5. Run focused unit and integration-tag test (the latter skips without DSN).

### Task 3: Make startup configuration explicit

**Files:**
- Modify: `src/conf/core.go`
- Modify: `src/boot/bootstrap.go`
- Test: `src/conf/core_test.go`
- Test: `src/boot/bootstrap_test.go`

1. Add failing tests for invalid auto-migrate input and the MariaDB default.
2. Refactor config construction to return validation errors and propagate them before stores/workers initialize.
3. Run focused configuration and bootstrap tests.

### Task 4: Document the ownership boundary

**Files:**
- Modify: `data-migrator/config.monorepo.yaml`
- Modify: `bkn-trace/agent-observability/README.md`

1. State that `bkn_trace` is a self-managed versioned schema and document startup failure/clean-slate recovery.
2. Run formatting and repository checks.

### Task 5: Verify and hand off

1. Run `go test ./...`, `go vet ./...`, and `git diff --check` from `bkn-trace/agent-observability`.
2. Commit with `fix(trace): version self-managed MariaDB schema`.
3. Create a PR using the repository template, with `Closes #833`, then wait for review.
