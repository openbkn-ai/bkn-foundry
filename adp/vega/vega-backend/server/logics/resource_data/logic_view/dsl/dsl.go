// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package dsl

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

// logicViewDSLGenerator is used to generate DSLS
type logicViewDSLGenerator struct {
	nodes         map[string]*interfaces.LogicDefinitionNode
	outputNode    *interfaces.LogicDefinitionNode
	nodeFieldsMap map[string]map[string]*interfaces.ViewProperty
	viewFieldMap  map[string]*interfaces.Property
}

// NewlogicViewSQLGenerator creates SQL generators
func NewlogicViewDSLGenerator(view *interfaces.LogicView) *logicViewDSLGenerator {
	nodeMap := make(map[string]*interfaces.LogicDefinitionNode)
	var outputNode *interfaces.LogicDefinitionNode
	nodes := view.LogicDefinition
	for i := range nodes {
		nodeMap[nodes[i].ID] = nodes[i]
		if nodes[i].Type == interfaces.LogicDefinitionNodeType_Output {
			outputNode = nodes[i]
		}
	}

	viewFieldMap := make(map[string]*interfaces.Property)
	for _, field := range view.SchemaDefinition {
		viewFieldMap[field.Name] = field
	}

	return &logicViewDSLGenerator{
		nodes:         nodeMap,
		outputNode:    outputNode,
		nodeFieldsMap: make(map[string]map[string]*interfaces.ViewProperty),
		viewFieldMap:  viewFieldMap,
	}
}

// DSL generator
func (g *logicViewDSLGenerator) BuildDSL(ctx context.Context, query interfaces.ResourceDataQueryParams, view *interfaces.LogicView,
	viewIndicesMap map[string][]string) (interfaces.DSLCfg, error) {
	sortParams := completeDSLSortParams(query.Sort, query.QueryType)

	var dsl interfaces.DSLCfg
	// Set the pagination parameters and track_total_hits
	dsl.From = query.Offset
	dsl.Size = query.Limit
	if query.NeedTotal {
		dsl.TrackTotalHits = true
	}

	if len(sortParams) > 0 {
		sort := []map[string]any{}
		for _, sp := range sortParams {
			if sp.Field == "" || sp.Direction == "" {
				return dsl, rest.NewHTTPError(ctx, http.StatusBadRequest,
					rest.PublicError_BadRequest).
					WithErrorDetails("The sort field and direction cannot be empty")
			}

			sortFieldName := sp.Field
			sortField, ok := g.viewFieldMap[sp.Field]

			if ok {
				if sortField.Type == interfaces.DataType_Binary {
					return dsl, rest.NewHTTPError(ctx, http.StatusBadRequest,
						rest.PublicError_BadRequest).
						WithErrorDetails(fmt.Sprintf("The sort field '%s' is binary type, do not support sorting", sp.Field))
				}

				// For fields of the text type, it is necessary to check whether a keyword index is configured under them. If it is configured, use xxx.keyword for sorting. Otherwise, it will not be included in the ranking
				// string type fields directly support sorting. If they have a full-text index, there will be text under the keyword of the field
				if IsTextType(sortField) {
					if HasFeature(sortField, interfaces.PropertyFeatureType_Keyword) {
						sortFieldName = sortFieldName + ".keyword"
					} else {
						continue
					}
				}
			}

			// The view field __score needs to be converted to the built-in opensearch field _score. For the time being, no modification will be made and compatibility processing will be handled
			if sortFieldName == "__score" {
				sortFieldName = "_score"
			}

			sort = append(sort, map[string]any{
				sortFieldName: sp.Direction,
			})
		}

		dsl.Sort = sort
	}

	// Get the searchAfter parameter
	searchAfterDSL, err := getSearchAfterDSL(nil)
	if err != nil {
		return dsl, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("failed to get search after dsl, %s", err.Error()))
	}

	// Merge searchAfterDSL into the main DSL structure
	dsl.SearchAfter = searchAfterDSL.SearchAfter
	dsl.Pit = searchAfterDSL.Pit

	// Construct query conditions
	queryDSL, err := g.buildDSLQuery(ctx, view, viewIndicesMap)
	if err != nil {
		return dsl, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("failed to build query dsl, %s", err.Error()))
	}

	// Merge the query conditions into the main DSL structure
	dsl.Query = queryDSL.Query

	// Add global filtering conditions. The fields of the global filtering conditions should be in the view field list
	dsl, err = addGlobalFiltersToDSL(ctx, dsl, query.FilterCondCfg, g.viewFieldMap)
	if err != nil {
		return dsl, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("failed to add global filters to dsl, %s", err.Error()))
	}

	logger.Infof("view_indices_map is %v", viewIndicesMap)

	return dsl, nil
}

