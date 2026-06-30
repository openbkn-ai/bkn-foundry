# Team R&D Workflow

[中文](WORKFLOW.zh.md) | English

This document defines BKN Foundry's team R&D collaboration standards, covering the **boundary between humans and Agents**, Issue management, Feature tracking, design documentation, and team notification processes. Every rule includes concrete steps and file paths so both humans and Agents can execute them.

---

## 📋 Table of Contents

- [Human + Agent Collaboration Model](#-human--agent-collaboration-model)
- [Issue Management](#-issue-management)
- [Agent Workflow](#-agent-workflow)
- [Feature Tracking: Issue → Branch → Design Doc](#-feature-tracking-issue--branch--design-doc)
- [Design Document Specification](#-design-document-specification)
- [PR and Merge Process](#-pr-and-merge-process)
- [Email Notification Process](#-email-notification-process)

---

## 🤝 Human + Agent Collaboration Model

This workflow applies to both **humans** and **Agents** (Claude Code / Codex, connected via the GitHub MCP or the `gh` CLI). Both share the same Issue / branch / PR rules; they differ only in their boundaries.

### Five Principles

1. **All collaboration happens on GitHub.** Requirements, design discussion, code, PRs, reviews, CI, board, docs — all on GitHub. Conclusions reached offline / in chat must be posted back to the relevant Issue / PR to count.
2. **Whatever can be delegated to an Agent, delegate it.** Let Agents do the work by default; humans spend their attention on judgment and gatekeeping.
3. **Risky / hard-to-revert operations may be done by an Agent, but only after human confirmation** (the confirmation gate; see [Agent Workflow](#-agent-workflow)).
4. **Two hard gates before merge: CI green + human review approval**, enforced by GitHub Branch Protection (see [PR and Merge Process](#-pr-and-merge-process)).
5. **Humans are accountable; the Agent is an amplifier.** When something breaks, it traces back to a person (the module Owner).

### Module Owners and Auto-Routing

Every service / module has an Owner in [`.github/CODEOWNERS`](../.github/CODEOWNERS). Two layers of automation are built on it:

- **Issue auto-assignment**: an `issues.labeled` Action maps "service label → Owner" and sets the Assignee automatically (see [Issue Triage Rules](#issue-triage-rules)).
- **PR auto-review**: native CODEOWNERS — a PR touching a module path automatically requests that Owner's review; with Branch Protection "Require review from Code Owners", the Owner must approve.
- **Backup reviewer (avoid a single point)**: each module should list **≥2 Owners** (primary + backup) in CODEOWNERS, with required approvals = 1 (any Owner), so a flood of Agent PRs doesn't serialize on one person.

**No central triage rotation**: each Owner triages the Issues in their own module.

---

## 🗂 Issue Management

### Issue Types and Labels

| Type | Label | Design Doc Required | Description |
| --- | --- | --- | --- |
| Bug Report | `type: bug` | No (optional for complex bugs) | Functional defects or unexpected behavior |
| Feature Request | `type: feature` | **Required** | New features or enhancements |
| Task | `type: task` | Recommended | Engineering tasks, research, refactoring |
| Documentation | `type: docs` | No | Missing or improvable documentation |

### Priority Labels

| Priority | Label | Response SLA |
| --- | --- | --- |
| Critical | `priority: critical` | Respond within 24 hours; must complete in current Sprint |
| High | `priority: high` | Must complete in current Sprint |
| Medium | `priority: medium` | Planned for next 1–2 Sprints |
| Low | `priority: low` | Future backlog; no fixed deadline |

### Collaboration Labels (Human / Agent)

| Label | Purpose |
| --- | --- |
| `agent-ready` | Acceptance criteria complete + `ac-approved` + independently doable — can be handed to an Agent |
| `ac-approved` | Acceptance criteria approved by a human (prerequisite for `agent-ready`) |
| `needs-human` | Returned after an Agent got stuck / went off-track; needs a human |
| `awaiting-confirmation` | Agent posted a risky-operation plan; waiting for Owner confirmation |
| `owner-confirmed` | Owner confirmed the risky operation; OK to execute (Owner-only) |
| `by-agent` | PR / Issue produced by an Agent; used for metrics |

> Service / module labels (e.g. `vega`, `bkn-safe`, `context-loader`) drive CODEOWNERS auto-routing to the Owner (see [`route-issue.yml`](../.github/workflows/route-issue.yml)).

### Issue Lifecycle

```text
Open → Triaged → In Progress → In Review → Done
         │                                    │
         └─────────── Closed (Won't Fix) ─────┘
```

| State | Action |
| --- | --- |
| `Open` | Created; awaiting triage |
| `Triaged` | Priority, Assignee, and Milestone assigned |
| `In Progress` | Branch and design doc created; development started (**update tracking comment in Issue**, see next section) |
| `In Review` | PR submitted; awaiting Code Review |
| `Done` | PR merged; Issue closed automatically or manually |
| `Closed (Won't Fix)` | Decided not to implement; reason stated in a comment |

### Issue Triage Rules

- **Auto-routing**: once a new Issue gets a service label, an Action (`.github/workflows/route-issue.yml`) assigns it to the module Owner per CODEOWNERS, so nothing falls through the cracks.
- **Distributed triage (no rotation)**: each module Owner triages their own module's Issues within **2 business days** of creation (set Priority, Milestone; decide to do it themselves / label `agent-ready` / leave it up for grabs).
- **Self-serve pickup**: members self-assign from Triaged, unassigned Issues (own module first); moving to In Progress = lock.
- Cross-module Issues must `@mention` the relevant module Owner with the expected outcome.
- Issues with no progress for 30+ days must be re-triaged or closed.

---

## 🤖 Agent Workflow

### When to Hand to an Agent

**Acceptance-criteria flip**: an Agent is encouraged to read the Issue first and *draft* the acceptance criteria + test plan as a comment; the Owner reviews and applies `ac-approved`. This moves the human from author to approver — higher throughput.

Label `agent-ready` only when all hold: ① acceptance criteria are complete (including test requirements); ② they are `ac-approved` (human-approved); ③ the task is independently doable. Issues without acceptance criteria, or not yet approved, cannot be handed to an Agent.

### Agent Loop

```text
1. Find Issues with label=agent-ready, state=Triaged & available, no Assignee
2. Claim: set Assignee (lock) → move to In Progress → comment "starting"
3. Read acceptance criteria → create branch via the Issue's "Create a branch" → write code + run/add tests
4. Open PR (body includes Closes #issue) → auto-moves to In Review
5. On a risky operation: post the "three-part confirmation" and wait for human confirmation before executing
6. Stop. Wait for CI green + module Owner approval to merge; if CI fails, fix it first
```

The Agent must post a comment in the Issue at these points so humans can audit asynchronously: claiming work, opening the PR, requesting confirmation, returning when stuck.

### Confirmation Gate: the Three-Part Confirmation

For any operation with side effects / hard to revert / affecting production (deploy, delete or modify data, schema migration, prod config, secrets & permissions, major dependency bumps, cross-service breaking changes), the Agent does **not** execute directly. It first posts three things in the Issue:

1. **What it will do** — the exact command / diff
2. **Blast radius** — which data / environments / services are affected
3. **Rollback** — how to undo if it goes wrong

**Structured approval (not a casual "ok")**: after posting the three parts, the Agent applies `awaiting-confirmation`; **only a module Owner** may replace it with `owner-confirmed`, which is the go-ahead. The Agent executes only once `owner-confirmed` is present; otherwise it does nothing.

**Deploys use a stronger native gate**: put deploy workflows behind a GitHub **Environment with required reviewers = Owners**. When an Agent triggers a deploy, GitHub pauses for human approval — auditable, and not dependent on parsing text.

### Agents Never

- approve / merge a PR (code review must be human)
- bypass / skip CI
- execute the risky operations above without confirmation

### Returning When Stuck

Agent stuck / off-track / can't get tests passing → comment the blocker → move back to Triaged → clear its own Assignee → label `needs-human`; the module Owner reschedules.

### Connecting

GitHub MCP or the `gh` CLI both work; no mandated tool. Only one Agent may take a given Issue at a time (locked via Assignee).

---

## 🔗 Feature Tracking: Issue → Branch → Design Doc

This is the core of this specification. Every Feature (`type: feature`) must follow the complete steps below from Issue creation to code merge. The **Issue tracking comment** is the single source of truth that links all three artifacts together.

### Complete Workflow

```text
1. Create Issue
      │
      ▼
2. Triage: assign Assignee, Priority, Milestone
      │
      ▼
3. Create design doc  →  docs/design/{module}/features/{issue-id}-{desc}.md
      │
      ▼
4. Create branch  →  feature/{issue-id}-{desc}
      │
      ▼
5. Post tracking comment in Issue (branch + design doc link)
      │
      ▼
6. Develop + update design doc
      │
      ▼
7. Submit PR (linked to Issue and design doc)
      │
      ▼
8. Code Review (includes doc review)
      │
      ▼
9. Merge → update design doc status to "implemented"
```

### Step 3: Create the Design Document

**File path rule:**

```
docs/design/{module}/features/{issue-id}-{short-desc}.md
```

| Placeholder | Description | Example |
| --- | --- | --- |
| `{module}` | Module name, matching the code directory | `auth`, `knowledge-graph`, `data-agent` |
| `{issue-id}` | GitHub Issue number (without `#`) | `123` |
| `{short-desc}` | Kebab-case summary of the Issue title; 5 words max | `add-oauth-support` |

**Examples:**

```
docs/design/auth/features/123-add-oauth-support.md
docs/design/knowledge-graph/features/456-batch-import-nodes.md
docs/design/data-agent/features/789-streaming-response.md
```

**Directory structure:**

```
docs/
└── design/
    ├── auth/
    │   └── features/
    │       └── 123-add-oauth-support.md
    ├── knowledge-graph/
    │   └── features/
    │       └── 456-batch-import-nodes.md
    └── data-agent/
        ├── features/
        │   └── 789-streaming-response.md
        └── adr/                          ← Architecture Decision Records
            └── 0001-use-opensearch.md
```

### Step 4: Create the Branch

**Branch naming rule:**

```
feature/{issue-id}-{short-desc}
```

The `{issue-id}` and `{short-desc}` in the branch name **must match** the design document filename exactly:

```bash
# Examples
git checkout -b feature/123-add-oauth-support
git checkout -b feature/456-batch-import-nodes
```

### Step 5: Post Tracking Comment in the Issue

After starting development, post one tracking comment in the Issue. Update this same comment as the work progresses — do not create multiple tracking comments.

```markdown
## 📌 Development Tracking

| Item | Value |
| --- | --- |
| **Branch** | `feature/123-add-oauth-support` |
| **Design Doc** | [docs/design/auth/features/123-add-oauth-support.md](../docs/design/auth/features/123-add-oauth-support.md) |
| **Status** | In Progress |
| **Assignee** | @username |
| **ETA** | YYYY-MM-DD |
```

> **Note**: After the PR is merged, update this comment's Status to `Done` and add the PR link.

---

## 📄 Design Document Specification

### Document Frontmatter (Metadata Header)

Every design document must begin with a YAML frontmatter block recording key metadata:

```markdown
---
issue: "#123"
branch: "feature/123-add-oauth-support"
module: "auth"
status: "draft"          # draft | in-review | approved | implemented
author: "@username"
created: "2026-03-16"
pr: ""                   # fill in after PR is merged, e.g. "#456"
---
```

| Field | Required | Description |
| --- | --- | --- |
| `issue` | Yes | Linked GitHub Issue number |
| `branch` | Yes | Corresponding development branch |
| `module` | Yes | Module this feature belongs to |
| `status` | Yes | Document/feature status; see values below |
| `author` | Yes | Primary assignee |
| `created` | Yes | Document creation date |
| `pr` | No | Fill in after PR is merged |

**Status values:**

| Value | Description |
| --- | --- |
| `draft` | Design in progress; not yet reviewed |
| `in-review` | PR submitted; under review |
| `approved` | Review passed; ready to implement |
| `implemented` | Merged; feature is live |

### Design Document Template

````markdown
---
issue: "#{issue-id}"
branch: "feature/{issue-id}-{short-desc}"
module: "{module}"
status: "draft"
author: "@username"
created: "YYYY-MM-DD"
pr: ""
---

# Feature #{issue-id}: {Feature Title}

## Background and Goals

Describe the feature background, user pain points, and the goals of this development effort.

## Design

### Summary

Brief overview of the overall approach.

### Interaction Design (if applicable)

For features involving UI / frontend interaction, a **link to the design mockup (Figma / etc.) is required**, with a brief description of the key interactions.

- Mockup: <link>

### API Changes (if any)

```http
POST /api/v1/{endpoint}
Content-Type: application/json

{
  "field": "value"
}
```

Response:

```json
{
  "id": "xxx",
  "status": "ok"
}
```

### Database Changes (if any)

Describe new or modified tables/columns and provide the migration script path.

### Key Flow

Describe the core logic flow in prose or pseudocode.

## Acceptance Criteria

- [ ] Functional criterion 1
- [ ] Functional criterion 2
- [ ] Test coverage meets requirements

## Test Strategy

Describe the unit tests, integration tests, or AT cases to be added.

## Impact Analysis

- **Backward compatibility**: Yes/No — explain
- **Dependency changes**: Yes/No — explain
- **Performance impact**: explain (omit if none)

## References

- Related Issue/PR links
- Related documentation links
````

### Bug Analysis Document (Optional)

For complex bugs involving multiple modules or requiring root cause analysis, an analysis document can be created at:

```
docs/design/{module}/bugs/{issue-id}-{short-desc}.md
```

Frontmatter uses the same format; the body should include: problem description, root cause analysis, fix approach, and verification steps.

### ADR (Architecture Decision Record)

For Features involving significant design decisions, the decision should be documented in the design doc and optionally archived as an ADR:

**Path:** `docs/design/{module}/adr/NNNN-{short-title}.md`

**File naming:** Sequence starts at `0001`, counted independently per module.

```markdown
---
number: "0001"
module: "{module}"
status: "accepted"       # proposed | accepted | deprecated | superseded
date: "YYYY-MM-DD"
related-issue: "#123"
---

# ADR-{module}-0001: {Decision Title}

## Context

Describe the background and constraints driving this decision.

## Decision

We will adopt ___.

## Rationale

- Reason 1
- Reason 2

## Consequences

**Positive:**
- ...

**Negative (trade-offs):**
- ...

## Alternatives Considered

Describe alternatives that were considered but not chosen, and why.
```

---

## 🔀 PR and Merge Process

### Merge Gates (Enforced by Branch Protection)

Merging to `main` requires both hard gates, physically enforced by GitHub Branch Protection / Rulesets — not left to good intentions:

1. **CI green** — required status checks pass (GitHub Actions runs the tests).
2. **Human approval** — at least one approval from someone other than the author, with **Require review from Code Owners** enabled, so the module Owner must approve.

Agents cannot approve, merge, or bypass CI. Recommended `main` configuration:

- Require a pull request before merging
- Require status checks to pass (check the CI job)
- Require approvals ≥ 1 + Require review from someone other than the author + Require review from Code Owners
- Require branches to be up to date before merging
- **Bypass list**: module Owners / maintainers are allowed to bypass (Branch Protection bypass actors / Ruleset bypass list)

> **Owners have bypass permission**: in emergencies or trusted cases, an Owner may push directly / merge past the gates — a trust channel, to be used sparingly and explained afterward in the relevant Issue / PR. **Agents are never on the bypass list**; the two gates always apply to Agents and regular contributors.

### PR Description Template

The PR description must include the following to complete the three-way link: Issue → Branch → Design Doc.

```markdown
## Description

Brief summary of what changed and why.

## Links

| Item | Value |
| --- | --- |
| **Issue** | Closes #123 |
| **Design Doc** | [docs/design/auth/features/123-add-oauth-support.md](../docs/design/auth/features/123-add-oauth-support.md) |
| **Branch** | `feature/123-add-oauth-support` |

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Documentation update
- [ ] Refactoring

## Testing

Describe how to verify this change (test commands, manual steps, etc.).

## Pre-Merge Checklist

- [ ] Design doc updated (status changed to `in-review`; `pr` field filled in)
- [ ] CHANGELOG.md updated (under `[Unreleased]` section)
- [ ] API documentation updated (if API changed)
- [ ] Tests added/updated; all pass locally
- [ ] No breaking changes, or breaking changes are noted in CHANGELOG
```

### Documentation Checks in Code Review

Reviewers must confirm:

- [ ] For UI / interaction changes: the design mockup link is attached and the implementation matches it
- [ ] Design doc is consistent with the actual implementation
- [ ] API documentation is up to date
- [ ] CHANGELOG.md records user-facing changes
- [ ] Design doc frontmatter: `pr` field is filled in and `status` is `in-review`

### Post-Merge Actions

After the PR is merged, the merger or Assignee must:

1. Update the design doc's `status` to `implemented`
2. Update the Issue tracking comment: set Status to `Done` and add the PR link

---

## 📧 Email Notification Process

### Notification Triggers

| Event | Recipients | Triggered By |
| --- | --- | --- |
| Official Release | All team members + external subscribers | CI automation / manual |
| RC Release | Internal testing team | CI automation / manual |
| Code freeze | All engineering members | Manual (Release Manager) |
| Critical bug fix | Affected module owners + testing team | Manual |
| Sprint start | Sprint participants | Manual (project lead) |

### Email Templates

#### Release Announcement

```
Subject: [BKN Foundry] vX.Y.Z Official Release

BKN Foundry vX.Y.Z has been officially released!

## Key Changes

### ✨ Added
- Feature description (#IssueNumber, design doc link)

### 🐛 Fixed
- Fix description (#IssueNumber)

### ⚠️ Breaking Changes (if any)
- Describe the change and migration path

## Download
- GitHub Releases: <link>
- Docker: docker pull kweaver/kweaver:vX.Y.Z

## Full Changelog
<CHANGELOG link>

Thank you to all contributors!
```

#### RC Release Notification

```
Subject: [BKN Foundry] vX.Y.Z-rc.N Test Release — Feedback Requested

BKN Foundry vX.Y.Z-rc.N has been released. Please validate and provide feedback.

## Test Scope
- Change list (with design doc links)

## Known Issues
- (if any)

## How to Submit Feedback
Open a GitHub Issue and add the label: milestone: vX.Y.Z

## Feedback Deadline
YYYY-MM-DD
```

#### Code Freeze Notification

```
Subject: [BKN Foundry] vX.Y.Z Code Freeze Notice

The vX.Y.Z release branch has been created. Code freeze begins YYYY-MM-DD.

## Rules During Freeze
- ✅ Allowed: Bug fixes, documentation updates, version bumps
- ❌ Not allowed: New features, refactoring, dependency upgrades

## Requesting an Exception
Reply to this email with justification. Approval required from Release Manager.

## Target Release Date
YYYY-MM-DD
```

### Sending Guidelines

| Item | Requirement |
| --- | --- |
| Delivery method | Team mailing list; major Releases may use CI automation |
| Subject prefix | Always use `[BKN Foundry]` for easy filtering and archiving |
| Language | English; Chinese version may be added for domestic team |
| CC | **All emails must CC the QA lead**; Release announcements must also CC the project lead |
| Attachments | No attachments; use links to reference documents or artifacts |

---

## 📚 Related Resources

- [Contributing Guidelines](CONTRIBUTING.md)
- [Release Guidelines](RELEASE.md)
- [Testing Guidelines](TESTING.md)
- [Architecture Guidelines](ARCHITECTURE.md)

---

*Last updated: 2026-06-30*
