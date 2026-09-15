# Managed Toolbox Function Trace Design

## Decision

Close the managed ContextLoader-to-Toolbox audit gap without changing generic
Toolbox execution, the MCP wire contract, or the public lifecycle API. A
managed `execute_tool` invocation keeps the existing single ContextLoader
Operation and Receipt. The Operation identifies the concrete capability that
was requested, and the same trusted Operation ID is forwarded to Execution
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

1. Before the lifecycle guard calls Core, recognize `execute_tool` and validate
   the required `toolbox_id` and `tool_id` as opaque non-empty identifiers.
2. Derive a stable, non-secret capability identity from those identifiers. Store
   it in the Operation's capability profile and use a function-specific
   operation display name. Do not use function arguments, labels, query text,
   or model output.
3. Continue to create exactly one Operation, keyed by the existing managed
   idempotency key. ContextLoader passes that Operation ID through its existing
   trusted headers to Execution Factory.
4. Return the existing complete `OperationReceipt` for `execute_tool`, so the
   published SDK return type stays accurate and callers can read it back.
   Other MCP tools retain the #1417 compact evidence-only receipt view.
5. SDK uses that unchanged receipt contract with its existing owner-scoped
   receipt/operation read APIs.

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

The existing lifecycle guard remains authoritative: invalid capability IDs fail
before downstream execution; authorization rejection and downstream failure
complete the one operation as failed; an in-flight call returns pending; a
terminal replay returns the existing receipt and never calls the function
again. The function identity derives from the validated request identifiers,
so these outcomes stay attributable to the same requested capability without
recording request content.

## Verification

Unit tests cover identity derivation, compact-reference selection, absent IDs,
and no leakage of function arguments. Lifecycle adapter tests assert the Core
request and forwarded Execution Factory parent ID. SDK tests verify that the
managed `execute_tool` receipt passes runtime validation and can drive the
existing read APIs. One E2E path covers two interactions in a conversation,
success, rejection, pending/replay, and readback.
