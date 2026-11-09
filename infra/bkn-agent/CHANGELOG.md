# Changelog

## [Unreleased]

### Added

- `DELETE /api/bkn-agent/v1/threads/{thread_id}` deletes a thread's checkpoints and ownership row for its owner (404 for anyone else, 409 while a turn runs); task and execution records are kept.

### Fixed

- Resource semantic understanding v7 (`0.2.2/06-semantic-drop-api-name.sql`) no longer asks for an English short name (`api_name`): the official v6 without its api_name rules. Only an untouched official v6 is upgraded; customized prompts, history and runtime settings are kept, and fresh installs start at v7. Apply it before the vega release that reverts its api_name support (#3017).
- Remove the business-provenance directory-account bootstrap, Secret dependency, and credential exception; reject the retired identity while retaining SQL defaults, caller identity, and shared configuration history (#2823).

- Backfill the exact historical Object Metric Designer v1 Chinese Prompt with a protected en-US translation, without replacing canonical content, administrator translations, or version pointers (#2784).
- Select the official claim-attribution prompt language consistently for shared configuration and execution, while preserving custom prompts and canonical exports (#2341). Apply the prompt-locales schema migration before updating the service; the normal migrator runs `0.2.1/10-claim-prompt-locales.sql`.
- Commit administrator Agent configuration and version history atomically; serialize version activation and deletion and refresh cached state after acquiring locks (related to #2341).
- Keep import and administrator Prompt writes in Agent → Prompt lock order, and run the MariaDB concurrency regressions in CI.
- Scope current-read configuration locks to a Prompt binding index (`0.2.1/11-agent-prompt-index.sql`), preserving concurrent binding history and retrying administrator edits that race with rebinding.
