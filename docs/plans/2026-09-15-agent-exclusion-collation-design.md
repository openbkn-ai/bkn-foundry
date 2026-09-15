# Agent exclusion collation fix

Issue: [#1581](https://github.com/openbkn-ai/bkn-foundry/issues/1581)

## Goal

Keep summary-only conversation pagination working when the internal Agent exclusion value is a non-ASCII display name, while preserving exact `ascii_bin` matching for application and subject IDs.

## Design

The conversation summary predicate currently binds one exclusion list to `agent_name`, `application_principal_id`, and `effective_subject_id`. Because the latter two columns use ASCII collation while `agent_name` uses the table's UTF-8 collation, a Chinese exclusion value causes MariaDB error 1267.

Normalize and deduplicate the input as today, then split it into:

- all values for `agent_name`;
- ASCII-only values for the two ASCII identity columns.

Omit the identity predicates when the ASCII subset is empty. This avoids cross-collation comparisons, preserves column-level comparison and case semantics, and keeps the query sargable without applying `CONVERT()` to indexed columns.

## Verification

Add focused predicate tests for Chinese-only, ASCII-only, mixed, duplicate, blank, and case-sensitive values. Add a MariaDB integration regression using the production column character sets to verify both `COUNT(*)` and page selection succeed and remain consistent.
