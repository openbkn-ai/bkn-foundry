// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/creasty/defaults"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	aerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

var newBknBackendAccess = drivenadapters.NewBknBackendAccess

// SearchSchema normalizes the request, delegates to KnSearch, and filters the response.
func (s *knSearchService) SearchSchema(ctx context.Context, req *interfaces.SearchSchemaReq) (*interfaces.SearchSchemaResp, error) {
	knReq, scope, err := NormalizeSearchSchemaReq(req)
	if err != nil {
		return nil, aerrors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}

	resp, err := s.KnSearch(ctx, knReq)
	if err != nil {
		return nil, err
	}

	metricTypes := []any{}
	if scope.IncludeMetricTypes {
		metricTypes, err = s.resolveMetricTypes(ctx, req, scope, resp)
		if err != nil {
			return nil, err
		}
	}

	filtered := FilterSearchSchemaResp(resp, metricTypes, scope, *req.MaxConcepts)
	bkntrace.EmitSearchSchemaEvents(ctx, s.Logger, req, filtered)
	return filtered, nil
}

// SearchSchemaScope holds the resolved boolean flags for output filtering.
type SearchSchemaScope struct {
	ConceptGroups        []string
	IncludeObjectTypes   bool
	IncludeRelationTypes bool
	IncludeActionTypes   bool
	IncludeMetricTypes   bool
}

// NormalizeSearchSchemaReq converts a SearchSchemaReq into KnSearchReq + scope.
// It applies struct default tags, validates inputs, and forces schema-only semantics.
func NormalizeSearchSchemaReq(req *interfaces.SearchSchemaReq) (*interfaces.KnSearchReq, SearchSchemaScope, error) {
	if err := defaults.Set(req); err != nil {
		return nil, SearchSchemaScope{}, errors.New("failed to apply defaults: " + err.Error())
	}

	// SearchScope 为 nil 时（用户未传），默认四类全开；
	// 非 nil 时 defaults.Set 已填充子字段。
	scope := SearchSchemaScope{
		IncludeObjectTypes:   true,
		IncludeRelationTypes: true,
		IncludeActionTypes:   true,
		IncludeMetricTypes:   true,
	}
	if req.SearchScope != nil {
		scope.ConceptGroups = normalizeConceptGroups(req.SearchScope.ConceptGroups)
		scope.IncludeObjectTypes = *req.SearchScope.IncludeObjectTypes
		scope.IncludeRelationTypes = *req.SearchScope.IncludeRelationTypes
		scope.IncludeActionTypes = *req.SearchScope.IncludeActionTypes
		scope.IncludeMetricTypes = *req.SearchScope.IncludeMetricTypes
	}
	if !scope.IncludeObjectTypes && !scope.IncludeRelationTypes && !scope.IncludeActionTypes && !scope.IncludeMetricTypes {
		return nil, scope, errors.New("search_scope must enable at least one concept type")
	}

	knID := strings.TrimSpace(req.XKnID)
	if knID == "" {
		knID = strings.TrimSpace(req.KnID)
	}
	if knID == "" {
		return nil, scope, errors.New("kn_id is required (configure X-Kn-ID header or pass kn_id in body)")
	}

	if strings.TrimSpace(req.Query) == "" {
		return nil, scope, errors.New("query is required")
	}

	if *req.MaxConcepts <= 0 {
		return nil, scope, errors.New("max_concepts must be greater than 0")
	}

	onlySchema := true
	return &interfaces.KnSearchReq{
		XAccountID:     req.XAccountID,
		XAccountType:   req.XAccountType,
		Query:          req.Query,
		KnID:           knID,
		OnlySchema:     &onlySchema,
		EnableRerank:   req.EnableRerank,
		RerankModel:    req.RerankModel,
		IncludeColumns: req.IncludeColumns,
		IndexOpsOnly:   req.IndexOpsOnly,
		RetrievalConfig: &interfaces.RetrievalConfig{
			ConceptRetrieval: &interfaces.ConceptRetrievalConfig{
				ConceptGroups: scope.ConceptGroups,
				TopK:          *req.MaxConcepts,
				SchemaBrief:   *req.SchemaBrief,
			},
		},
	}, scope, nil
}

