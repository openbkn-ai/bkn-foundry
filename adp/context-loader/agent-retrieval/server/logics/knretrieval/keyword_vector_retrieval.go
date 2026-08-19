// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// KeywordVectorRetrieval based on keyword + vector recall.
func (k *knRetrievalServiceImpl) KeywordVectorRetrieval(ctx context.Context, req *interfaces.SemanticSearchRequest) (resp *interfaces.SemanticSearchResponse, err error) {
	// Record observability data.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Query strategy.
	var queryStrategys []*interfaces.SemanticQueryStrategy
	// Concept result candidate set.
	conceptResults := []*interfaces.ConceptResult{}
	// Customize the query strategy and request the business knowledge network interface for keyword matching.
	queryStrategys = k.longtailRecallByKnowledgeNetwork(req.Query)
	// Filter query strategies.
	queryStrategys = k.filterQueryStrategysBySearchScope(queryStrategys, req.SearchScope)
	if len(queryStrategys) > 0 {
		// Execute query strategies concurrently.
		var queryConceptResults []*interfaces.ConceptResult
		queryConceptResults, err = k.parallelExecSemanticQueryStrategy(ctx, req.KnID, queryStrategys)
		if err != nil {
			k.logger.WithContext(ctx).Warnf("[SemanticSearchV2] parallelExecSemanticQueryStrategy failed. knId:%s, queryStrategys:%v, err:%v", req.KnID, queryStrategys, err)
			return
		}
		if len(queryConceptResults) > 0 {
			conceptResults = append(conceptResults, queryConceptResults...)
		}
	}
	queryUnderstanding := &interfaces.QueryUnderstanding{
		OriginQuery:    req.Query,
		QueryStrategys: queryStrategys,
	}
	// TODO: instance data sampling, skipped in this version.
	// Sorting: Sort according to matching score, remove duplicates.
	rerankConceptResults, err := k.rerankConcepts(ctx, queryUnderstanding, conceptResults, req.RerankAction, req.MaxConcepts, req.RerankLLMModel, req.RerankVectorModel)
	if err != nil {
		return
	}
	// Assemble the result.
	resp = &interfaces.SemanticSearchResponse{
		QueryUnderstanding: queryUnderstanding,
		KnowledgeConcepts:  rerankConceptResults,
		HitsTotal:          len(conceptResults),
	}
	return
}
