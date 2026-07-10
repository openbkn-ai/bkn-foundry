---
issue: "#114"
branch: "fix/114-search-schema-rerank-degradation"
module: "context-loader"
status: "draft"
author: "@sh00tg0a1"
created: "2026-07-10"
pr: ""
---

# Fix #114 (Phase A): search_schema reranker configurable + graceful degradation

## Background And Goals

`search_schema` on a knowledge network recalls candidate concepts (object /
relation types) and then re-ranks them by semantic relevance. When the ranking
stage fails, the tool returns concepts unrelated to the query (e.g. querying
"裁判" returns the `award_winners / awards / players / teams` cluster), so
Decision Agent cannot obtain the correct schema and loops on
`search_schema` / `run_sql` / `describe_resource` (8+ useless retrievals for a
single question).

Issue #114 records three root causes. This document covers **Phase A only**,
the first root cause, which is a self-contained agent-retrieval-only fix that
removes a fragile external dependency:

1. Fine-ranking calls
   `mfModelAPIClient.Rerank(ctx, query, docs, "")` with an **always-empty**
   model.
2. The client hardcodes the empty model to the literal `"reranker"`
   (`drivenadapters/mf_model_api_client.go:131`) and calls
   `mf-model-api /v1/small-model/reranker`.
3. The model factory frequently has **no model named `reranker`** registered
   (only the embedding small model exists after a fresh deploy / VM wipe), so
   mf-model-api returns `ModelFactory.ExternalSmallModel.Used.NameNotExist`
   (400).
4. `rankRelationTypes` catches the error and falls back to
   `rankRelationTypesBySimpleMatch` (`concept_retrieval.go:568`), a name-only
   match that **discards the coarse-recall relevance signal** → unrelated
   concepts surface.

Today this is worked around at the environment level by registering a
`reranker` small model (adapter → dashscope `gte-rerank-v2`) in each target
cluster's `t_small_model`. That keeps breaking on every fresh deploy. Phase A
makes the ranking correct **without** depending on that registration.

### Scope

- **In scope (Phase A):** reranker model name configurable + per-request
  override; relevance-preserving graceful degradation when the reranker is
  unavailable. Agent-retrieval only. No bkn-backend / vega change.
- **Out of scope:** object independent ranking / orphan recall
  (`selectObjectTypesForConceptRetrieval`) → **Phase B**, tracked with #147
  (same function). Concept-index vectors → a **separate issue** on the
  KN-build side (bkn-backend / vega embed concepts into
  `adp_bkn_concept_dataset`); agent-retrieval cannot populate that index.

## Design

### 1. Reranker model name configurable

Today the reranker model name is a literal buried in the driven adapter. Move
the default to config and allow a per-request override.

- **Config default.** Add `RerankModel string` to `KnConceptSearchConfig`
  (`infra/config/config.go`), `yaml:"rerank_model" default:"reranker"`. The
  default preserves current behavior (`"reranker"`). Operators can point it at
  whatever small model their factory actually has, without redeploying a new
  binary or registering a placeholder model.

- **Client stops hardcoding.** `mfModelAPIClient.Rerank` keeps
  `if model == "" { model = "reranker" }` only as an ultimate safety net, but
  the caller now always passes an explicit model, so the literal is no longer
  the effective default.

- **Per-request override.** `rankRelationTypes` currently receives
  `enableRerank bool`. Extend the recall path to also carry a `rerankModel
  string`: resolve as `perRequest ?? config.ConceptSearchConfig.RerankModel`.
  Surface it on the request structs:
  - `interfaces.SearchSchemaReq` → add `RerankModel *string
    json:"rerank_model,omitempty"` (mirrors the existing
    `rerank_llm_model` override pattern used by the LLM reranker).
  - Thread through `KnSearchConceptRetrievalConfig` →
    `rankRelationTypes(ctx, query, objects, relations, topK, enableRerank,
    rerankModel)` → `Rerank(ctx, query, docs, rerankModel)`.

  `rerank_model` is an **ops / power-user escape hatch**, not agent-facing
  schema. It is exposed on the REST `/in` request body and internal config
  only; it is **not** added to the MCP tool JSON Schema or the `.adp` toolset
  (an LLM agent should not be choosing reranker model names). The graceful
  degradation below is the actual reliability fix — `rerank_model` just lets an
  operator override without a redeploy.

