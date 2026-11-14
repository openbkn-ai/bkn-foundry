# Changelog

## [Unreleased]

### Added

- The graph explorer's `configure_analysis` draws an industry or supply chain (`kind: chain`, one type along a relation from that type to itself in `chainRelByOt`, lanes by `propertyByOt`); a relation that does not link the type to itself is refused. In an analysis, the instructions route industry-chain, supply-chain and upstream/downstream questions to one Cypher query and this chart instead of drawing the chain node batch by node batch.

- Public executions accept an optional `kn_version` (`running`, `draft` or a version number) and `draft_revision` in `context`. Both are sent to Context Loader as `X-Kn-Version` / `X-Kn-Draft-Revision`, pinned into scoped tool calls like `kn_id` (a different model-supplied value returns `ToolScopeMismatch`), and stored with the execution. `draft_revision` without `kn_version=draft` returns 422. Requests without them are stored and run as before, and their scoped tool calls read the running version: a model-supplied `draft`, version number, `draft_revision` or `branch=draft` returns `ToolScopeMismatch` (#1229).

- Add the quality-evaluation assistant to the built-in Agent catalog, preserve administrator changes when seeding it, and carry an explicit Eval purpose through public execution and request deduplication (#2570).

- `DELETE /api/bkn-agent/v1/threads/{thread_id}` deletes a thread's checkpoints and ownership row for its owner (404 for anyone else, 409 while a turn runs); task and execution records are kept.

### Fixed

- A structured reply the OpenAI SDK refuses to parse at the output cap (`LengthFinishReasonError`, raised whenever a `response_format` is sent) is treated as truncated: retried once asking for a shorter answer, then failed as `StructuredOutputError … output truncated`; the reasoning tokens are logged, to tell reasoning from answer (a thinking DeepSeek spent all 16384 output tokens on reasoning). Callers that need the answer to fit turn reasoning off for their task with `x-bkn-agent-thinking-mode: disabled`.
- Structured output on providers without the `json_schema` response format (DeepSeek answers 400 "This response_format type is unavailable now"): the prompt fallback now asks for the `json_object` response format, so the reply is valid JSON, and drops it when the provider refuses that too. Raw control characters inside JSON strings are accepted. A reply cut off at the output cap (`finish_reason: length`) is retried with a request for a shorter answer instead of being echoed back, and the final `StructuredOutputError` says `output truncated`, for callers to ask for less per call.
- In an analysis, the graph explorer's `canvas_control clear` no longer tells the model the user will confirm. The instructions said no confirmation follows, the tool said one did, and the model undid and cleared again in a loop.
- BKN project Ontology Assistant v7 (`0.2.2/09-ontology-ask-content-language-v7.sql`): everything Ask AI writes follows the language the message names (the project's content language), not the prompt translation picked for the caller's UI language: the reply and every name, description, display name and reason of a revision, a candidate or a proposal. One Language paragraph is added to both translations. Only an untouched official v6 is upgraded; customized prompts, history and runtime settings stay. A fresh install starts at v7.
- BKN project requirements v9 (`0.2.2/08-requirements-kinetic-network-coverage-v9.sql`): on a network that already runs, pass kinetic judges the network's items (`kept-network-` ids) by their descriptions, gives no new need for a capability one covers or for a step of what one already computes, and gives a kept need one covers again with `covered_by` that item, which bkn-project links. Only an untouched official v8 is upgraded; customized prompts, history and runtime settings stay. A fresh install starts at v9.
- Resource semantic understanding v7 (`0.2.2/07-semantic-drop-api-name.sql`) no longer asks for an English short name (`api_name`): the official v6 without its api_name rules. Only an untouched official v6 is upgraded; customized prompts, history and runtime settings are kept, and fresh installs start at v7. Apply it before the vega release that reverts its api_name support (#3017).
- Stop requiring the Agent's `use` permission on public executions and Agent settings: the public surface runs built-in Agents only, so access follows the knowledge network's authorization alone (`view_detail` to run or read, `modify` to change a network's setting). Users with network access no longer get `BknAgent.Execution.Forbidden` for lacking an Agent grant that role administration could not assign.

- Remove the business-provenance directory-account bootstrap, Secret dependency, and credential exception; reject the retired identity while retaining SQL defaults, caller identity, and shared configuration history (#2823).

- Backfill the exact historical Object Metric Designer v1 Chinese Prompt with a protected en-US translation, without replacing canonical content, administrator translations, or version pointers (#2784).
- Select the official claim-attribution prompt language consistently for shared configuration and execution, while preserving custom prompts and canonical exports (#2341). Apply the prompt-locales schema migration before updating the service; the normal migrator runs `0.2.1/10-claim-prompt-locales.sql`.
- Commit administrator Agent configuration and version history atomically; serialize version activation and deletion and refresh cached state after acquiring locks (related to #2341).
- Keep import and administrator Prompt writes in Agent → Prompt lock order, and run the MariaDB concurrency regressions in CI.
- Scope current-read configuration locks to a Prompt binding index (`0.2.1/11-agent-prompt-index.sql`), preserving concurrent binding history and retrying administrator edits that race with rebinding.
