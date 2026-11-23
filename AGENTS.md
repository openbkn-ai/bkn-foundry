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
| Logs and Trace review | [rules/OBSERVABILITY_REVIEW.md](rules/OBSERVABILITY_REVIEW.md) |
| Module owners (review routing) | [.github/CODEOWNERS](.github/CODEOWNERS) |
| Issue templates (bug / feature / task) | [.github/ISSUE_TEMPLATE/](.github/ISSUE_TEMPLATE/) |
| Pull request template | [.github/pull_request_template.md](.github/pull_request_template.md) |

## Hard rules for Agents

- **Review before external writes**: Unless the requester explicitly directs otherwise, after making and verifying code changes, present the working-tree diff for review first. Do **not** commit, push, create or update a PR, or post Issue/PR comments before the requester approves.
- **Review feedback requires fresh write approval**: When addressing Issue or PR review feedback, limit work to local edits, verification, and a diff until the requester explicitly authorizes a commit or push. Earlier authorization does not carry over to later review revisions.
- **Language**: Communicate with users in Chinese by default. Use another language only when the requester explicitly asks for it or the artifact itself requires it.
- **Clarify material ambiguity**: Before editing, ask for direction when an ambiguity would materially change scope, behavior, risk, or the intended solution; otherwise proceed with a stated, reasonable assumption.
- **Bug regression coverage**: For bug fixes, add or update a focused regression test that demonstrates the reported failure whenever it is practical.
- **Database locks**: Use non-locking reads by default. Before adding `FOR UPDATE` or another locking read, check whether a conditional update, unique constraint, or existing concurrency control suffices. When a lock is necessary, limit it to the transaction that needs it, explain the race, lock scope, acquisition order, and release point, and verify the concurrent behavior.
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
- Branch names must use a valid type prefix and at most two path segments after it: `<type>/<description>`, `<type>/<issue-number>-<description>`, or `<type>/<module>/<description>`; segments start with lowercase letters or digits and may contain `-`, `.`, or `_`.

## Database migration versions

- The repository-root `VERSION` identifies the next version to be released. Use its value for database initialization and upgrade scripts unless the requester explicitly specifies another migration version upfront. Changing `VERSION` requires explicit authorization.
- Update that version's complete `init.sql` and append the next available numbered upgrade script.
- Review compatibility for clean installs and upgrades from earlier supported releases. Include environments deployed ahead of the next release only when the requester explicitly asks for that compatibility.
- Check the existing migrator's checkpoint selection for the upgrade paths in scope and report unsupported checkpoints on those paths. Changing the migration executor requires separate authorization.
- Do not add or run tests for SQL migration files unless the user explicitly requests them; review the SQL directly and report any unverified execution behavior.
