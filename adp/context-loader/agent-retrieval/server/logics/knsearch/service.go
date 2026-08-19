// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (local search main service implementation)
// file: service.go
package knsearch

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Search Knowledge Network Search Local Main Entrance.
func (s *localSearchImpl) Search(ctx context.Context, req *interfaces.KnSearchLocalRequest) (*interfaces.KnSearchLocalResponse, error) {
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Start Search, kn_id=%s, query=%s, only_schema=%v",
		req.KnID, req.Query, req.OnlySchema)

	// 1. Merge configuration.
	mergedConfig := MergeRetrievalConfig(req.RetrievalConfig)
	s.logger.WithContext(ctx).Debugf("[KnSearchLocal] Merged config: concept_top_k=%d, schema_brief=%v, enable_coarse_recall=%v",
		mergedConfig.ConceptRetrieval.TopK,
		boolValue(mergedConfig.ConceptRetrieval.SchemaBrief),
		boolValue(mergedConfig.ConceptRetrieval.EnableCoarseRecall))

	// 2. Concept Recall (Schema Recall)
	conceptResult, err := s.conceptRetrieval(ctx, req, mergedConfig.ConceptRetrieval)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnSearchLocal] Concept retrieval failed: %v", err)
		return nil, err
	}

	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Concept retrieval completed: object_types=%d, relation_types=%d, action_types=%d",
		len(conceptResult.ObjectTypes), len(conceptResult.RelationTypes), len(conceptResult.ActionTypes))

	// Concept recall takes the knowledge network export view by default. Data source enrichment is not performed in that way.
	// condition_operations is always empty. The caller (especially the Agent) relies on it to determine whether the field can be.
	// match / knn, if you get the empty one, you can only rely on guessing - no matter how accurate your ability is, if you can't get it, it means nothing. Complete it here,
	// Schema responses share the same results with subsequent instance recalls.
	// Always complete all operators: instance recall relies on it to select searchable fields, and pruning is left until before the response is issued to avoid responding.
	// The streamlined switch incidentally changed the recall semantics.
	s.backfillConditionOperations(ctx, req.KnID, conceptResult.ObjectTypes, false)

	// 3. buildresponse.
	response := &interfaces.KnSearchLocalResponse{
		ObjectTypes:   conceptResult.ObjectTypes,
		RelationTypes: conceptResult.RelationTypes,
		ActionTypes:   conceptResult.ActionTypes,
	}

	// 4. Semantic instance recall: only done when the caller explicitly wants an instance (search_schema is always schema-only)
	if req.OnlySchema {
		s.logger.WithContext(ctx).Infof("[KnSearchLocal] only_schema=true, skip semantic instance retrieval")
		trimToIndexBackedOperations(response.ObjectTypes, req.IndexOpsOnly)
		return response, nil
	}

	instanceResult, instanceErr := s.semanticInstanceRetrieval(ctx, req, conceptResult.ObjectTypes, mergedConfig)
	if instanceErr != nil {
		// Instance recall failure does not bring down the entire search: the Schema itself is already a useful result and is returned in a degraded manner.
		s.logger.WithContext(ctx).Warnf("[KnSearchLocal] Semantic instance retrieval failed, degrade to schema-only: %v", instanceErr)
		trimToIndexBackedOperations(response.ObjectTypes, req.IndexOpsOnly)
		return response, nil
	}

	response.Nodes = instanceResult.Nodes
	response.Message = instanceResult.Message
	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Semantic instance retrieval completed: nodes=%d", len(response.Nodes))

	trimToIndexBackedOperations(response.ObjectTypes, req.IndexOpsOnly)
	return response, nil
}