// FilterSearchSchemaResp builds a SearchSchemaResp from KnSearchResp, applying scope filtering.
func FilterSearchSchemaResp(resp *interfaces.KnSearchResp, metricTypes []any, scope SearchSchemaScope, maxConcepts int) *interfaces.SearchSchemaResp {
	objectTypes := []any{}
	relationTypes := []any{}
	actionTypes := []any{}
	if resp != nil {
		objectTypes = toAnySlice(resp.ObjectTypes)
		relationTypes = toAnySlice(resp.RelationTypes)
		actionTypes = toAnySlice(resp.ActionTypes)
	}

	if scope.IncludeRelationTypes {
		relationTypes = limitAnySlice(relationTypes, maxConcepts)
	}
	if scope.IncludeObjectTypes {
		if scope.IncludeRelationTypes && len(relationTypes) > 0 {
			objectTypes = limitObjectTypesKeepingRelationEndpoints(objectTypes, relationTypes, maxConcepts)
		} else {
			objectTypes = limitAnySlice(objectTypes, maxConcepts)
		}
	}

	result := &interfaces.SearchSchemaResp{
		ObjectTypes:   []any{},
		RelationTypes: []any{},
		ActionTypes:   []any{},
		MetricTypes:   []any{},
	}
	if scope.IncludeObjectTypes {
		result.ObjectTypes = objectTypes
	}
	if scope.IncludeRelationTypes {
		result.RelationTypes = relationTypes
	}
	if scope.IncludeActionTypes {
		result.ActionTypes = actionTypes
	}
	if scope.IncludeMetricTypes {
		result.MetricTypes = limitAnySlice(metricTypes, maxConcepts)
	}
	return result
}

func (s *knSearchService) resolveMetricTypes(ctx context.Context, req *interfaces.SearchSchemaReq, scope SearchSchemaScope, resp *interfaces.KnSearchResp) ([]any, error) {
	backend := newBknBackendAccess()
	if backend == nil {
		return []any{}, nil
	}

	directReq := buildMetricRecallQuery(strings.TrimSpace(req.KnID), strings.TrimSpace(req.Query), *req.MaxConcepts, scope.ConceptGroups)
	if strings.TrimSpace(req.XKnID) != "" {
		directReq.KnID = strings.TrimSpace(req.XKnID)
	}

	directResp, err := backend.SearchMetricTypes(ctx, directReq)
	if err != nil {
		return nil, err
	}

	objectIDs := extractObjectCandidateIDs(resp)
	expansionMetrics := []*interfaces.MetricType{}
	if len(objectIDs) > 0 {
		expansionReq := buildMetricExpansionQuery(directReq.KnID, strings.TrimSpace(req.Query), objectIDs, *req.MaxConcepts, scope.ConceptGroups)
		expansionResp, expansionErr := backend.SearchMetricTypes(ctx, expansionReq)
		if expansionErr != nil {
			s.Logger.WithContext(ctx).Warnf("[SearchSchema] metric expansion failed, fallback to direct recall: %v", expansionErr)
		} else if expansionResp != nil {
			expansionMetrics = expansionResp.Entries
		}
	}

	directMetrics := []*interfaces.MetricType{}
	if directResp != nil {
		directMetrics = directResp.Entries
	}

	return toAnySlice(mergeMetricTypesByID(directMetrics, expansionMetrics, *req.MaxConcepts)), nil
}

func buildMetricRecallQuery(knID, query string, limit int, conceptGroups []string) *interfaces.QueryConceptsReq {
	return &interfaces.QueryConceptsReq{
		KnID:          knID,
		ConceptGroups: normalizeConceptGroups(conceptGroups),
		Cond: &interfaces.KnCondition{
			Operation: interfaces.KnOperationTypeOr,
			SubConditions: []*interfaces.KnCondition{
				{
					Field:      "*",
					Operation:  interfaces.KnOperationTypeKnn,
					Value:      query,
					ValueFrom:  interfaces.CondValueFromConst,
					LimitKey:   interfaces.CondLimitKeyK,
					LimitValue: limit,
				},
				{
					Field:     "*",
					Operation: interfaces.KnOperationTypeMatch,
					Value:     query,
					ValueFrom: interfaces.CondValueFromConst,
				},
			},
		},
		Sort: []*interfaces.KnSortParams{
			{Field: "_score", Direction: "desc"},
		},
		Limit:     limit,
		NeedTotal: false,
	}
}

func buildMetricExpansionQuery(knID, query string, objectIDs []string, limit int, conceptGroups []string) *interfaces.QueryConceptsReq {
	return &interfaces.QueryConceptsReq{
		KnID:          knID,
		ConceptGroups: normalizeConceptGroups(conceptGroups),
		Cond: &interfaces.KnCondition{
			Operation: interfaces.KnOperationTypeAnd,
			SubConditions: []*interfaces.KnCondition{
				{
					Field:     "scope_type",
					Operation: interfaces.KnOperationTypeEqual,
					Value:     "object_type",
					ValueFrom: interfaces.CondValueFromConst,
				},
				{
					Field:     "scope_ref",
					Operation: interfaces.KnOperationTypeIn,
					Value:     objectIDs,
					ValueFrom: interfaces.CondValueFromConst,
				},
				buildMetricRecallQuery(knID, query, limit, conceptGroups).Cond,
			},
		},
		Sort: []*interfaces.KnSortParams{
			{Field: "_score", Direction: "desc"},
		},
		Limit:     limit,
		NeedTotal: false,
	}
}

