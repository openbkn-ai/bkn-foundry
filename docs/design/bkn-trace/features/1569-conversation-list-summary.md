---
issue: "#1569"
branch: "fix/1569-conversation-list-summary"
module: "bkn-trace"
status: "approved"
author: "@leecky"
created: "2026-09-14"
pr: ""
---

# Feature #1569: Summary-only conversation list reads

## Background and Goals

The Business Provenance conversation list is a navigation surface. It needs a
canonical conversation's first-round question/result, round count, agent,
status, evidence completeness, and duration. The current identity-page path
loads complete Trace and Artifact projections before deriving those fields.
On a local deployment, a 20-row list response of about 16 KB took 4.4–9.0
seconds, while the high-operation detail endpoints completed in about 0.9–1.2
seconds.

The goal is to make the list read only its summary contract. Detail, Markdown,
and analysis reads remain deferred until the user enters a conversation.

## Design

### Summary

For canonical conversations, `ListConversations` will build rows from the
lifecycle store, the selected first interactions' existing Core receipts, and
the first-round Question/Result artifacts only. The receipt read preserves the
existing Core visibility boundary; it does not load Trace projections,
Operations, or arbitrary non-terminal artifacts.

### Public Behavior

The endpoint, query parameters, ordering, pagination, and Studio interaction
stay unchanged. A row continues to show the first canonical round only. If
either first-round artifact is unavailable, its preview remains empty; a later
round must never be used as a substitute. Lifecycle identity (`agent_or_app`
and initiator) is retained. Detail-derived metadata such as knowledge-network
coverage, request/Trace counts, and error summaries is intentionally absent
from the navigation list and remains available from the on-demand detail read.

### Key Flow

1. Page canonical conversation identities from MariaDB.
2. Batch-load canonical conversations and their interactions.
3. Determine each conversation's first interaction by ordinal.
4. Batch-read only the selected interactions' Core receipts to retain the
   existing visibility boundary, then load only their Question and Result
   artifacts.
5. Construct summaries from lifecycle fields plus those two artifact types.
6. Preserve the existing compatibility fallback for non-canonical queries.

### API and Database Changes

None. The existing list API remains backward compatible. No schema migration,
background job, cache, new service, or new durable projection is introduced.

## Acceptance Criteria

- [x] Canonical conversation list reads do not load complete Trace or Operation projections.
- [x] First-round previews, count, agent, status, evidence completeness, and duration keep their existing semantics.
- [x] Missing first-round artifacts are not replaced by later-round previews.
- [x] Enterprise-injected internal-agent exclusions remain applied by the identity query without re-enabling full Trace reads.
- [x] Focused regression tests cover both the reduced read boundary and semantic compatibility.

## Test Strategy

Unit tests use a recording projection source / artifact source to prove that
the list requests only first-round Question and Result artifacts and never
requests full Trace projection data. Existing summary tests cover ordering,
pagination, and first-round preview semantics; they will be kept green.

## Implementation Verification

Implemented in `fix/1569-conversation-list-summary`; ready for PR.

Verified on 2026-09-14:

- `go test ./src/domain/service/evidencesvc ./src/domain/valueobject/evidencevo ./src/drivenadapter/memoryaccess/evidencestore ./src/drivenadapter/httpaccess/opensearchevidencestore ./src/drivenadapter/httpaccess/opensearchcoreprojection`
- `go test ./...` from `bkn-trace/agent-observability`
- `git diff --check`
- authenticated local Studio refresh of the 63-row Business Provenance list: about
  1.93 seconds after deploying the candidate enterprise image

The regression test proves the canonical conversation-list path invokes no
full execution projection. It requests only receipt-authorized `Question` and
`Result` artifacts for selected first managed interactions. The OpenSearch
adapter test proves that narrow call does not read the evidence index.

## Impact Analysis

- **Backward compatibility:** The endpoint, ordering, and Studio-visible list
  behavior are unchanged. Detail-derived optional metadata is omitted from the
  navigation list rather than forcing a full Trace read; detail endpoints
  retain it.
- **Dependency changes:** No.
- **Performance:** List work scales with page-size first-round artifacts rather
  than full Trace payload size and operation count. Details retain their
  existing on-demand path.
- **Enterprise filtering:** internal analysis/claim Agent exclusions are
  translated into the canonical conversation identity query, so those hidden
  conversations remain excluded without forcing an execution projection.

## References

- https://github.com/openbkn-ai/bkn-foundry/issues/1569
- https://github.com/openbkn-ai/openbkn-ee/issues/31