// Generate the query conditions for the Resource node and return the query condition DSL
func (g *logicViewDSLGenerator) buildResourceQuery(ctx context.Context, node *interfaces.LogicDefinitionNode,
	refResources map[string]*interfaces.Resource, viewIndicesMap map[string][]string) (map[string]any, error) {
	var cfg interfaces.ResourceNodeCfg
	err := mapstructure.Decode(node.Config, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode view node config, %s", err.Error())
	}

	if cfg.ResourceID == "" {
		return nil, fmt.Errorf("resource id is empty")
	}

	indices, exists := viewIndicesMap[cfg.ResourceID]
	if !exists {
		return nil, fmt.Errorf("no indices found for resource ID: %s", cfg.ResourceID)
	}

	indexConditions := map[string]any{
		"terms": map[string]any{
			"_index": indices,
		},
	}

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range refResources[cfg.ResourceID].SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	filterCond, err := g.buildDSLCondition(ctx, cfg.Filters, fieldMap)
	if err != nil {
		return nil, err
	}

	if filterCond == nil {
		return indexConditions, nil
	}

	return map[string]any{
		"bool": map[string]any{
			"must": []any{indexConditions, filterCond},
		},
	}, nil

}

// Add global filtering conditions to the DSL
func addGlobalFiltersToDSL(ctx context.Context, dsl interfaces.DSLCfg, filters *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.DSLCfg, error) {
	// condStr, needScore, err := buildDSLCondition(ctx, filters, fieldsMap)
	// if err != nil {
	// 	return dsl, err
	// }

	// if condStr != "" {
	// 	var filterCondition map[string]any
	// 	if err := sonic.Unmarshal([]byte(condStr), &filterCondition); err != nil {
	// 		return dsl, fmt.Errorf("failed to unmarshal filter condition, %s", err.Error())
	// 	}

	// 	// If scoring is required, use the must query
	// 	if needScore {
	// 		dsl.TrackScores = true
	// 		dsl.Query.Bool.Must = append(dsl.Query.Bool.Must, filterCondition)
	// 	} else {
	// 		dsl.Query.Bool.Filter = append(dsl.Query.Bool.Filter, filterCondition)
	// 	}
	// }

	// return dsl, nil
	return dsl, nil
}

func (g *logicViewDSLGenerator) buildDSLQuery(ctx context.Context, view *interfaces.LogicView,
	viewIndicesMap map[string][]string) (interfaces.DSLCfg, error) {
	// The custom view logic definition cannot be null
	if view.LogicDefinition == nil {
		return interfaces.DSLCfg{}, fmt.Errorf("logic definition is nil")
	}

	// Extract all view nodes
	var viewNodes []*interfaces.LogicDefinitionNode
	for _, node := range view.LogicDefinition {
		switch node.Type {
		case interfaces.LogicDefinitionNodeType_Resource:
			viewNodes = append(viewNodes, node)
		case interfaces.LogicDefinitionNodeType_Union:
			var unionCfg *interfaces.UnionNodeCfg
			err := mapstructure.Decode(node.Config, &unionCfg)
			if err != nil {
				return interfaces.DSLCfg{}, fmt.Errorf("failed to decode union node config, %s", err.Error())
			}

			// The interfaces.DSLCfg class view only allows configuration of all
			if unionCfg.UnionType != interfaces.UnionType_All {
				return interfaces.DSLCfg{}, fmt.Errorf("unsupported union type: %s", unionCfg.UnionType)
			}
		case interfaces.LogicDefinitionNodeType_Output:
		default:
			return interfaces.DSLCfg{}, fmt.Errorf("unsupported node type: %s", node.Type)
		}
	}

	var dsl interfaces.DSLCfg
	// The query structure is determined based on the number of view nodes
	if len(viewNodes) == 1 {
		// For single-view nodes, directly use filter instead of should
		query, err := g.buildResourceQuery(ctx, viewNodes[0], view.RefResources, viewIndicesMap)
		if err != nil {
			return interfaces.DSLCfg{}, err
		}
		dsl.Query.Bool.Filter = []any{query}

	} else {
		// For multi-view nodes, use should
		shouldQueries := make([]any, 0, len(viewNodes))
		for _, node := range viewNodes {
			query, err := g.buildResourceQuery(ctx, node, view.RefResources, viewIndicesMap)
			if err != nil {
				return interfaces.DSLCfg{}, err
			}
			shouldQueries = append(shouldQueries, query)
		}

		dsl.Query.Bool.Should = shouldQueries
		// Set min_should_match to 1 to ensure that at least one should condition is matched
		dsl.Query.Bool.MinShouldMatch = 1
	}

	return dsl, nil
}

