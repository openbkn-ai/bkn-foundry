# Changelog

Chinese version: [CHANGELOG.zh-CN.md](CHANGELOG.zh-CN.md)

## Unreleased

### Features & Improvements

- Surface actionable permission-request guidance from `run_cypher`, `search_instance`, and `get_logic_properties_values`
  - Preserve explicit row-filter and property-masking impacts across BKN Backend and Context Loader boundaries
  - Put localized Studio request links in both structured tool output and the user-facing message so MCP hosts can present them reliably

### Added

- Extend read-only Cypher with inferred typed value results, arithmetic/CASE/nullable booleans, lists/maps, registered scalar functions and mixed top-level aggregates. Carry recursive result types, exact numbers, candidate/match/evidence completeness and result pagination through REST/MCP and Trace. Snapshot fields remain explicit and authorized; evaluation is bounded. Add independently scoped UNION/UNION ALL branches and consecutive UNWIND expansion before RETURN, with shared request budgets and branch-aware trace references. Add ordered WITH/MATCH/OPTIONAL MATCH/UNWIND stages, variable scope and aliases, post-aggregation filtering, stage pagination, wildcard projections, and node-property origins through aliases and collect/UNWIND. Staged MATCH uses bounded authorized scans before correlation and requires complete accessible node keys. Paths and full TCK/integration acceptance remain pending.

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
