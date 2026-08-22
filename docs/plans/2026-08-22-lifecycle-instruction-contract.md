# Lifecycle Instruction Contract Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the ContextLoader MCP lifecycle contract concise, consistent with the explicit start mode, and schema-enforced for completed finishes.

**Architecture:** The MCP adapter continues to own lifecycle schemas and localized tool metadata. `bkn_start_interaction` exposes an explicit `new`/`continue` choice; server instructions translate that choice into a small execution sequence. `bkn_finish_interaction` uses JSON Schema to require the final answer only for `completed`.

**Tech Stack:** Go, JSON Schema Draft 7, embedded MCP locale JSON, Go unit tests.

---

### Task 1: Define the lifecycle wire contract

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas.go`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_schema_test.go`

1. Add failing tests for required `conversation_mode`, its two start branches, and `completed` requiring `answer`.
2. Run `go test ./server/driveradapters/mcp -run 'TestLifecycle' -count=1` and confirm failure on the old schema.
3. Add the smallest `oneOf` branches that express those rules.
4. Re-run the targeted tests.

### Task 2: Align lifecycle execution and guidance

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter.go`
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_validation.go`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/lifecycle_adapter_test.go`

1. Add failing tests for `new` creating a Conversation and invalid start branches returning correction guidance before Core.
2. Make conversation creation depend on `conversation_mode=new`, not an omitted ID.
3. Re-run focused adapter tests.

### Task 3: Publish concise localized instructions

**Files:**
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas/instructions.txt`
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas/locales/en-US/instructions.txt`
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas/tools_meta.json`
- Modify: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas/locales/en-US/tools_meta.json`
- Test: `adp/context-loader/agent-retrieval/server/driveradapters/mcp/locale_test.go`

1. Add failing locale assertions for the explicit mode mapping and literal `bkn_context` shape.
2. Replace only lifecycle wording; leave query-routing guidance unchanged.
3. Re-run locale tests.

### Task 4: Verify and submit

1. Run `gofmt`, targeted MCP tests, `go vet ./server/driveradapters/mcp/...`, `make test`, and `git diff --check`.
2. Record the pre-existing full-suite race in the PR if it remains.
3. Commit, push, open the PR with `Closes #1116`, and request review.
