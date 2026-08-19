// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (configuration and defaults)
// file: config.go
package knsearch

import "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"

// DefaultConceptRetrievalConfig returns the concept recall default configuration.
func DefaultConceptRetrievalConfig() *interfaces.KnSearchConceptRetrievalConfig {
	return &interfaces.KnSearchConceptRetrievalConfig{
		TopK:                   10,
		IncludeSampleData:      boolPtr(false),
		SchemaBrief:            boolPtr(false),
		EnableCoarseRecall:     boolPtr(true),
		CoarseObjectLimit:      2000,
		CoarseRelationLimit:    300,
		CoarseMinRelationCount: 5000,
		EnablePropertyBrief:    boolPtr(true),
		PerObjectPropertyTopK:  8,
		GlobalPropertyTopK:     30,
	}
}

// DefaultSemanticInstanceRetrievalConfig returns the semantic instance recall default configuration.
func DefaultSemanticInstanceRetrievalConfig() *interfaces.KnSearchSemanticInstanceRetrievalConfig {
	return &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		InitialCandidateCount:             50,
		PerTypeInstanceLimit:              5,
		MaxSemanticSubConditions:          10,
		SemanticFieldKeepRatio:            0.2,
		SemanticFieldKeepMin:              5,
		SemanticFieldKeepMax:              15,
		SemanticFieldRerankBatchSize:      128,
		MinDirectRelevance:                0.3,
		EnableGlobalFinalScoreRatioFilter: boolPtr(true),
		GlobalFinalScoreRatio:             0.25,
		ExactNameMatchScore:               0.85,
		EnableKnnInstanceRetrieval:        boolPtr(true),
		MaxKnnSubConditionsPerType:        1,
		EnableRRFFusion:                   boolPtr(true),
		RRFK:                              60,
		KnnWeight:                         float64Ptr(0.5),
		InstanceRerankMode:                InstanceRerankModeOff,
		RerankTopN:                        50,
		RerankFieldCharLimit:              200,
	}
}

// DefaultPropertyFilterConfig returnpropertyfilterdefaultconfiguration.
func DefaultPropertyFilterConfig() *interfaces.KnSearchPropertyFilterConfig {
	return &interfaces.KnSearchPropertyFilterConfig{
		MaxPropertiesPerInstance: 20,
		MaxPropertyValueLength:   500,
		EnablePropertyFilter:     boolPtr(true),
	}
}

// MergeRetrievalConfig Merges user configuration and default configuration.
func MergeRetrievalConfig(userConfig *interfaces.KnSearchRetrievalConfig) *interfaces.KnSearchRetrievalConfig {
	result := &interfaces.KnSearchRetrievalConfig{
		ConceptRetrieval:          DefaultConceptRetrievalConfig(),
		SemanticInstanceRetrieval: DefaultSemanticInstanceRetrievalConfig(),
		PropertyFilter:            DefaultPropertyFilterConfig(),
	}

	if userConfig == nil {
		return result
	}

	// Merge concept recall configuration.
	if userConfig.ConceptRetrieval != nil {
		mergeConceptRetrievalConfig(result.ConceptRetrieval, userConfig.ConceptRetrieval)
	}

	// Merge semantic instance recall configuration.
	if userConfig.SemanticInstanceRetrieval != nil {
		mergeSemanticInstanceRetrievalConfig(result.SemanticInstanceRetrieval, userConfig.SemanticInstanceRetrieval)
	}

	// Merge attribute filtering configuration.
	if userConfig.PropertyFilter != nil {
		mergePropertyFilterConfig(result.PropertyFilter, userConfig.PropertyFilter)
	}

	return result
}