### 2. Graceful degradation (relevance-preserving)

When `Rerank` fails, do **not** re-score by name. The recalled relations and
objects already carry a coarse-recall BM25 `_score`
(`interfaces.RelationType.Score`, `interfaces.ObjectType.Score`), which is a
real relevance signal. Degrade to that instead of throwing it away:

```
rerankResp, err := s.rerankClient.Rerank(ctx, query, documents, rerankModel)
if err != nil {
    s.logger.Warnf("[RankRelationTypes] Rerank unavailable (%v); "+
        "degrading to coarse-recall _score order", err)
    return rankRelationTypesByScore(relations, topK)   // sort by .Score desc, truncate
}
```

`rankRelationTypesByScore` sorts by `rel.Score` descending and truncates to
`topK`, using a stable sort so equal-score relations keep recall order. The
existing name-only `rankRelationTypesBySimpleMatch` is retained **only** as a
last resort for the degenerate case where every `_score` is zero (e.g.
coarse-recall disabled), guarded explicitly rather than used as the default
fallback.

Rationale for BM25 `_score` over the existing `knrerank.rerankByLLM` (system
default big model) as the primary fallback:

- BM25 `_score` is already computed, zero extra latency, zero extra LLM cost,
  and deterministic.
- The LLM reranker adds a network round-trip and token cost on the hot
  `search_schema` path, and itself depends on a reachable LLM. It is a heavier,
  optional secondary — not needed to restore correctness. Left out of Phase A
  to keep the change small and the failure mode simple.

### Call sites touched

- `interfaces/search_schema.go` — `SearchSchemaReq.RerankModel`.
- `interfaces/kn_search_local.go` — carry `RerankModel` on the concept
  retrieval config.
- driver adapter for `/in` search_schema — map `rerank_model` into the local
  request.
- `logics/knsearch/concept_retrieval.go` — `rankRelationTypes` signature +
  degradation branch; new `rankRelationTypesByScore`.
- `drivenadapters/mf_model_api_client.go` — comment only (behavior unchanged;
  literal now a safety net).
- `infra/config/config.go` — `KnConceptSearchConfig.RerankModel`.

## Acceptance Criteria

- With **no** `reranker` model registered in the factory,
  `POST /api/agent-retrieval/in/v1/kn/search_schema {"kn_id":"worldcup_vega_catalog_bkn","query":"球员","max_concepts":5}`
  returns query-relevant concepts (player / match cluster), **not** the
  `award_*` cluster. Logs show
  `Rerank unavailable ...; degrading to coarse-recall _score order`, **not**
  `fallback to simple match`.
- With a `reranker` model registered, results are unchanged from today
  (no regression).
- `rerank_model` in the REST request body overrides the configured default and
  reaches mf-model-api (verifiable in the mf-model-api request log).
- `search_schema` MCP tool schema and the `.adp` toolset are unchanged
  (`rerank_model` not surfaced to agents).

## Failure Conditions (must NOT happen)

- Reranker unavailable → returns concepts unrelated to the query.
- Degradation path re-orders by name and buries the BM25-relevant concepts.
- `rerank_model` passed but ignored (still hits `reranker`).
- Any change under `adp/bkn` (bkn-backend) or `adp/vega`.

## Out of Scope → Follow-ups

- **Phase B (#147 + #114②):** `selectObjectTypesForConceptRetrieval` keeps
  high coarse-recall-score orphan objects (relation-less but relevant tables,
  e.g. `referees`) instead of returning only relation-endpoint objects.
- **KN-build issue (#114③):** embed concept name+comment into
  `adp_bkn_concept_dataset` so the KNN coarse-recall subquery is no longer a
  no-op; or remove the misleading KNN subquery.
