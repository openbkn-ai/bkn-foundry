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

## #1161 completion remains a separate cross-service plan

This PR deliberately stops at the managed ContextLoader boundary and does not
claim to create an independently readable function Operation. Before #1161 can
close, its dedicated design and execution plan must cover:

- a distinct Execution Factory function Operation linked to the managed
  ContextLoader Interaction and upstream source Receipt/Operation;
- authoritative owner checks and explicit, versioned invocation context;
- success, target failure, permission rejection, and idempotent replay;
- an E2E scenario spanning two Interactions in one Conversation, function Trace
  readback, and finalization.

The existing `bkn-trace-managed-business-interaction.mjs` validates managed
ContextLoader queries and a rejected query, but does not execute a provisioned
Toolbox function or read a distinct function Operation. It is therefore not
presented as #1161 acceptance evidence.
