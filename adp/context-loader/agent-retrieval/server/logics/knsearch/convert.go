// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch provides business logic for knowledge network search operations.
// file: convert.go
// description: Conversion of KnSearchReq/KnSearchResp to local request/response.
package knsearch

import (
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// KnSearchReqToLocal converts KnSearchReq to KnSearchLocalRequest.
func KnSearchReqToLocal(req *interfaces.KnSearchReq) *interfaces.KnSearchLocalRequest {
	if req == nil {
		return nil
	}
	local := &interfaces.KnSearchLocalRequest{
		AccountID:   req.XAccountID,
		AccountType: req.XAccountType,
		Query:       req.Query,
		KnID:        req.KnID,
	}
	// By default, only Schema is returned: this is what all existing callers get today.
	// Explicitly pass only_schema=false to perform additional semantic instance recall.
	local.OnlySchema = true
	if req.OnlySchema != nil {
		local.OnlySchema = *req.OnlySchema
	}
	local.EnableRerank = true
	if req.EnableRerank != nil {
		local.EnableRerank = *req.EnableRerank
	}
	if req.RerankModel != nil {
		local.RerankModel = *req.RerankModel
	}
	local.IndexOpsOnly = req.IndexOpsOnly
	if req.IncludeColumns != nil {
		local.IncludeColumns = *req.IncludeColumns
	}
	local.RetrievalConfig = retrievalConfigToLocal(req.RetrievalConfig)
	applySearchScopeToLocalRetrievalConfig(local, req.SearchScope)
	return local
}

func applySearchScopeToLocalRetrievalConfig(local *interfaces.KnSearchLocalRequest, scope *interfaces.SearchScopeConfig) {
	if local == nil || scope == nil {
		return
	}
	conceptGroups := normalizeConceptGroups(scope.ConceptGroups)
	if len(conceptGroups) == 0 {
		return
	}
	if local.RetrievalConfig == nil {
		local.RetrievalConfig = &interfaces.KnSearchRetrievalConfig{}
	}
	if local.RetrievalConfig.ConceptRetrieval == nil {
		local.RetrievalConfig.ConceptRetrieval = &interfaces.KnSearchConceptRetrievalConfig{}
	}
	local.RetrievalConfig.ConceptRetrieval.ConceptGroups = conceptGroups
}

// retrievalConfigToLocal Convert any form of retrieval_config to *KnSearchRetrievalConfig.
// Explicit copying when cfg is *RetrievalConfig: avoid bool fields with omitempty causing false to be omitted during JSON round-trips.
// Then Unmarshal to *bool becomes nil (different from explicit false semantics).
func retrievalConfigToLocal(cfg any) *interfaces.KnSearchRetrievalConfig {
	if cfg == nil {
		return nil
	}
	switch v := cfg.(type) {
	case *interfaces.RetrievalConfig:
		return retrievalConfigStructToLocal(v)
	case interfaces.RetrievalConfig:
		return retrievalConfigStructToLocal(&v)
	default:
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil
		}
		var local interfaces.KnSearchRetrievalConfig
		if err := json.Unmarshal(data, &local); err != nil {
			return nil
		}
		return &local
	}
}

func retrievalConfigStructToLocal(rc *interfaces.RetrievalConfig) *interfaces.KnSearchRetrievalConfig {
	if rc == nil {
		return nil
	}
	out := &interfaces.KnSearchRetrievalConfig{}
	if rc.ConceptRetrieval != nil {
		cr := rc.ConceptRetrieval
		out.ConceptRetrieval = &interfaces.KnSearchConceptRetrievalConfig{
			ConceptGroups:          normalizeConceptGroups(cr.ConceptGroups),
			ObjectTypes:            normalizeObjectTypeIDs(cr.ObjectTypes),
			ExcludeObjectTypes:     normalizeObjectTypeIDs(cr.ExcludeObjectTypes),
			TopK:                   cr.TopK,
			IncludeSampleData:      boolPtr(cr.IncludeSampleData),
			SchemaBrief:            boolPtr(cr.SchemaBrief),
			EnableCoarseRecall:     boolPtr(cr.EnableCoarseRecall),
			CoarseObjectLimit:      cr.CoarseObjectLimit,
			CoarseRelationLimit:    cr.CoarseRelationLimit,
			CoarseMinRelationCount: cr.CoarseMinRelationCount,
			EnablePropertyBrief:    boolPtr(cr.EnablePropertyBrief),
			PerObjectPropertyTopK:  cr.PerObjectPropertyTopK,
			GlobalPropertyTopK:     cr.GlobalPropertyTopK,
		}
	}
	if rc.SemanticInstanceRetrieval != nil {
		s := rc.SemanticInstanceRetrieval
		out.SemanticInstanceRetrieval = &interfaces.KnSearchSemanticInstanceRetrievalConfig{
			InitialCandidateCount:             s.InitialCandidateCount,
			PerTypeInstanceLimit:              s.PerTypeInstanceLimit,
			MaxSemanticSubConditions:          s.MaxSemanticSubConditions,
			SemanticFieldKeepRatio:            s.SemanticFieldKeepRatio,
			SemanticFieldKeepMin:              s.SemanticFieldKeepMin,
			SemanticFieldKeepMax:              s.SemanticFieldKeepMax,
			SemanticFieldRerankBatchSize:      s.SemanticFieldRerankBatchSize,
			MinDirectRelevance:                s.MinDirectRelevance,
			EnableGlobalFinalScoreRatioFilter: boolPtr(s.EnableGlobalFinalScoreRatioFilter),
			GlobalFinalScoreRatio:             s.GlobalFinalScoreRatio,
			ExactNameMatchScore:               s.ExactNameMatchScore,
		}
	}
	if rc.PropertyFilter != nil {
		p := rc.PropertyFilter
		out.PropertyFilter = &interfaces.KnSearchPropertyFilterConfig{
			MaxPropertiesPerInstance: p.MaxPropertiesPerInstance,
			MaxPropertyValueLength:   p.MaxPropertyValueLength,
			EnablePropertyFilter:     boolPtr(p.EnablePropertyFilter),
		}
	}
	return out
}

// KnSearchLocalResponseToResp Convert KnSearchLocalResponse to KnSearchResp.
func KnSearchLocalResponseToResp(local *interfaces.KnSearchLocalResponse) *interfaces.KnSearchResp {
	if local == nil {
		return nil
	}
	resp := &interfaces.KnSearchResp{
		ObjectTypes:   local.ObjectTypes,
		RelationTypes: local.RelationTypes,
		ActionTypes:   local.ActionTypes,
	}
	// Only bring nodes/message if instance recall is actually done: Schema-only responses remain as is.
	if len(local.Nodes) > 0 {
		resp.Nodes = local.Nodes
	}
	if local.Message != "" {
		message := local.Message
		resp.Message = &message
	}
	return resp
}
