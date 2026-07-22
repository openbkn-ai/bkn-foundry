# AGENTS.md

Entry point for AI agents (Claude Code / Codex / others) working in this repo.
Read this first, then load the rules under [`rules/`](rules/). Before working in any subdirectory, locate and read every `AGENTS.md` from the repository root through that target directory; rules in the more specific (deeper) file take precedence.

## Read before doing anything

| Topic | File |
| --- | --- |
| How we collaborate (humans + Agents) | [rules/WORKFLOW.md](rules/WORKFLOW.md) |
| Contribution guide (branches, commits, style) | [rules/CONTRIBUTING.md](rules/CONTRIBUTING.md) |
| Architecture & module boundaries | [rules/ARCHITECTURE.md](rules/ARCHITECTURE.md) |
| API / HTTP / error conventions | [rules/DEVELOPMENT.md](rules/DEVELOPMENT.md) |
| Testing conventions | [rules/TESTING.md](rules/TESTING.md) |
| Module owners (review routing) | [.github/CODEOWNERS](.github/CODEOWNERS) |
| Issue templates (bug / feature / task) | [.github/ISSUE_TEMPLATE/](.github/ISSUE_TEMPLATE/) |
| Pull request template | [.github/pull_request_template.md](.github/pull_request_template.md) |

## Hard rules for Agents

- **Review before external writes**: Unless the requester explicitly directs otherwise, after making and verifying code changes, present the working-tree diff for review first. Do **not** commit, push, create or update a PR, or post Issue/PR comments before the requester approves.
- **Language**: Communicate with users in Chinese by default. Use another language only when the requester explicitly asks for it or the artifact itself requires it.
- **Clarify material ambiguity**: Before editing, ask for direction when an ambiguity would materially change scope, behavior, risk, or the intended solution; otherwise proceed with a stated, reasonable assumption.
- **Bug regression coverage**: For bug fixes, add or update a focused regression test that demonstrates the reported failure whenever it is practical.
- **Verification handoff**: After implementing a change, report relevant edge cases and any remaining test-coverage gaps along with the commands run.
- **Issue / PR templates**: Before creating an Issue, select the matching template under [`.github/ISSUE_TEMPLATE/`](.github/ISSUE_TEMPLATE/) and preserve its structure when filling in all applicable sections. Before creating or updating a PR, read and fully complete [`.github/pull_request_template.md`](.github/pull_request_template.md). Do not delete required sections; mark non-applicable items explicitly with a brief reason.
- **Only pick up Issues labeled `agent-ready`** (acceptance criteria complete + independently doable) that are unassigned. Self-assign to lock.
- **Acceptance criteria**: a human approves them (label `ac-approved`) before an Issue becomes `agent-ready`. You may *draft* them for human approval.
- **Risky operations** (deploy, delete/modify data, schema migration, prod config, secrets/permissions, major dependency bumps, cross-service breaking changes): do **not** execute. Post the three-part confirmation (what / blast radius / rollback), apply label `awaiting-confirmation`, and wait for an Owner to apply `owner-confirmed`.
- **You may never**: approve or merge a PR, bypass or skip CI, or act without the confirmation above. Code review and the merge gates are human-only.
- **Open PRs with `Closes #<issue>`** and write back progress as Issue/PR comments. Label your PRs `by-agent`.
- **Stuck / off-track / tests won't pass** → comment the blocker, return the Issue to triage, clear your assignee, label `needs-human`.

## Commits & branches

- Conventional Commits: `type(scope): subject` (`feat` / `fix` / `chore` / `refactor` / `docs` / `test`; scope = service name).
- Branch from the Issue's "Create a branch"; one PR per Issue, kept small.
