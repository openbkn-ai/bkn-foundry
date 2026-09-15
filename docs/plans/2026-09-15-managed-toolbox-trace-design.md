# Managed Toolbox Function Trace Design

## Decision

Close the managed ContextLoader-to-Toolbox audit gap without changing generic
Toolbox execution, the MCP wire contract, or the public lifecycle API. A
managed `execute_tool` invocation keeps the existing single ContextLoader
Operation and Receipt. The Operation retains the registered `execute_tool`
capability contract and records only the requested Toolbox and Tool identifiers
in its redacted input. The same trusted Operation ID is forwarded to Execution
Factory as the function's parent operation.

This deliberately does not create a second Execution Factory lifecycle. Two
independent operations for one invocation would make replay and terminal state
ambiguous. Execution Factory remains the producer of function-runtime evidence;
ContextLoader remains the owner of the operation lifecycle.

## Current gap

ContextLoader already creates and completes an Operation for every non-lifecycle
MCP tool. For `execute_tool`, its `tool_name` is the outer MCP command, so a
Trace only says `execute_tool`; it cannot identify the requested Toolbox
Function. The function runtime receives the current operation as a parent, but
the returned MCP receipt is intentionally reduced for agent-context size.

The SDK's typed managed-call helper requires the stable lifecycle identifiers
that the reduced view omits. This makes a managed `execute_tool` response
unreadable to that helper even though the Core record exists.

## Chosen flow

1. Before the lifecycle guard calls Core, recognize `execute_tool` and retain
   the opaque `toolbox_id` and `tool_id` in its redacted input snapshot. Remove
   nested function arguments entirely.
2. Keep `execute_tool` as the Operation `tool_name` and resolve its existing
   static capability profile. This preserves the registered
   `managed_children_only` evidence policy; the two opaque IDs identify the
   requested function without becoming a dynamic, unregistered tool name.
3. Continue to create exactly one Operation, keyed by the existing managed
   idempotency key. ContextLoader passes that Operation ID through its existing
   trusted headers to Execution Factory.
4. Persist an `execute_tool` terminal result as only status, a hash of the
   original terminal value, and (on failure) a safe error code and stage. Raw
   result rows and error text never enter the Trace payload.
5. Return a compact `execute_tool` receipt view containing stable readback
   identifiers, status and durability. Pending and terminal replays apply that
   same projection and omit the stored Operation. Other MCP tools retain the
   #1417 evidence-only receipt view.

## Boundaries

- No change to generic `toolboxes.execute()` or CLI output.
- No server-side inference from headers, session state, question text, or
  arbitrary caller data.
- No raw tool arguments, result rows, credentials, tokens, or secrets in
  operation metadata or returned references.
- No Studio work in this change; the existing Trace API is the read path.
- A direct Execution Factory Toolbox request remains unmanaged. It is a
  separate contract and issue.

## Failure and replay behaviour

The existing lifecycle guard remains authoritative: authorization rejection and
downstream failure complete the one operation as failed; an in-flight call
returns pending; a terminal replay returns the existing compact receipt and
never calls the function again. Every execute-tool terminal and replay path
uses the same safe receipt boundary.

## Verification

Unit tests cover safe input, terminal success and failure summaries, and
compact-reference selection. Lifecycle adapter tests capture the real Core
begin/finish payloads for successful and failed `execute_tool` calls. Guard
tests cover pending, successful replay and failed replay. SDK readback remains
an existing success-path capability; typed error readback is intentionally not
added here because it would change the public SDK error contract.
