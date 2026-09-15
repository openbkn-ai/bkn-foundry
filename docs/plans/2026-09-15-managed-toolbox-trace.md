# Managed Toolbox Function Trace Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make a managed ContextLoader `execute_tool` call independently readable as the requested Toolbox Function while preserving one lifecycle operation and the existing generic Toolbox contract.

**Architecture:** ContextLoader retains `execute_tool` as the registered capability contract and records its safe `toolbox_id` / `tool_id` in the redacted input snapshot. It forwards the existing Operation through trusted Execution Factory headers. The `execute_tool` receipt adds only stable readback identifiers to the evidence-only #1417 view.

**Tech Stack:** Go, ContextLoader MCP adapter, Execution Factory Toolbox runtime, BKN Trace lifecycle API, TypeScript SDK, Go and Vitest tests.

---

## Implemented scope

- [x] Keep the registered outer `execute_tool` capability profile, including
  `managed_children_only`; record only `kn_id`, `toolbox_id`, and `tool_id` in
  the redacted operation input.
- [x] Return the minimum stable receipt identifiers for a completed managed
  call without restoring Core ownership, request, trace, operation-key,
  row-version, or timestamp fields.
- [x] Store only a terminal status, SHA-256 hash, and safe error classification
  for `execute_tool` output or failure. Function rows and error text do not
  enter the lifecycle payload.
- [x] Apply the same compact receipt projection to pending, successful replay,
  and failed replay; omit the stored Operation for those execute-tool replies.
- [x] Include a stable structured error code beside the bounded replay receipt.
  The paired SDK change exposes it through an additive `ManagedToolError`, so
  callers can read back terminal and pending receipts without treating replay
  as success.
- [x] Cover direct helper, lifecycle adapter success/failure, and guard replay
  states in the ContextLoader MCP package.

## Deliberately deferred

- Automatic Trace generation for direct generic Execution Factory Toolbox API
  calls. That is a separate cross-service contract.

### Task 4: Verify Execution Factory receives the trusted parent operation

**Files:**
- Test: `adp/context-loader/agent-retrieval/server/drivenadapters/operator_integration_tools_test.go`
- Test: `adp/execution-factory/operator-integration/server/logics/toolbox/function_runtime_headers_test.go`

**Step 1: Write a failing boundary test**

Execute through ContextLoader with a lifecycle context and assert only the server-captured conversation, interaction and parent operation headers reach the platform Function runtime. Assert body-supplied identifiers cannot replace them.

**Step 2: Run focused tests**

Run the respective Go package tests.

**Step 3: Make only any wiring correction proved necessary**

The current forwarding path may already satisfy the test. Do not edit Execution Factory unless the boundary test demonstrates a missing trusted value.

**Step 4: Re-run focused tests and commit only changed files**

### Task 5: Add an end-to-end readback scenario and documentation

**Files:**
- Modify: `bkn-sdk/test/e2e/bkn-trace-managed-business-interaction.mjs`
- Modify: `docs/plans/2026-09-15-managed-toolbox-trace-design.md`

**Step 1: Add E2E coverage**

Start a conversation, run upstream managed queries, invoke a Toolbox Function, read its operation and receipt using returned IDs, then start a second interaction in the same conversation. Include success, rejection and replay; assert one terminal operation per idempotency key.

**Step 2: Run relevant test suites**

Run ContextLoader MCP package tests, Execution Factory Toolbox package tests, SDK unit tests, and the available managed-business E2E suite.

**Step 3: Update design status and commit**

Record actual verification and limitations. Keep direct generic Toolbox execution explicitly out of scope.
