# AGENTS.md

Entry point for AI agents (Claude Code / Codex / others) working in this repo.
Read this first, then load the rules under [`rules/`](rules/).

## Read before doing anything

| Topic | File |
| --- | --- |
| How we collaborate (humans + Agents) | [rules/WORKFLOW.md](rules/WORKFLOW.md) |
| Contribution guide (branches, commits, style) | [rules/CONTRIBUTING.md](rules/CONTRIBUTING.md) |
| Architecture & module boundaries | [rules/ARCHITECTURE.md](rules/ARCHITECTURE.md) |
| API / HTTP / error conventions | [rules/DEVELOPMENT.md](rules/DEVELOPMENT.md) |
| Testing conventions | [rules/TESTING.md](rules/TESTING.md) |
| Module owners (review routing) | [.github/CODEOWNERS](.github/CODEOWNERS) |

## Hard rules for Agents

- **Only pick up Issues labeled `agent-ready`** (acceptance criteria complete + independently doable) that are unassigned. Self-assign to lock.
- **Acceptance criteria**: a human approves them (label `ac-approved`) before an Issue becomes `agent-ready`. You may *draft* them for human approval.
- **Risky operations** (deploy, delete/modify data, schema migration, prod config, secrets/permissions, major dependency bumps, cross-service breaking changes): do **not** execute. Post the three-part confirmation (what / blast radius / rollback), apply label `awaiting-confirmation`, and wait for an Owner to apply `owner-confirmed`.
- **You may never**: approve or merge a PR, bypass or skip CI, or act without the confirmation above. Code review and the merge gates are human-only.
- **Open PRs with `Closes #<issue>`** and write back progress as Issue/PR comments. Label your PRs `by-agent`.
- **Stuck / off-track / tests won't pass** → comment the blocker, return the Issue to triage, clear your assignee, label `needs-human`.

## Commits & branches

- Conventional Commits: `type(scope): subject` (`feat` / `fix` / `chore` / `refactor` / `docs` / `test`; scope = service name).
- Branch from the Issue's "Create a branch"; one PR per Issue, kept small.
