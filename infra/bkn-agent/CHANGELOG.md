# Changelog

## [Unreleased]

### Added

- Public executions accept an optional `kn_version` (`running`, `draft` or a version number) and `draft_revision` in `context`. Both are sent to Context Loader as `X-Kn-Version` / `X-Kn-Draft-Revision`, pinned into scoped tool calls like `kn_id` (a different model-supplied value returns `ToolScopeMismatch`), and stored with the execution. `draft_revision` without `kn_version=draft` returns 422. Requests without them are stored and run as before, and their scoped tool calls read the running version: a model-supplied `draft`, version number, `draft_revision` or `branch=draft` returns `ToolScopeMismatch` (#1229).

- Add the quality-evaluation assistant to the built-in Agent catalog, preserve administrator changes when seeding it, and carry an explicit Eval purpose through public execution and request deduplication (#2570).

- `DELETE /api/bkn-agent/v1/threads/{thread_id}` deletes a thread's checkpoints and ownership row for its owner (404 for anyone else, 409 while a turn runs); task and execution records are kept.

### Fixed

- Resource semantic understanding v7 (`0.2.2/07-semantic-drop-api-name.sql`) no longer asks for an English short name (`api_name`): the official v6 without its api_name rules. Only an untouched official v6 is upgraded; customized prompts, history and runtime settings are kept, and fresh installs start at v7. Apply it before the vega release that reverts its api_name support (#3017).
- Stop requiring the Agent's `use` permission on public executions and Agent settings: the public surface runs built-in Agents only, so access follows the knowledge network's authorization alone (`view_detail` to run or read, `modify` to change a network's setting). Users with network access no longer get `BknAgent.Execution.Forbidden` for lacking an Agent grant that role administration could not assign.

- Remove the business-provenance directory-account bootstrap, Secret dependency, and credential exception; reject the retired identity while retaining SQL defaults, caller identity, and shared configuration history (#2823).

- Backfill the exact historical Object Metric Designer v1 Chinese Prompt with a protected en-US translation, without replacing canonical content, administrator translations, or version pointers (#2784).
- Select the official claim-attribution prompt language consistently for shared configuration and execution, while preserving custom prompts and canonical exports (#2341). Apply the prompt-locales schema migration before updating the service; the normal migrator runs `0.2.1/10-claim-prompt-locales.sql`.
- Commit administrator Agent configuration and version history atomically; serialize version activation and deletion and refresh cached state after acquiring locks (related to #2341).
- Keep import and administrator Prompt writes in Agent → Prompt lock order, and run the MariaDB concurrency regressions in CI.
- Scope current-read configuration locks to a Prompt binding index (`0.2.1/11-agent-prompt-index.sql`), preserving concurrent binding history and retrying administrator edits that race with rebinding.
