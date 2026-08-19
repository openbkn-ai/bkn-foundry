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

// AgentIntentRetrieval semanticsretrieve.
// Originally relied on "Conceptual Intention Analysis Agent" + "Conceptual Recall Strategy Agent" for coarse intent recognition and recall planning;
// After both are retired with decision-agent (agent-factory), this path is downgraded to Query-based keyword recall (longtail).
// Then the query strategy is executed and rearranged through the business knowledge network. Access parties requiring full Schema recall should use search_schema instead.
func (k *knRetrievalServiceImpl) AgentIntentRetrieval(ctx context.Context, req *interfaces.SemanticSearchRequest) (resp *interfaces.SemanticSearchResponse, err error) {
	// Record observability data.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	queryUnderstanding := &interfaces.QueryUnderstanding{}
	// Build keyword query strategy based on user Query.
	queryStrategys := k.longtailRecallByKnowledgeNetwork(req.Query)
	// Filter query strategies.
	queryStrategys = k.filterQueryStrategysBySearchScope(queryStrategys, req.SearchScope)

	// Concept result candidate set.
	conceptResults := []*interfaces.ConceptResult{}
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
		// Return the executed strategies.
		queryUnderstanding.QueryStrategys = queryStrategys
	}
	// Sorting: Sort by concept type, remove duplicates.
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

// Long-tail recall strategy: keyword matching based on business knowledge network -- building query strategy.
func (k *knRetrievalServiceImpl) longtailRecallByKnowledgeNetwork(query string) (queryStrategys []*interfaces.SemanticQueryStrategy) {
	// Generate query strategy based on the original Query of user data.
	var empty []*interfaces.QueryStrategyCondition
	objectTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeObject, query, empty)
	if objectTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, objectTypeDiscoveryStrategy)
	}
	releationTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeRelation, query, empty)
	if releationTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, releationTypeDiscoveryStrategy)
	}
	actionTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeAction, query, empty)
	if actionTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, actionTypeDiscoveryStrategy)
	}
	return
}
