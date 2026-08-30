# Install Status Internal Service Mapping Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refresh pod-backed internal Service health from the owning live workload instead of retaining stale install-time readiness.

**Architecture:** `merge.jq` already maps a Service name to a workload key using an exact match and a `-svc` fallback. Add a second fallback for the `-internal` alias. A self-contained shell test runs jq against fixture JSON to keep the dashboard merge contract executable without a cluster.

**Tech Stack:** Bash, jq, Kubernetes install-status manifest data.

---

### Task 1: Add the failing internal-Service regression test

**Files:**
- Create: `deploy/conf/install-status/merge_test.sh`
- Test: `deploy/conf/install-status/merge_test.sh`

**Step 1: Write the failing test**

Create fixture JSON containing a pod-sourced `agent-observability-internal`
entry with `ready: "1/2"` and a live `agent-observability` Deployment with one
ready replica. Invoke `merge.jq`, then assert that its merged entry is
`ready: "1/1"` and `state: "up"`. Include existing `-svc` and unmatched cases.

**Step 2: Run the test to verify it fails**

Run: `bash deploy/conf/install-status/merge_test.sh`

Expected: the internal-Service assertion fails because the current fallback
only strips `-svc`.

### Task 2: Map `-internal` aliases to their live workload

**Files:**
- Modify: `deploy/conf/install-status/merge.jq:57`
- Test: `deploy/conf/install-status/merge_test.sh`

**Step 1: Implement the minimal production change**

Extend the workload lookup to try the Service name with `-internal` removed
after the exact and `-svc` forms, leaving all other merge behavior unchanged.

**Step 2: Run the focused regression test**

Run: `bash deploy/conf/install-status/merge_test.sh`

Expected: all assertions pass.

### Task 3: Verify install-status artifacts

**Files:**
- Modify: `deploy/conf/install-status/merge.jq`
- Create: `deploy/conf/install-status/merge_test.sh`

**Step 1: Check jq syntax and render behavior**

Run: `jq -n --arg now '2026-08-29T00:00:00Z' --slurpfile snap <fixture> --slurpfile work <fixture> -f deploy/conf/install-status/merge.jq`

Expected: valid JSON with updated internal-Service readiness.

**Step 2: Inspect the scoped diff**

Run: `git diff --check && git diff -- deploy/conf/install-status/merge.jq deploy/conf/install-status/merge_test.sh docs/plans/2026-08-29-install-status-internal-design.md docs/plans/2026-08-29-install-status-internal.md`

Expected: only the intended mapping, regression test, and implementation documentation are changed.
