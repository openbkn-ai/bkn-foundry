# Conversation Initial Intent Preview Design

## Problem

Conversation summaries currently skip the earliest interaction when it has a
question but no result. They may therefore display a later continuation prompt,
or combine a question and result from different interactions through the
conversation-wide fallback.

## Contract

- Group request facts by interaction.
- Select the chronologically earliest interaction whose aggregated question is
  non-empty; use interaction ID as the deterministic timestamp tie-breaker.
- Return both previews from that same interaction. Its result may be empty.
- If no interaction has a question, return empty question and result previews.
- If the whole conversation predates interaction IDs, retain its legacy
  request-level preview. A mixed conversation still uses only identified
  interactions, so it cannot combine artifacts across interactions.

## Scope

Change only the Foundry conversation-summary selection helper and its tests. Do
not change APIs, persistence, lifecycle state, pagination, EE routes, or UI.
