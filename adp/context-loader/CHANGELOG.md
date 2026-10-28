# Changelog

Chinese version: [CHANGELOG.zh-CN.md](CHANGELOG.zh-CN.md)

## Unreleased

- `search_schema` expands enums and outputs aliases (BKN spec 3.0 stage 2). A property whose value type is an enum carries `value_type` and its items as `enum: [{value, display, deprecated}]`, deprecated items included; an enum over the expansion limit (`concept_search_config.enum_expand_limit`, default 50), or any enum in brief mode, gives `enum_count` instead, and the new `enum_query` parameter looks its items up by display name or code. Object, relation and action types carry `aliases` at either level, data properties in full mode only. `get_kn_detail`, `get_object_types` and `get_relation_types` do not carry aliases. A network without value types or aliases gets the same output as before. (#2282)

- `search_schema` outputs the BKN spec 3.0 relation declarations: `reverse_name`, `cardinality`, `required`, `characteristics`, `verification`, `importance` (omitted when primary) and relation properties. A primary relation carries its fallback path under `fallback` with the rule for using it, and a fallback path no longer appears on its own: recalling one brings its primary in. Object, relation and action types carry `lifecycle` when not active, so a deprecated definition looked up by id (`get_object_types`, `get_relation_types`, a pinned object type) says what replaces it. A network without these declarations gets the same output as before. (#2285)

- Reserve acknowledgement-recovery time inside existing lifecycle budgets, replay start with its original idempotency key, and freeze terminal payload/artifact envelopes across retries without re-executing business tools. (#2539)

### Features & Improvements

- `search_schema` outputs the BKN spec 3.0 stage 1 property declarations on data properties: `semantic`, `required`, `unit`, `crs` and `timezone` in full mode, only `semantic` in brief mode. An undeclared field is omitted, so a network without declarations gets the same output as before. `get_kn_detail` and `get_object_types` do not carry them. (#2260)

- Platform Functions reuse governed Evidence capture to record existing structured result snapshots and actual execution identity without tool-side evidence declarations for Core Claim assembly. Snapshots omitted before publication preserve existing receipt completeness. Once a snapshot event is declared, delivery/confirmation failure can still make evidence partial. Runtime capture failures emit sanitized diagnostics; managed finish still rejects caller-owned claims. (#1332)

- Submit Interaction finish with a separate bounded POST/replay budget (default 5 seconds), preserving the exact request/key across finite retries. Unconfirmed submission now returns an MCP tool error with `retry_same_finish`; only a matching Core terminal acknowledgement is success. Tool execution is never repeated. (#2340)

- Preserve canonical business references, operation edges and causation IDs through Kafka evidence publishing; recover transient attempt-finalization infrastructure failures within the existing bounded budget without re-executing tools. (#2425)

- Stop retrying artifact uploads when ingest authentication is explicitly unconfigured. Distinguish artifact-endpoint outages from lifecycle Core outages while preserving committed start IDs, capture authorization failures, and the existing Trace I/O budgets. (#1691)

- Add opt-in durable answer handoff to Core managed finish, avoiding synchronous answer-artifact uploads while retaining the existing Trace time budget. Business completion and pending evidence are returned separately; the default legacy path is unchanged. Enable only after all Core workers have upgraded. (#1691)

- Surface actionable permission-request guidance from `run_cypher`, `search_instance`, and `get_logic_properties_values`
  - Preserve explicit row-filter and property-masking impacts across BKN Backend and Context Loader boundaries
  - Put localized Studio request links in both structured tool output and the user-facing message so MCP hosts can present them reliably

### Removed

- Remove `POST /kn/semantic-search` (both the public path and the `in/v1` internal path). Use `POST /kn/search_schema`
  - Every field that set the endpoint apart from `search_schema` -- `query_understanding`, `hits_total`, `intent_score` / `match_score` / `rerank_score` and the per-concept `samples` -- was cleared by the handler before the response was written, so `return_query_understanding: true` never had a visible effect and the response was a strict subset of `search_schema`
  - The `agent_intent_planning` and `agent_intent_retrieval` modes still spent an LLM call on intent analysis whose result was then discarded
  - Deletes the `knretrieval` and `knrerank` packages, the `SemanticSearch*` / `QueryUnderstanding` / `ConceptResult` / `KnowledgeRerank*` types, and the `api_public/kn.yaml` and `api_private/kn_schema_search.yaml` documents
  - Removes the now-unread `rerank_llm` configuration block from the service config, the Helm values and the rendered ConfigMap. Existing overrides for that key are ignored rather than rejected
- Remove `search_scope.include_object_types` / `include_relation_types` / `include_action_types` from the `kn_search` request contract
  - `kn_search` never filtered its response by concept type; the three switches were accepted and silently ignored. `search_scope.concept_groups` is unaffected, and `search_schema` keeps its own working scope switches
- Remove `retrieval_config.semantic_instance_retrieval.max_keywords` and `pre_filter_per_type_limit` from the request contract; no code has ever read them

## 0.8.0

### Features & Improvements

- Add concept group scoped Schema discovery in `search_schema`
  - Support `search_scope.concept_groups` to limit object, relation, and action schema discovery to selected BKN concept groups
  - Keep existing `search_schema` behavior unchanged when concept groups are not provided
  - Return referenced object types together with scoped relation and action schemas, so callers receive a complete Schema context
  - Note: metric schema requests carry the same concept group scope, but actual metric filtering depends on BKN metrics support
- Embed the ContextLoader standard toolset in the service startup flow
  - Automatically sync the built-in toolset to execution-factory during ContextLoader startup
  - Use `ContextLoader 标准内置工具集；契约版本: 0.8.0` as the toolset contract description

### Compatibility

- Keep compatible and legacy HTTP paths unchanged; new integrations that need concept group scope should use `search_schema`

### Documentation

- Update API, MCP schema, toolset, and release documentation for concept group scoped Schema discovery
- Document the built-in ContextLoader toolset delivery model and contract version rule

## 0.7.0

### Features & Improvements

- Add `search_schema` as the standard Schema discovery entry for MCP and HTTP callers
  - Support object, relation, action, and metric schema discovery from one interface
  - Use request body `kn_id` for the HTTP `search_schema` API
- Consolidate MCP Schema discovery around `search_schema` to reduce tool selection ambiguity for agents

### Compatibility

- Keep `kn_search` as a compatible HTTP path and `semantic-search` as a legacy HTTP path

### Documentation

- Update release overview and tool usage documentation for the `search_schema` entry unification and metric schema recall contract

- Allow REST `execute_tool` without lifecycle context, matching `search_schema`;
  keep permission checks and explicit-context validation, and leave the MCP
  managed-context requirement unchanged.
