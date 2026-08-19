// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// AgentIntentPlanning Semantic Search: Intent-based analysis of agents + planning strategies.
func (k *knRetrievalServiceImpl) AgentIntentPlanning(ctx context.Context, req *interfaces.SemanticSearchRequest) (resp *interfaces.SemanticSearchResponse, err error) {
	// Record observability data.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// The conceptual intent analysis agent has been retired along with the decision-agent, and semantic retrieval has been downgraded to a Query-based keyword recall strategy.
	queryUnderstandResult := &interfaces.QueryUnderstanding{}
	queryStrategys := k.longtailRecallByKnowledgeNetwork(req.Query)
	// Filter query strategies.
	queryStrategys = k.filterQueryStrategysBySearchScope(queryStrategys, req.SearchScope)
	// TODO: Filter query strategies based on search and configuration.
	// Strategy execution: parse and execute query_strategy concurrently and obtain the results.
	conceptResults, err := k.parallelExecSemanticQueryStrategy(ctx, req.KnID, queryStrategys)
	if err != nil {
		return
	}
	// Return the executed strategies.
	queryUnderstandResult.QueryStrategys = queryStrategys
	// TODO: instance data sampling, skipped in this version.
	// Sorting: fine sorting, deduplication.
	rerankConceptResults, err := k.rerankConcepts(ctx, queryUnderstandResult, conceptResults, req.RerankAction, req.MaxConcepts, req.RerankLLMModel, req.RerankVectorModel)
	if err != nil {
		return
	}
	// Assemble the result.
	resp = &interfaces.SemanticSearchResponse{
		QueryUnderstanding: queryUnderstandResult,
		KnowledgeConcepts:  rerankConceptResults,
		HitsTotal:          len(conceptResults),
	}
	return
}

// deduplicateConcepts Concept result deduplication: Deduplication based on ID and Type.
func (k *knRetrievalServiceImpl) deduplicateConcepts(concepts []*interfaces.ConceptResult) []*interfaces.ConceptResult {
	seen := make(map[string]bool)
	unique := make([]*interfaces.ConceptResult, 0)
	for _, c := range concepts {
		uniqueKey := fmt.Sprintf("%s:%s", c.ConceptType, c.ConceptID)
		if !seen[uniqueKey] {
			seen[uniqueKey] = true
			unique = append(unique, c)
		}
	}
	return unique
}

// Filter query strategies based on search and configuration.
func (k *knRetrievalServiceImpl) filterQueryStrategysBySearchScope(queryStrategys []*interfaces.SemanticQueryStrategy, searchScope *interfaces.SearchScopeConfig) []*interfaces.SemanticQueryStrategy {
	// Filtered query strategy.
	filteredQueryStrategys := make([]*interfaces.SemanticQueryStrategy, 0)
	for _, queryStrategy := range queryStrategys {
		if queryStrategy.Filter != nil {
			switch queryStrategy.Filter.ConceptType {
			case interfaces.KnConceptTypeObject:
				if !*searchScope.IncludeObjectTypes {
					continue
				}
			case interfaces.KnConceptTypeRelation:
				if !*searchScope.IncludeRelationTypes {
					continue
				}
			case interfaces.KnConceptTypeAction:
				if !*searchScope.IncludeActionTypes {
					continue
				}
			}
		}
		filteredQueryStrategys = append(filteredQueryStrategys, queryStrategy)
	}
	return filteredQueryStrategys
}