func extractObjectCandidateIDs(resp *interfaces.KnSearchResp) []string {
	if resp == nil {
		return nil
	}
	objectTypes := toAnySlice(resp.ObjectTypes)
	if len(objectTypes) == 0 {
		return nil
	}

	objectIDs := make([]string, 0, len(objectTypes))
	seen := make(map[string]struct{}, len(objectTypes))
	for _, obj := range objectTypes {
		objMap, ok := obj.(map[string]any)
		if !ok {
			continue
		}
		conceptID, ok := objMap["concept_id"].(string)
		if !ok || conceptID == "" {
			continue
		}
		if _, exists := seen[conceptID]; exists {
			continue
		}
		seen[conceptID] = struct{}{}
		objectIDs = append(objectIDs, conceptID)
	}
	return objectIDs
}

func mergeMetricTypesByID(directMetrics, expansionMetrics []*interfaces.MetricType, limit int) []*interfaces.MetricType {
	merged := make([]*interfaces.MetricType, 0, len(directMetrics)+len(expansionMetrics))
	seen := make(map[string]struct{}, len(directMetrics)+len(expansionMetrics))

	appendMetric := func(metric *interfaces.MetricType) {
		if metric == nil || metric.ID == "" {
			return
		}
		if _, exists := seen[metric.ID]; exists {
			return
		}
		seen[metric.ID] = struct{}{}
		merged = append(merged, metric)
	}

	for _, metric := range directMetrics {
		appendMetric(metric)
	}
	for _, metric := range expansionMetrics {
		appendMetric(metric)
	}

	if limit > 0 && len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

func toAnySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return []any{}
	}
	var slice []any
	if err := json.Unmarshal(data, &slice); err != nil {
		return []any{}
	}
	return slice
}

func limitAnySlice(items []any, limit int) []any {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

// limitObjectTypesKeepingRelationEndpoints 按相关性截断对象类，同时补齐返回关系的端点。
//
// 两条约束的优先级：
//  1. 相关性优先——检索层已按对象类自身与 query 的相关性排好序，先取前 limit 个。
//     修复前这里会把关系端点整体提到最前，把相关性排序又打乱一次，导致与查询最匹配
//     的对象类被挤出响应（issue #778）。
//  2. 自洽兜底——返回的关系若指向未返回的对象，调用方拿到的是断头引用，因此缺失的
//     端点一律补上，允许超出 limit。这是既有契约，本次修复保留。
func limitObjectTypesKeepingRelationEndpoints(objectTypes, relationTypes []any, limit int) []any {
	if len(objectTypes) == 0 {
		return objectTypes
	}
	if len(relationTypes) == 0 {
		return limitAnySlice(objectTypes, limit)
	}

	conceptIDOf := func(item any) string {
		itemMap, ok := item.(map[string]any)
		if !ok {
			return ""
		}
		conceptID, _ := itemMap["concept_id"].(string)
		return conceptID
	}

	// 必须拷贝：limitAnySlice 返回的是 objectTypes 的子切片，直接 append 会写坏原切片。
	head := limitAnySlice(objectTypes, limit)
	out := make([]any, len(head), len(objectTypes))
	copy(out, head)

	included := make(map[string]struct{}, len(out))
	for _, obj := range out {
		if conceptID := conceptIDOf(obj); conceptID != "" {
			included[conceptID] = struct{}{}
		}
	}

	objectByID := make(map[string]any, len(objectTypes))
	for _, obj := range objectTypes {
		if conceptID := conceptIDOf(obj); conceptID != "" {
			if _, exists := objectByID[conceptID]; !exists {
				objectByID[conceptID] = obj
			}
		}
	}

	appendEndpoint := func(conceptID string) {
		if conceptID == "" {
			return
		}
		if _, ok := included[conceptID]; ok {
			return
		}
		obj, ok := objectByID[conceptID]
		if !ok {
			return
		}
		included[conceptID] = struct{}{}
		out = append(out, obj)
	}

	for _, rel := range relationTypes {
		relMap, ok := rel.(map[string]any)
		if !ok {
			continue
		}
		sourceID, _ := relMap["source_object_type_id"].(string)
		targetID, _ := relMap["target_object_type_id"].(string)
		appendEndpoint(sourceID)
		appendEndpoint(targetID)
	}

	return out
}
