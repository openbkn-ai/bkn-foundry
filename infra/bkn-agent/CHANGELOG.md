# Changelog

## [Unreleased]

### Fixed

- Select the official claim-attribution prompt language consistently for shared configuration and execution, while preserving custom prompts and canonical exports (#2341). Apply the prompt-locales schema migration before updating the service; the normal migrator runs `0.2.1/10-claim-prompt-locales.sql`.
- Commit administrator Agent configuration and version history atomically; serialize version activation and deletion and refresh cached state after acquiring locks (related to #2341).
- Keep import and administrator Prompt writes in Agent → Prompt lock order, and run the MariaDB concurrency regressions in CI.
- Scope current-read configuration locks to a Prompt binding index (`0.2.1/11-agent-prompt-index.sql`), preserving concurrent binding history and retrying administrator edits that race with rebinding.