// Construct filtering conditions
func (g *logicViewDSLGenerator) buildDSLCondition(ctx context.Context, filters *interfaces.FilterCondCfg,
	fieldMap map[string]*interfaces.Property) (map[string]any, error) {
	// filters 来自视图定义里存的节点配置，是服务端数据、调用方改不了。新的 like 契约拒绝
	// 未转义的 %，直接套到存量定义上会让一次升级把视图查废，因此按老行为改写并告警。
	if marked := filter_condition.MarkLegacyLikeWildcards(filters); marked > 0 {
		logger.Warnf("%d stored like/not_like condition(s) in this logic view use '%%' as a wildcard; "+
			"kept on the pre-change behaviour of this backend. Escape it as '\\%%' or switch the condition to [regex] in the view definition.",
			marked)
	}

	// 将过滤条件拼接到 dsl 的 query 中
	filterCond, err := filter_condition.NewFilterCondition(ctx, filters, fieldMap)
	if err != nil {
		return nil, fmt.Errorf("failed to new condition, %s", err.Error())
	}

	if filterCond == nil {
		return nil, nil
	}

	dslCond, err := g.ConvertFilterCondition(ctx, filterCond, fieldMap)
	if err != nil {
		return nil, fmt.Errorf("failed to convert condition to dsl, %s", err.Error())
	}

	return dslCond, nil
}

// Supplement the "sort" field
func completeDSLSortParams(sort []*interfaces.SortField, queryType string) []*interfaces.SortField {
	defaultSort := []*interfaces.SortField{}
	if queryType == "stream" {
		defaultSort = []*interfaces.SortField{
			{Field: "_id", Direction: interfaces.DESC_DIRECTION},
		}
	}

	sort = append(sort, defaultSort...)
	newSort := []*interfaces.SortField{}
	// duplicate removal
	sortFieldSet := map[string]struct{}{}
	for _, sortParam := range sort {
		if _, ok := sortFieldSet[sortParam.Field]; !ok {
			newSort = append(newSort, sortParam)
			sortFieldSet[sortParam.Field] = struct{}{}
		}
	}

	return newSort
}

// Check whether the field is of text type
func IsTextType(fieldInfo *interfaces.Property) bool {
	return fieldInfo != nil && fieldInfo.Type == interfaces.DataType_Text
}

// Check whether the field features contain the specified features
func HasFeature(fieldInfo *interfaces.Property, feature string) bool {
	for _, f := range fieldInfo.Features {
		if f.FeatureType == feature {
			return true
		}
	}
	return false
}

// Three situations require the splicing of dsl
// 1. There is no pit, but search_after
// 2. There is pit and search_after
// 3. There is pit, but no search_after
func getSearchAfterDSL(searchAfterParams *interfaces.SearchAfterParams) (interfaces.DSLCfg, error) {
	var dsl interfaces.DSLCfg

	if searchAfterParams == nil {
		return dsl, nil
	}

	if len(searchAfterParams.SearchAfter) > 0 {
		dsl.SearchAfter = searchAfterParams.SearchAfter
	}

	// Set pit
	if searchAfterParams.PitID != "" {
		dsl.Pit = &struct {
			ID        string `json:"id,omitempty"`
			KeepAlive string `json:"keep_alive,omitempty"`
		}{}
		dsl.Pit.ID = searchAfterParams.PitID
		if searchAfterParams.PitKeepAlive != "" {
			dsl.Pit.KeepAlive = searchAfterParams.PitKeepAlive
		}
	}

	return dsl, nil

}