func mergeConceptRetrievalConfig(base, user *interfaces.KnSearchConceptRetrievalConfig) {
	if len(user.ConceptGroups) > 0 {
		base.ConceptGroups = normalizeConceptGroups(user.ConceptGroups)
	}
	if len(user.ObjectTypes) > 0 {
		base.ObjectTypes = normalizeObjectTypeIDs(user.ObjectTypes)
	}
	if len(user.ExcludeObjectTypes) > 0 {
		base.ExcludeObjectTypes = normalizeObjectTypeIDs(user.ExcludeObjectTypes)
	}
	if user.TopK > 0 {
		base.TopK = user.TopK
	}
	if user.IncludeSampleData != nil {
		base.IncludeSampleData = user.IncludeSampleData
	}
	if user.SchemaBrief != nil {
		base.SchemaBrief = user.SchemaBrief
	}
	if user.EnableCoarseRecall != nil {
		base.EnableCoarseRecall = user.EnableCoarseRecall
	}
	if user.EnablePropertyBrief != nil {
		base.EnablePropertyBrief = user.EnablePropertyBrief
	}

	if user.CoarseObjectLimit > 0 {
		base.CoarseObjectLimit = user.CoarseObjectLimit
	}
	if user.CoarseRelationLimit > 0 {
		base.CoarseRelationLimit = user.CoarseRelationLimit
	}
	if user.CoarseMinRelationCount > 0 {
		base.CoarseMinRelationCount = user.CoarseMinRelationCount
	}
	if user.PerObjectPropertyTopK > 0 {
		base.PerObjectPropertyTopK = user.PerObjectPropertyTopK
	}
	if user.GlobalPropertyTopK > 0 {
		base.GlobalPropertyTopK = user.GlobalPropertyTopK
	}
}

func mergeSemanticInstanceRetrievalConfig(base, user *interfaces.KnSearchSemanticInstanceRetrievalConfig) {
	if user.InitialCandidateCount > 0 {
		base.InitialCandidateCount = user.InitialCandidateCount
	}
	if user.PerTypeInstanceLimit > 0 {
		base.PerTypeInstanceLimit = user.PerTypeInstanceLimit
	}
	if user.MaxSemanticSubConditions > 0 {
		base.MaxSemanticSubConditions = user.MaxSemanticSubConditions
	}
	if user.SemanticFieldKeepRatio > 0 {
		base.SemanticFieldKeepRatio = user.SemanticFieldKeepRatio
	}
	if user.SemanticFieldKeepMin > 0 {
		base.SemanticFieldKeepMin = user.SemanticFieldKeepMin
	}
	if user.SemanticFieldKeepMax > 0 {
		base.SemanticFieldKeepMax = user.SemanticFieldKeepMax
	}
	if user.SemanticFieldRerankBatchSize > 0 {
		base.SemanticFieldRerankBatchSize = user.SemanticFieldRerankBatchSize
	}
	if user.MinDirectRelevance > 0 {
		base.MinDirectRelevance = user.MinDirectRelevance
	}
	if user.EnableKnnInstanceRetrieval != nil {
		base.EnableKnnInstanceRetrieval = user.EnableKnnInstanceRetrieval
	}
	if user.MaxKnnSubConditionsPerType > 0 {
		base.MaxKnnSubConditionsPerType = user.MaxKnnSubConditionsPerType
	}
	if user.EnableGlobalFinalScoreRatioFilter != nil {
		base.EnableGlobalFinalScoreRatioFilter = user.EnableGlobalFinalScoreRatioFilter
	}
	if user.GlobalFinalScoreRatio > 0 {
		base.GlobalFinalScoreRatio = user.GlobalFinalScoreRatio
	}
	if user.ExactNameMatchScore > 0 {
		base.ExactNameMatchScore = user.ExactNameMatchScore
	}
	if user.EnableRRFFusion != nil {
		base.EnableRRFFusion = user.EnableRRFFusion
	}
	if user.RRFK > 0 {
		base.RRFK = user.RRFK
	}
	if user.KnnWeight != nil {
		base.KnnWeight = user.KnnWeight
	}
	if mode := normalizeRerankMode(user.InstanceRerankMode); mode != "" {
		base.InstanceRerankMode = mode
	}
	if user.InstanceRerankModel != "" {
		base.InstanceRerankModel = user.InstanceRerankModel
	}
	if user.RerankTopN > 0 {
		base.RerankTopN = user.RerankTopN
	}
	if user.RerankFieldCharLimit > 0 {
		base.RerankFieldCharLimit = user.RerankFieldCharLimit
	}
}

func mergePropertyFilterConfig(base, user *interfaces.KnSearchPropertyFilterConfig) {
	if user.MaxPropertiesPerInstance > 0 {
		base.MaxPropertiesPerInstance = user.MaxPropertiesPerInstance
	}
	if user.MaxPropertyValueLength > 0 {
		base.MaxPropertyValueLength = user.MaxPropertyValueLength
	}
	if user.EnablePropertyFilter != nil {
		base.EnablePropertyFilter = user.EnablePropertyFilter
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func boolValue(value *bool) bool {
	if value == nil {
		return false
	}
	return *value
}

func float64Ptr(value float64) *float64 {
	return &value
}
