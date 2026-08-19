// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"context"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// rerankByConceptType collects different concept class sets and sorts them. The top limit of each concept set is taken.
//
//nolint:unused // Reserved function, may be used later.
func (k *knRetrievalServiceImpl) rerankByConceptType(conceptResults []*interfaces.ConceptResult, limit int) []*interfaces.ConceptResult {
	// Deduplicate.
	conceptResults = k.deduplicateConcepts(conceptResults)
	conceptTypeMap := make(map[interfaces.KnConceptType][]*interfaces.ConceptResult)
	// Classification by concept type.
	for _, concept := range conceptResults {
		conceptTypeMap[concept.ConceptType] = append(conceptTypeMap[concept.ConceptType], concept)
	}
	// Sort by concept type.
	for _, concepts := range conceptTypeMap {
		sort.Slice(concepts, func(i, j int) bool {
			return concepts[i].MatchScore > concepts[j].MatchScore
		})
		if len(concepts) > limit {
			concepts = concepts[:limit]
		}
	}
	result := []*interfaces.ConceptResult{}
	// Sequence requirements: object type, relation type, action class.
	if len(conceptTypeMap[interfaces.KnConceptTypeObject]) > 0 {
		result = append(result, conceptTypeMap[interfaces.KnConceptTypeObject]...)
	}
	if len(conceptTypeMap[interfaces.KnConceptTypeRelation]) > 0 {
		result = append(result, conceptTypeMap[interfaces.KnConceptTypeRelation]...)
	}
	if len(conceptTypeMap[interfaces.KnConceptTypeAction]) > 0 {
		result = append(result, conceptTypeMap[interfaces.KnConceptTypeAction]...)
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

// rerankConcepts rerank concepts. Three-layer model priority: request incoming (llmModel/vectorModel) > [agent/retrieval configuration, reserved] > yaml/default (downstream fallback).
// The per-request model is transparently transmitted through KnowledgeRerankReq, and is partially covered when the downstream request is constructed, without writing the reranker singleton.
func (k *knRetrievalServiceImpl) rerankConcepts(ctx context.Context, queryUnderstandResult *interfaces.QueryUnderstanding, conceptResults []*interfaces.ConceptResult,
	action interfaces.KnowledgeRerankActionType, limit int, llmModel, vectorModel string,
) (rerankResults []*interfaces.ConceptResult, err error) {
	// Deduplicate.
	conceptResults = k.deduplicateConcepts(conceptResults)

	// Optimization 1: If there is no concept, return an empty list directly without calling rerank.
	if len(conceptResults) == 0 {
		k.logger.WithContext(ctx).Debug("[knretrieval#rerank] No concepts to rerank, returning empty list")
		return []*interfaces.ConceptResult{}, nil
	}

	if action == interfaces.KnowledgeRerankActionDefault {
		rerankResults = conceptResults
	} else {
		// Using the local Rerank module.
		k.logger.WithContext(ctx).Info("[knretrieval#rerank] Using local KnowledgeReranker")
		rerankResults, err = k.knReranker.Rerank(ctx, &interfaces.KnowledgeRerankReq{
			QueryUnderstanding: queryUnderstandResult,
			KnowledgeConcepts:  conceptResults,
			Action:             action,
			LLMModel:           llmModel,
			VectorModel:        vectorModel,
		})
		if err != nil {
			// When local rerank fails, return directly to the original concept list (downgrade)
			k.logger.WithContext(ctx).Warnf("[knretrieval#rerank] Local rerank failed: %v, using original concepts as fallback", err)
			rerankResults = conceptResults
			err = nil // Clean up errors to ensure core functionality is not affected.
		}
	}
	// Sort by RerankScore in descending order and MatchScore in descending order if they are the same (no longer filter RerankScore=0 to avoid concepts being null)
	rerankResults = k.sortByRerankAndMatchScore(rerankResults)
	// Pagination.
	if len(rerankResults) > limit {
		rerankResults = rerankResults[:limit]
	}
	return
}

// sortByRerankAndMatchScore sorts by RerankScore in descending order, and if they are the same, sort by MatchScore in descending order.
func (k *knRetrievalServiceImpl) sortByRerankAndMatchScore(conceptResults []*interfaces.ConceptResult) []*interfaces.ConceptResult {
	if conceptResults == nil {
		return nil
	}
	sort.Slice(conceptResults, func(i, j int) bool {
		if conceptResults[i].RerankScore != conceptResults[j].RerankScore {
			return conceptResults[i].RerankScore > conceptResults[j].RerankScore
		}
		return conceptResults[i].MatchScore > conceptResults[j].MatchScore
	})
	return conceptResults
}
