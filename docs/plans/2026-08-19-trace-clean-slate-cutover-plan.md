# Trace 0.1.4 Clean-Slate Cutover Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Provide an operator runbook for cutting Trace over to isolated 0.1.4 storage and retiring 0.1.3 Trace data only after acceptance.

**Architecture:** The runbook treats MariaDB, Trace, Evidence, and Projection as explicit old/new target pairs. It separates non-destructive cutover validation from destructive retirement, and leaves shared log indexes out of the automated cleanup path until an operator supplies a reviewed, bounded filter.

**Tech Stack:** Helm values, MariaDB, OpenSearch, Bash cleanup script, Markdown.

---

### Task 1: Document the storage boundary and cutover inputs

**Files:**
- Create: `bkn-trace/agent-observability/docs/0.1.4-clean-slate-cutover.md`

**Step 1:** Record the Owner decision: 0.1.3 Trace/log data is not migrated or read by 0.1.4.

**Step 2:** List the exact configurable targets and make the default index names non-authoritative for a clean-slate deployment.

**Step 3:** Describe cutover validation and the rollback window before retirement.

### Task 2: Document controlled retirement

**Files:**
- Modify: `bkn-trace/agent-observability/README.md`
- Test: `bkn-trace/agent-observability/scripts/test_cleanup_legacy_bkn_trace_data.sh`

**Step 1:** Link the existing cleanup-script section to the runbook and state that it never runs automatically.

**Step 2:** Describe preview, three-part Owner confirmation, explicit `--confirm`, zero-count verification, and the safe `status=absent` handling for an explicitly targeted index that has already been retired.

**Step 3:** State that shared log indexes need a separate reviewed delete-by-query filter and are not cleanup-script targets.

**Step 4:** Run the cleanup-script safety-contract test to verify the documented guardrails remain true.

### Task 3: Review the documentation diff

**Files:**
- Verify: `bkn-trace/agent-observability/docs/0.1.4-clean-slate-cutover.md`
- Verify: `bkn-trace/agent-observability/README.md`

**Step 1:** Run `git diff --check`.

**Step 2:** Verify links and configured defaults against `charts/agent-observability/values.yaml` and `src/conf/*.go`.

**Step 3:** Commit the documentation and request review; do not execute deployment or cleanup.
