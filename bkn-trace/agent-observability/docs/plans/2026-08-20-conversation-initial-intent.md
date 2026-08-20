# Conversation Initial Intent Preview Implementation Plan

1. Add a regression test where the first interaction has the user's initial
   question but no result, and a later interaction is complete.
2. Add a regression test proving a result-only later interaction is not paired
   with the first interaction's question.
3. Run the focused tests and confirm the current implementation fails.
4. Select the earliest interaction with a non-empty question and remove the
   conversation-wide fallback.
5. Run focused and full `evidencesvc` package tests, formatting, and diff checks.

