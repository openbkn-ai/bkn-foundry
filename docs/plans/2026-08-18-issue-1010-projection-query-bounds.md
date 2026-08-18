# Issue #1010: bound Core receipt projection reads

> **Owner:** BKN Trace
> **Scope:** First, isolated performance correction for the OpenSearch Core receipt query. Projection rebuild is already addressed by `24339bad`; canonical SQL batching and list-summary materialization remain separate follow-up changes.

## Root cause

The summary service intentionally caps a bounded projection scan at 2,000 entries. The OpenSearch Core adapter interprets that limit as `limit * 20 + 1`, however, and therefore asks OpenSearch for up to 10,000 receipt candidates before service-side aggregation and pagination.

## Contract for this change

- `iprojectionsource.Query.Limit` is the receipt candidate budget for one Core projection query.
- The Core adapter asks for at most `Limit + 1` documents: the single extra document preserves the existing truncation signal without fetching unrelated historical records.
- Stable sorting, access-scope checks, artifact hydration, public cursors, totals and partial-result semantics remain unchanged.
- No rebuild, schema, API or UI change is included.

## Steps

1. Add an adapter regression test proving `Limit: 20` emits OpenSearch `size: 21`.
2. Run it red against the current `limit * 20 + 1` behaviour.
3. Replace the multiplier with the documented bounded look-ahead rule.
4. Run the focused adapter and summary-service suites, then the module test suite.
5. Build the affected service image before committing and opening one PR. Deployment verification remains a separate EE-image operation because this change is in the community Trace Core image.

## Out of scope / follow-up

- Cursor/search-after summary materialization.
- Batched canonical MariaDB enrichment.
- Source timing metrics.

Those are separate Issue #1010 increments because they require new projection contracts and cannot safely be mixed with the receipt-query bound correction.
