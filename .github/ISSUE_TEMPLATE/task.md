---
name: Task
about: Standard unit of work — an independently deliverable, verifiable change for a human or an Agent
title: ''
labels: ''
assignees: ''

---

## Goal
<!-- One sentence: what needs to be done -->

## Background / Context
<!-- Why; related file paths, related commits / issues (#number); known gotchas -->

## Acceptance Criteria (Definition of Done)
<!-- Rule: no acceptance criteria = cannot be labeled `agent-ready` -->
- [ ] Specific, verifiable condition 1
- [ ] Tests: new feature ships with tests / bug fix ships with a regression test
- [ ] CI green (where the module has PR tests wired)
- [ ] For UI / interaction work: design mockup link attached (Figma / etc.)
- [ ] Deployment / verification steps (if needed)

## Boundaries
<!-- What NOT to touch; which steps are risky operations (deploy / delete or modify data /
     schema migration / secrets & permissions / major dependency bumps) that require
     human confirmation before an Agent executes them -->

## Module / Owner
<!-- Owning service, for CODEOWNERS routing + auto-assign to the Owner:
     bkn-safe / adp-bkn / adp-vega / adp-context-loader / adp-execution-factory /
     decision-agent / infra-mf-model-api / infra-mf-model-manager / infra-oss-gateway /
     infra-sandbox / trace-ai / deploy -->

<!--
Hand to an Agent when: acceptance criteria are complete AND the task is independently doable
  → add label `agent-ready`.
Agent stuck / off-track → label `needs-human`; the module Owner reschedules.
-->
