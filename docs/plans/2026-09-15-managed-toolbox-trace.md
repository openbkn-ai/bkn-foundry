# Managed Toolbox Function Trace Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make a managed ContextLoader `execute_tool` call independently readable as the requested Toolbox Function while preserving one lifecycle operation and the existing generic Toolbox contract.

**Architecture:** ContextLoader derives a safe capability identity from the validated `toolbox_id` and `tool_id` before creating its existing operation. It forwards that operation through the existing trusted Execution Factory headers. The `execute_tool` response carries a compact lifecycle reference that satisfies the SDK's typed managed-call reader; other MCP tools retain their evidence-only receipt view.

**Tech Stack:** Go, ContextLoader MCP adapter, Execution Factory Toolbox runtime, BKN Trace lifecycle API, TypeScript SDK, Go and Vitest tests.

---

### Task 1: Specify and test the managed capability identity

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard.go`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard_test.go`

**Step 1: Write failing tests**

Cover `execute_tool` with opaque `toolbox_id` / `tool_id`, asserting that the operation intent receives the function-specific name and safe capability profile. Cover missing IDs, another MCP tool, and a request containing raw arguments; the profile must contain only the two IDs and capability kind.

**Step 2: Run the focused test**

Run: `go test ./adp/context-loader/agent-retrieval/server/driveradapters/mcp -run 'TestSessionGuard.*ExecuteTool' -count=1`

Expected: FAIL because the current intent has `ToolName: execute_tool` only.

**Step 3: Implement the minimal identity derivation**

Add a private helper used only by the lifecycle guard. It recognizes the `execute_tool` input contract, validates the two identifier fields, derives a stable function display name, and provides a safe capability-profile override. Do not parse or store function arguments.

**Step 4: Re-run the focused test**

Expected: PASS.

**Step 5: Commit**

```bash
git add adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard.go adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard_test.go
git commit -m "feat(trace): identify managed toolbox functions"
```

### Task 2: Preserve the identity in Core lifecycle creation

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter.go`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter_test.go`

**Step 1: Write a failing adapter test**

Capture the Core begin payload for an `execute_tool` call. Assert the function-specific `tool_name`, capability profile and normalized input exclude raw arguments while retaining existing idempotency and parent-operation rules.

**Step 2: Run it and observe the failure**

Run: `go test ./adp/context-loader/agent-retrieval/server/driveradapters/mcp -run Test.*ExecuteTool.*Lifecycle -count=1`

**Step 3: Pass the derived identity through the existing guard intent**

Extend the private `operationIntent` only as needed. Preserve the request's outer MCP command for dispatch; change only lifecycle metadata.

**Step 4: Re-run focused tests**

Expected: PASS.

**Step 5: Commit**

```bash
git add adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter.go adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter_test.go
git commit -m "feat(trace): persist managed toolbox identity"
```

### Task 3: Return a compact SDK-readable reference for execute_tool

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard.go`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard_test.go`
- Test: `bkn-sdk/test/unit/context-loader.test.ts`

**Step 1: Write failing Go and SDK tests**

For completed, failed, pending and terminal-replay `execute_tool` outcomes, assert `bkn_receipt` includes only the stable readback identifiers plus status and durability. Assert a normal business tool remains on the reduced #1417 view. In SDK, pass this reference to `callManagedTool` and assert it validates.

**Step 2: Run focused tests**

Run Go test above and `npm test -- --run test/unit/context-loader.test.ts` in `bkn-sdk`.

**Step 3: Implement request-scoped receipt projection**

Select the reference projection using the outer tool name. Never restore the full receipt to MCP responses. Leave `OperationReceipt` API shape unchanged; the SDK uses the returned IDs with its existing read client.

**Step 4: Re-run focused tests**

Expected: PASS.

**Step 5: Commit each repository independently**

```bash
git add adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard.go adp/context-loader/agent-retrieval/server/driveradapters/mcp/session_guard_test.go
git commit -m "fix(trace): return managed toolbox receipt references"
```

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
