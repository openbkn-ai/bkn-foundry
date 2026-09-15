# Agent exclusion collation fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent MariaDB collation errors when summary pagination excludes a non-ASCII Agent name, without changing identity matching semantics or the summary-only read path.

**Architecture:** Keep filtering in `sessionstore.ListConversationSummaryIdentities`. Refactor the predicate builder to compare all values with the UTF-8 `agent_name` column, but compare only ASCII values with the `ascii_bin` identity columns. Add unit coverage for generated predicates and a real MariaDB regression when the integration environment is available.

**Tech Stack:** Go, `database/sql`, MariaDB, existing sessionstore tests.

---

### Task 1: Add failing predicate tests

**Files:**
- Create or modify: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/summary_page_test.go`

**Steps:**

1. Add tests asserting that a Chinese-only exclusion value produces an `agent_name` predicate and no ASCII identity predicates.
2. Add tests asserting that ASCII values are used for all three fields, and mixed values use all values for `agent_name` but only ASCII values for identity fields.
3. Run the focused test and confirm the current helper fails because it emits the Chinese placeholder for ASCII columns.

### Task 2: Implement the minimal predicate split

**Files:**
- Modify: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/summary_page.go`

**Steps:**

1. Preserve trimming and de-duplication.
2. Partition values by ASCII representability.
3. Generate the `agent_name` condition from all values.
4. Generate identity conditions only when the ASCII subset is non-empty.
5. Keep existing `IS NULL OR NOT IN` and `ascii_bin` case semantics; do not cast database columns.
6. Run the focused predicate tests and confirm they pass.

### Task 3: Verify end-to-end summary behavior

**Files:**
- Modify if needed: `bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/store_integration_test.go`

**Steps:**

1. Run the sessionstore package tests.
2. Run the evidencesvc summary tests that cover Chinese Agent names and stable Agent IDs.
3. If the repository's MariaDB integration harness is available, add/run a regression with production `utf8mb4`/`ascii_bin` columns and verify count plus page selection.
4. Run `gofmt` and the relevant Go test commands.

### Task 4: Review the final diff

1. Confirm only the isolated worktree and the issue-specific files changed.
2. Confirm the existing summary-only behavior remains intact.
3. Report test results and any unavailable MariaDB integration dependency.
