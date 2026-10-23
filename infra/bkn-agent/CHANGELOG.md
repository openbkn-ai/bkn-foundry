# Changelog

## [Unreleased]

### Fixed

- Select the official claim-attribution prompt language consistently for shared configuration and execution, while preserving custom prompts and canonical exports (#2341). Apply the prompt-locales schema migration before updating the service; the normal migrator upgrades existing 0.2.1 installations to component schema 0.2.2.
- Commit administrator Agent configuration and version history atomically; serialize version activation and deletion and refresh cached state after acquiring locks (related to #2341).
- Keep import and administrator Prompt writes in Agent → Prompt lock order, and run the MariaDB concurrency regressions in CI.
- Scope current-read configuration locks to a Prompt binding index (component schema 0.2.3), preserving concurrent binding history and retrying administrator edits that race with rebinding.
