# Trace List Pagination Implementation Plan

**Goal:** Select Trace and Conversation identities from the canonical Core lifecycle ledger before expanding current-page projection details and canonical state.

## Tasks

1. Add an optional summary identity page port over the existing lifecycle store.
2. Implement Trace and Conversation keyset pagination plus independent exact totals in MariaDB.
3. Add batch Trace/Conversation ID filters to Core and Evidence Projection sources.
4. Route ordinary Trace/Conversation list calls through identity selection and page-local expansion while retaining the legacy path for non-pushable filters.
5. Replace per-item canonical reads with batch Conversation, Interaction, Assembly Revision, Operation, and Trace source-module queries.
6. Verify candidate bounds, stable cursor traversal, batch ID pushdown, module tests, vet, and diff hygiene before review.

No deploy, index rebuild, cleanup, schema migration, or alias change belongs to this plan.
