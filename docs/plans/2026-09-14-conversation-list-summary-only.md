# Conversation List Summary-Only Reads Implementation Plan

> **For implementation:** execute in order, with the focused test suite after each task.

**Goal:** Keep the business-provenance conversation list independent of full Trace projection size while preserving its lifecycle-derived list semantics and its first-turn preview rule.

**Architecture:** The list already obtains its page, managed interactions, status, evidence completeness, duration, and round count from the canonical session store. Replace its final full execution-projection read with a narrow read for the first managed interaction in each selected conversation: batch-read its existing Core receipts for the unchanged visibility boundary, then read only `Question` and `Result` artifacts. A small value-object mapper derives previews. Detail, Markdown, and analysis paths retain the existing full projection reader.

**Technology:** Go, domain ports, MariaDB session store, OpenSearch projection adapter, existing Go tests.

## Scope and invariants

- No HTTP contract, database schema, UI structure, cache, scanner, background job, or second projection.
- List content comes only from canonical lifecycle facts, first-turn receipt authorization, and first-turn `Question`/`Result` artifacts.
- If the first turn lacks either artifact, leave that preview blank; never borrow a later turn.
- The list does not load Trace, Operation, or arbitrary artifact content.
- Detail, raw Trace Markdown, and analysis retain their current on-demand reads.
- Enterprise internal-agent exclusions are an identity-page predicate; they do
  not require a full Trace read.

## Task 1: Lock the list read boundary with a failing service test

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary_service_test.go`

1. Extend the existing capturing projection source with separate artifact-only call recording.
2. Update the canonical conversation-page fixture to provide lifecycle state and first-turn terminal artifacts through that artifact-only reader.
3. Assert that the list returns the same first-turn previews and round count.
4. Assert no `LoadExecutionProjection` call is made, and the artifact query contains only first-turn interaction IDs and `Question`/`Result` types.
5. Run `go test ./src/domain/service/evidencesvc -run TestListConversationsLoadsInteractionScopedTerminalArtifactsForPage` and verify it fails because the current list still calls the full reader.

## Task 2: Add the narrow internal artifact-projection capability

**Files:**
- Modify: `bkn-trace/agent-observability/src/port/driven/iprojectionsource/port.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore/projection_source.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore/store.go`
- Modify: `bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection/source.go`
- Modify tests adjacent to the adapters as needed.

1. Define a separate artifact-only source interface returning artifact results and truncation state; leave `ProjectionSourcePort` unchanged for all detail/full-projection callers.
2. Implement it in the OpenSearch artifact store by reusing the existing bounded artifact projection query without first reading evidence documents.
3. Implement it in the in-memory store by filtering only artifacts, without calling `ListEvidence`.
4. Implement the narrow Core wrapper so it first authorizes selected interactions through their existing receipts.
5. Add focused adapter tests proving the narrow call omits evidence-document reads and retains scope, interaction ID, type, and limit filters.

## Task 3: Map terminal artifacts and switch only the canonical list path

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/valueobject/evidencevo/summary.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary.go`
- Modify: `bkn-trace/agent-observability/src/domain/service/evidencesvc/summary_service_test.go`

1. Add a deterministic value-object helper that maps interaction-scoped terminal artifacts to first-question/latest-result previews, using the same ordering rule as the existing summary builder.
2. In `listConversationIdentityPage`, require the narrow artifact source and load only terminal artifacts for the already-selected first interactions.
3. Build list rows from lifecycle state, apply terminal previews only to their matching first interaction, and preserve the blank-on-missing-first-artifact rule.
4. Keep `applyCanonicalConversationState` as the owner of lifecycle-derived status, completeness, duration, identity, and timestamps.
5. Run the focused service tests and verify the new regression test passes.

## Task 4: Verify the targeted change

**Files:**
- Modify: `docs/design/bkn-trace/features/1569-conversation-list-summary.md` (implementation/verification record)

1. Run `go test ./src/domain/service/evidencesvc`.
2. Run adapter package tests for in-memory evidence storage, OpenSearch evidence projection, and OpenSearch Core projection.
3. Run `go test ./...` from `bkn-trace/agent-observability` if dependency availability permits.
4. Run `gofmt -w` only on modified Go files and inspect `git diff --check` plus the final diff.
5. Record commands and outcomes in the design document; do not commit or open a PR until the requester reviews the diff, per repository policy.

## Task 5: Preserve enterprise internal-agent exclusion on the narrow path

**Files:**
- Modify: `src/port/driven/isessionstore/port.go`
- Modify: `src/drivenadapter/dbaccess/mariadb/sessionstore/summary_page.go`
- Modify: `src/domain/service/evidencesvc/summary.go`

1. Pass the existing internal-agent exclusion values to the identity-page query.
2. Apply the predicate consistently to count and page SQL, retaining rows that
   have no agent name.
3. Keep all content-dependent filters on the legacy full-projection path.
