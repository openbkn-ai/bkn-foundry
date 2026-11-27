// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package opensearch provides OpenSearch/ElasticSearch connector implementation.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/shopspring/decimal"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

const singleHavingGroup = "__vega_single_group"

var errSearchTimedOut = errors.New("index search timed out")

type searchShardStats struct {
	Total      int64  `json:"total"`
	Successful int64  `json:"successful"`
	Failed     *int64 `json:"failed"`
}

func (s searchShardStats) complete() bool {
	// OpenSearch includes shards skipped by prefiltering in Successful.
	return s.Total > 0 && s.Successful == s.Total && s.Failed != nil && *s.Failed == 0
}

func validateSearchResponse(body []byte, rejectEarlyTermination bool) error {
	var execution struct {
		TimedOut        *bool            `json:"timed_out"`
		TerminatedEarly bool             `json:"terminated_early"`
		Shards          searchShardStats `json:"_shards"`
	}
	if err := jsoncodec.Unmarshal(body, &execution); err != nil {
		return fmt.Errorf("decode search execution status: %w", err)
	}
	if execution.TimedOut != nil && *execution.TimedOut {
		return errSearchTimedOut
	}
	if execution.TimedOut == nil || !execution.Shards.complete() {
		return errors.New("incomplete index search result")
	}
	if rejectEarlyTermination && execution.TerminatedEarly {
		return errors.New("index search terminated before completion")
	}
	return nil
}

func (c *OpenSearchConnector) searchDocuments(ctx context.Context, indexName string, query map[string]any, snapshot bool) ([]byte, error) {
	queryJSON, err := jsoncodec.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize query: %w", err)
	}
	req := opensearchapi.SearchRequest{
		Body:                      bytes.NewReader(queryJSON),
		AllowPartialSearchResults: new(false),
	}
	if !snapshot {
		req.Index = []string{indexName}
	}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if snapshot && resp.StatusCode == http.StatusNotFound {
		return nil, interfaces.ErrQueryPageSessionExpired
	}
	if resp.IsError() {
		return nil, fmt.Errorf("search failed: %s", resp.String())
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	rejectEarlyTermination := snapshot || query["track_total_hits"] == true || query["aggs"] != nil || query["aggregations"] != nil
	if err := validateSearchResponse(body, rejectEarlyTermination); err != nil {
		return nil, err
	}
	return body, nil
}

func openSearchInt64(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func hasExactSearchTotal(total int64, hasValue bool, relation string) bool {
	return hasValue && total >= 0 && relation == "eq"
}

func searchTotal(hits map[string]any) *int64 {
	count, _ := hits["total"].(map[string]any)
	total, valid := openSearchInt64(count["value"])
	relation, _ := count["relation"].(string)
	if !hasExactSearchTotal(total, valid, relation) {
		return nil
	}
	return &total
}

func openSearchFloat64(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (c *OpenSearchConnector) ExecuteQueryWithDsl(ctx context.Context, resource *interfaces.Resource, dsl string) (*interfaces.ResourceDataQueryResult, error) {
	if resource == nil || strings.TrimSpace(resource.SourceIdentifier) == "" {
		return nil, interfaces.NewConditionBuildError("DSL queries require a Resource source index")
	}
	// Ensure the connector is enabled
	if !c.enabled {
		return nil, fmt.Errorf("OpenSearch connector is not enabled")
	}
	// Ensure we have a connection
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to OpenSearch: %w", err)
	}
	// Validate DSL
	if dsl == "" {
		return nil, fmt.Errorf("DSL query is empty")
	}
	// Parse the DSL to ensure it's valid JSON
	var dslMap map[string]any
	if err := jsoncodec.Unmarshal([]byte(dsl), &dslMap); err != nil {
		return nil, fmt.Errorf("invalid DSL JSON: %w", err)
	}

	responseBody, err := c.searchDocuments(ctx, resource.SourceIdentifier, dslMap, false)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := jsoncodec.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	total := searchTotal(hits)
	if total == nil && dslMap["track_total_hits"] == true {
		return nil, errors.New("index search has no exact total")
	}

	hitsArray, ok := hits["hits"].([]any)
	if !ok {
		return &interfaces.ResourceDataQueryResult{
			Entries:    []map[string]any{},
			TotalCount: total,
		}, nil
	}

	fieldTypes := rawQueryFieldTypes(resource)

	// Extract documents from hits
	documents := make([]map[string]any, 0, len(hitsArray))
	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]any)
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]any)
		if !ok {
			// If _source is not present, create an empty map
			source = make(map[string]any)
		}

		// Add _id to the source
		source["_id"] = hitMap["_id"]

		// Add _score field if present
		if score, ok := openSearchFloat64(hitMap["_score"]); ok {
			source["_score"] = score
		}

		if err := stringifyJSONQueryFields(source, fieldTypes); err != nil {
			return nil, err
		}
		documents = append(documents, source)
	}

	return &interfaces.ResourceDataQueryResult{
		Entries:    documents,
		TotalCount: total,
	}, nil
}

// ExecuteRawQuery executes a raw OpenSearch DSL query on the specified index.
func (c *OpenSearchConnector) ExecuteRawQuery(ctx context.Context, resource *interfaces.Resource, query map[string]any) (*interfaces.RawQueryResponse, error) {
	if resource == nil || strings.TrimSpace(resource.SourceIdentifier) == "" {
		return nil, interfaces.NewConditionBuildError("DSL queries require a Resource source index")
	}
	return c.executeRawQuery(ctx, resource.SourceIdentifier, query, rawQueryFieldTypes(resource), false)
}

func (c *OpenSearchConnector) executeRawQuery(ctx context.Context, indexName string, query map[string]any,
	fieldTypes map[string]string, snapshot bool) (*interfaces.RawQueryResponse, error) {
	aggregationPlan, err := compileRawAggregationPlan(query)
	if err != nil {
		return nil, err
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	responseBody, err := c.searchDocuments(ctx, indexName, query, snapshot)
	if err != nil {
		return nil, err
	}

	// Parse response
	var searchResp struct {
		Hits struct {
			Total struct {
				Value    *int64 `json:"value"`
				Relation any    `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Source map[string]any `json:"_source"`
				Sort   []any          `json:"sort"` // Add the "sort" field
			} `json:"hits"`
		} `json:"hits"`
		Aggregations map[string]any `json:"aggregations"`
	}

	if err := jsoncodec.Unmarshal(responseBody, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	var totalCount int64
	hitsTotal := searchResp.Hits.Total
	if hitsTotal.Value != nil {
		totalCount = *hitsTotal.Value
	}
	if query["track_total_hits"] == true {
		relation, _ := hitsTotal.Relation.(string)
		if !hasExactSearchTotal(totalCount, hitsTotal.Value != nil, relation) {
			return nil, errors.New("index search has no exact total")
		}
	}
	if aggregationPlan != nil {
		entries, err := aggregationPlan.flatten(searchResp.Aggregations)
		if err != nil {
			return nil, err
		}
		return &interfaces.RawQueryResponse{
			Columns:    []interfaces.ColumnInfo{},
			Entries:    entries,
			TotalCount: &totalCount,
		}, nil
	}

	if searchResp.Hits.Hits == nil {
		return nil, errors.New("index search response has no document list")
	}
	if len(searchResp.Hits.Hits) == 0 {
		return &interfaces.RawQueryResponse{
			Columns:    []interfaces.ColumnInfo{},
			Entries:    []map[string]any{},
			TotalCount: &totalCount,
		}, nil
	}

	var columns []interfaces.ColumnInfo
	entries := make([]map[string]any, 0, len(searchResp.Hits.Hits))
	for i, hit := range searchResp.Hits.Hits {
		if i == 0 {
			columns = rawHitColumns(hit.Source, fieldTypes)
		}
		if err := stringifyJSONQueryFields(hit.Source, fieldTypes); err != nil {
			return nil, err
		}
		entries = append(entries, hit.Source)
	}

	// Build response
	// total_count is set to the total amount of data returned by OpenSearch
	response := &interfaces.RawQueryResponse{
		Columns:    columns,
		Entries:    entries,
		TotalCount: &totalCount,
	}

	// If there is a result, check whether search_after needs to be returned
	if len(searchResp.Hits.Hits) > 0 {
		lastHit := searchResp.Hits.Hits[len(searchResp.Hits.Hits)-1]
		// If the last record has a sort value, return it as search_after
		if len(lastHit.Sort) > 0 {
			response.SearchAfter = lastHit.Sort
		}
	}

	return response, nil
}

func rawHitColumns(source map[string]any, fieldTypes map[string]string) []interfaces.ColumnInfo {
	columns := make([]interfaces.ColumnInfo, 0, len(source))
	for fieldName := range source {
		fieldType := interfaces.DataType_String
		if mappedType, ok := fieldTypes[fieldName]; ok {
			fieldType = mappedType
		}
		columns = append(columns, interfaces.ColumnInfo{Name: fieldName, Type: fieldType})
	}
	return columns
}

// rawQueryFieldTypes 按源字段名取类型，显式 schema 优先于探查记录的对象类型。
func rawQueryFieldTypes(resource *interfaces.Resource) map[string]string {
	objects, _ := resource.SourceMetadata["object_fields"].([]any)
	fieldTypes := make(map[string]string, len(resource.SchemaDefinition)+len(objects))
	for _, object := range objects {
		if name, ok := object.(string); ok && name != "" {
			fieldTypes[name] = interfaces.DataType_Json
		}
	}
	for _, field := range resource.SchemaDefinition {
		if field == nil {
			continue
		}
		name := field.OriginalName
		if name == "" {
			name = field.Name
		}
		fieldTypes[name] = field.Type
	}
	return fieldTypes
}

// ExecuteQuery executes a query on the OpenSearch index.
// ExecuteQuery executes OpenSearch queries and returns results
// Parameter
//
//	-ctx: Context information
//	-resource: Resource information, including index names, etc
//	-params: Query parameters, including output fields, sorting, pagination, etc
//
// Return value:
//   - *interfaces.ResourceDataQueryResult: Query result, including row data and total number
//     -error: Error message
func (c *OpenSearchConnector) ExecuteQuery(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {

	// Ensure we have a connection
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to OpenSearch: %w", err)
	}

	if indexName == "" {
		return nil, fmt.Errorf("index name is empty in resource")
	}
	if err := c.validateStoredFilterCondition(resource, params); err != nil {
		return nil, err
	}
	if params.Aggregation != nil && params.Aggregation.Aggr == "count_distinct" &&
		params.Aggregation.AllowApproximate != nil && !*params.Aggregation.AllowApproximate {
		return c.executeExactDistinct(ctx, indexName, resource, params)
	}

	// Aggregation query: Executed when any of the parameters Aggregation, GroupBy, or Having exists
	if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		// Build OpenSearch aggregated queries
		query := map[string]any{
			"size": 0, // Aggregation queries do not need to return documents
		}
		if params.NeedTotal {
			query["track_total_hits"] = true
		}

		// Handle the filtration conditions
		if params.ActualFilterCond != nil {
			filterQuery, err := c.ConvertFilterCondition(params.ActualFilterCond, resource.SchemaDefinition)
			if err != nil {
				return nil, fmt.Errorf("failed to build filter query: %w", err)
			}
			if filterQuery != nil {
				query["query"] = filterQuery
			}
		} else {
			query["query"] = map[string]any{
				"match_all": map[string]any{},
			}
		}

		// Build an aggregated query
		aggs := map[string]any{}

		// Determine the aggregation function and alias
		var aggAlias string
		var metricBody map[string]any
		if params.Aggregation != nil {
			if params.Aggregation.Alias != "" {
				aggAlias = params.Aggregation.Alias
			} else {
				aggAlias = "__value"
			}

			aggField := params.Aggregation.Property
			resolvedField, err := c.exactMatchFieldName(aggField, resource.SchemaDefinition)
			if err != nil {
				return nil, fmt.Errorf("resolve aggregation field %q: %w", aggField, err)
			}
			aggField = resolvedField
			aggFunc := params.Aggregation.Aggr

			switch aggFunc {
			case "count":
				metricBody = map[string]any{
					"value_count": map[string]any{
						"field": aggField,
					},
				}
			case "count_distinct":
				metricBody = map[string]any{
					"cardinality": map[string]any{
						"field":               aggField,
						"precision_threshold": 40000,
					},
				}
			case "sum":
				metricBody = map[string]any{
					"sum": map[string]any{
						"field": aggField,
					},
				}
			case "avg":
				metricBody = map[string]any{
					"avg": map[string]any{
						"field": aggField,
					},
				}
			case "max":
				metricBody = map[string]any{
					"max": map[string]any{
						"field": aggField,
					},
				}
			case "min":
				metricBody = map[string]any{
					"min": map[string]any{
						"field": aggField,
					},
				}
			}
		}

		if len(params.GroupBy) > 0 {
			return c.executeGroupedQuery(ctx, indexName, resource, params, query, metricBody)
		}

		var havingSelector map[string]any
		if params.Having != nil {
			if aggAlias == "" {
				aggAlias = "__value"
			}
			fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
			for _, field := range resource.SchemaDefinition {
				fields[field.Name] = field
			}
			havingField := filter_condition.HavingTemporalField(params, fields)
			if agg := params.Aggregation; agg != nil && (agg.Aggr == "sum" || agg.Aggr == "min" || agg.Aggr == "max") {
				if field := fields[agg.Property]; field != nil && field.Type == interfaces.DataType_Decimal {
					havingField = field
				}
			}
			selector, err := c.buildHavingBucketSelector(params.Having, aggAlias, havingField)
			if err != nil {
				return nil, fmt.Errorf("failed to build HAVING condition: %w", err)
			}
			if params.Aggregation != nil && params.Aggregation.Aggr == "sum" &&
				params.Having.Field != "count(*)" && havingField != nil && havingField.Type == interfaces.DataType_Decimal {
				// 空集合的原生 SUM 为 0；pipeline 的空桶不能将它转换为 NaN。
				selector["bucket_selector"].(map[string]any)["gap_policy"] = "insert_zeros"
			}
			havingSelector = selector
		}

		if havingSelector != nil {
			children := map[string]any{"having_filter": havingSelector}
			if metricBody != nil {
				children[aggAlias] = metricBody
			}
			aggs[singleHavingGroup] = map[string]any{
				"filters": map[string]any{
					"filters": map[string]any{
						"all": map[string]any{
							"match_all": map[string]any{},
						},
					},
				},
				"aggs": children,
			}
		} else if metricBody != nil {
			aggs[aggAlias] = metricBody
		}

		// Add the aggregation to the query
		query["aggs"] = aggs

		logger.Debugf("Executing OpenSearch aggregate query")
		bodyBytes, err := c.searchDocuments(ctx, indexName, query, false)
		if errors.Is(err, errSearchTimedOut) {
			return nil, interfaces.NewQueryExecutionBudgetError("aggregation_time", nil)
		}
		if err != nil {
			return nil, err
		}
		logger.Debugf("OpenSearch aggregate response: %s", string(bodyBytes))

		// Parsing response
		var result map[string]any
		if err := jsoncodec.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode aggregate search result: %w", err)
		}

		hits, _ := result["hits"].(map[string]any)
		totalCount := searchTotal(hits)

		// Extract the aggregation results
		aggregations, ok := result["aggregations"].(map[string]any)
		if !ok {
			if params.Aggregation != nil && params.Aggregation.Aggr == "count_distinct" {
				return nil, fmt.Errorf("distinct count response is missing aggregations")
			}
			return &interfaces.ResourceDataQueryResult{
				Entries:    []map[string]any{},
				TotalCount: totalCount,
			}, nil
		}

		var rows []map[string]any
		if params.Having != nil {
			if group, ok := aggregations[singleHavingGroup].(map[string]any); ok {
				if buckets, ok := group["buckets"].(map[string]any); ok {
					if bucket, retained := buckets["all"].(map[string]any); retained {
						row := make(map[string]any)
						if params.Aggregation != nil {
							c.mergeMetricIntoRowFromBucket(bucket, row, aggAlias)
						} else {
							row["__value"] = bucket["doc_count"]
						}
						rows = append(rows, row)
					}
				}
			}
		} else {
			// There is no grouping, only aggregation
			if params.Aggregation != nil {
				row := make(map[string]any)
				if aggResult, ok := aggregations[aggAlias].(map[string]any); ok {
					if value, ok := aggResult["value"]; ok {
						row[aggAlias] = value
					}
				}
				rows = append(rows, row)
			}
		}

		return &interfaces.ResourceDataQueryResult{
			Entries:       rows,
			TotalCount:    totalCount,
			IsApproximate: params.Aggregation != nil && params.Aggregation.Aggr == "count_distinct",
		}, nil
	}

	query, err := c.buildDocumentQuery(resource, params)
	if err != nil {
		return nil, err
	}
	return c.executeDocumentQuery(ctx, indexName, resource, query, false)
}

func (c *OpenSearchConnector) buildDocumentQuery(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (map[string]any, error) {
	// Build the OpenSearch query
	query := map[string]any{
		"query": map[string]any{
			"match_all": map[string]any{},
		},
		"from": 0,
		"size": 100,
	}

	// Handle output fields (_source)
	if params != nil && len(params.OutputFields) > 0 {
		// Filter out _score field as it's not a source field but a calculated score
		sourceFields := []string{}
		includeScore := false
		for _, field := range params.OutputFields {
			if field != "_score" {
				sourceFields = append(sourceFields, field)
			} else {
				includeScore = true
			}
		}
		if len(sourceFields) > 0 {
			query["_source"] = sourceFields
		}
		// Ensure track_scores is true to get _score when needed
		if includeScore {
			query["track_scores"] = true
		}
	}

	// Handle sorting
	if params != nil && len(params.Sort) > 0 {
		sort := make([]map[string]any, 0, len(params.Sort))
		for _, s := range params.Sort {
			resolvedField := s.Field
			// 文档元字段仅用于排序，不作为普通 schema 字段放行过滤或聚合。
			if s.Field != "_score" && s.Field != "_id" {
				var err error
				resolvedField, err = c.exactMatchFieldName(s.Field, resource.SchemaDefinition)
				if err != nil {
					return nil, fmt.Errorf("resolve sort field %q: %w", s.Field, err)
				}
			}
			sort = append(sort, map[string]any{
				resolvedField: map[string]any{
					"order": s.Direction,
				},
			})
		}
		query["sort"] = sort
	}

	// Handle pagination
	if params != nil {
		if params.NeedTotal {
			query["track_total_hits"] = true
		}
		if params.Paging.Offset > 0 && params.SearchAfter == nil {
			query["from"] = params.Paging.Offset
		}

		if params.Paging.Limit > 0 {
			query["size"] = params.Paging.Limit
		}

		// Handle search_after
		if len(params.SearchAfter) > 0 {
			query["search_after"] = params.SearchAfter
		}
	}

	// Handle filter conditions
	if params != nil && params.ActualFilterCond != nil {
		// Build filter condition query
		filterQuery, err := c.ConvertFilterCondition(params.ActualFilterCond, resource.SchemaDefinition)
		if err != nil {
			return nil, fmt.Errorf("failed to build filter query: %w", err)
		}
		if filterQuery != nil {
			query["query"] = filterQuery
		}
	}

	return query, nil
}

func (c *OpenSearchConnector) executeDocumentQuery(ctx context.Context, indexName string, resource *interfaces.Resource,
	query map[string]any, snapshot bool,
) (*interfaces.ResourceDataQueryResult, error) {
	responseBody, err := c.searchDocuments(ctx, indexName, query, snapshot)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := jsoncodec.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	needTotal := query["track_total_hits"] == true
	total := searchTotal(hits)
	if snapshot && needTotal && total == nil {
		return nil, errors.New("index search has no exact total")
	}

	hitsArray, ok := hits["hits"].([]any)
	if !ok {
		return nil, errors.New("index search response has no document list")
	}
	if needTotal && total != nil && int64(len(hitsArray)) > *total {
		return nil, errors.New("index search returned more documents than its exact total")
	}

	fieldTypes := make(map[string]string, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		if property != nil {
			fieldTypes[property.Name] = property.Type
		}
	}

	// Extract documents from hits
	documents := make([]map[string]any, 0, len(hitsArray))
	var searchAfter []any
	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]any)
		if !ok {
			return nil, errors.New("invalid index search hit")
		}

		source, ok := hitMap["_source"].(map[string]any)
		if !ok {
			return nil, errors.New("index search hit has no _source object")
		}

		source["_id"] = hitMap["_id"]
		// Add _score field if present
		if score, ok := openSearchFloat64(hitMap["_score"]); ok {
			source["_score"] = score
		}
		if err := stringifyJSONQueryFields(source, fieldTypes); err != nil {
			return nil, err
		}
		documents = append(documents, source)
		searchAfter, _ = hitMap["sort"].([]any)
	}

	return &interfaces.ResourceDataQueryResult{
		Entries:     documents,
		TotalCount:  total,
		SearchAfter: searchAfter,
	}, nil
}

// Compile the saved condition independently before building the merged query.
// This preserves error provenance when both saved and caller conditions exist.
func (c *OpenSearchConnector) validateStoredFilterCondition(resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) error {
	if params == nil || params.FixedActualFilterCond == nil {
		return nil
	}
	if _, err := c.ConvertFilterCondition(params.FixedActualFilterCond, resource.SchemaDefinition); err != nil {
		return interfaces.NewStoredConditionBuildError(err)
	}
	return nil
}

func (c *OpenSearchConnector) mergeMetricIntoRowFromBucket(bucket map[string]any, row map[string]any, aggAlias string) {
	if aggAlias == "" {
		return
	}
	if value, ok := bucket[aggAlias]; ok {
		if valueMap, ok := value.(map[string]any); ok {
			if val, ok := valueMap["value"]; ok {
				row[aggAlias] = val
			}
		} else {
			row[aggAlias] = value
		}
	}
}

// buildHavingBucketSelector 使用数值参数比较聚合结果，不把输入值或别名拼入脚本。
func (c *OpenSearchConnector) buildHavingBucketSelector(having *interfaces.HavingClause, aggAlias string, metricField *interfaces.Property) (map[string]any, error) {
	source := ""
	scriptParams := map[string]any{}
	metricPath := aggAlias
	if having.Field == "count(*)" {
		metricPath = "_count"
		metricField = nil
	}
	decimalMetric := metricField != nil && metricField.Type == interfaces.DataType_Decimal
	prefix := ""
	if decimalMetric {
		scale, ok := common.NumberAsInt64(metricField.Attributes["scale"])
		if !ok || scale < 0 || scale > 308 {
			return nil, interfaces.NewConditionBuildError("Decimal HAVING requires a valid field scale")
		}
		scriptParams["scale"] = scale
		prefix = "if (params.metric == null || Double.isNaN(params.metric)) return false; " +
			"BigDecimal metric = BigDecimal.valueOf(params.metric).setScale(params.scale, RoundingMode.HALF_UP); "
	}
	switch having.Operation {
	case "==", "!=", ">", ">=", "<", "<=":
		value, err := indexHavingValue(metricField, having.Value)
		if err != nil {
			return nil, err
		}
		source = "params.metric " + having.Operation + " params.value"
		if decimalMetric {
			source = "return metric.compareTo(new BigDecimal(params.value)) " + having.Operation + " 0;"
		}
		scriptParams["value"] = value
	case "in", "not_in", "range", "out_range":
		var values []any
		switch array := having.Value.(type) {
		case []any:
			values = array
		case []string:
			values = make([]any, len(array))
			for i, value := range array {
				values[i] = value
			}
		}
		if len(values) == 0 {
			return nil, interfaces.NewConditionBuildError("HAVING operation %s requires a non-empty array", having.Operation)
		}
		if (having.Operation == "range" || having.Operation == "out_range") && len(values) != 2 {
			return nil, interfaces.NewConditionBuildError("HAVING range requires exactly two values")
		}
		numeric := make([]any, len(values))
		for i, value := range values {
			parsed, err := indexHavingValue(metricField, value)
			if err != nil {
				return nil, err
			}
			numeric[i] = parsed
		}
		switch having.Operation {
		case "in", "not_in":
			scriptParams["values"] = numeric
			source = "for (def value : params.values) { if (params.metric == value) return true; } return false;"
			if having.Operation == "not_in" {
				source = "for (def value : params.values) { if (params.metric == value) return false; } return true;"
			}
			if decimalMetric {
				source = "for (def value : params.values) { if (metric.compareTo(new BigDecimal(value)) == 0) return true; } return false;"
				if having.Operation == "not_in" {
					source = "for (def value : params.values) { if (metric.compareTo(new BigDecimal(value)) == 0) return false; } return true;"
				}
			}
		case "range", "out_range":
			scriptParams["lower"], scriptParams["upper"] = numeric[0], numeric[1]
			source = "params.metric >= params.lower && params.metric <= params.upper"
			if having.Operation == "out_range" {
				source = "params.metric < params.lower || params.metric > params.upper"
			}
			if decimalMetric {
				source = "return metric.compareTo(new BigDecimal(params.lower)) >= 0 && metric.compareTo(new BigDecimal(params.upper)) <= 0;"
				if having.Operation == "out_range" {
					source = "return metric.compareTo(new BigDecimal(params.lower)) < 0 || metric.compareTo(new BigDecimal(params.upper)) > 0;"
				}
			}
		}
	default:
		return nil, interfaces.NewConditionBuildError("unsupported HAVING operation: %s", having.Operation)
	}
	return map[string]any{
		"bucket_selector": map[string]any{
			"buckets_path": map[string]any{"metric": metricPath},
			"script":       map[string]any{"source": prefix + source, "params": scriptParams},
		},
	}, nil
}

// indexHavingValue 保留 Decimal 阈值的十进制文本；日期聚合使用 epoch milliseconds，包括 date_nanos。
// 保留输入的亚毫秒部分；最终比较精度受 OpenSearch double 聚合值限制。
func indexHavingValue(field *interfaces.Property, value any) (any, error) {
	if field != nil && field.Type == interfaces.DataType_Decimal {
		text := fmt.Sprint(value)
		if len(text) > 1024 {
			return nil, interfaces.NewConditionBuildError("Decimal HAVING value is too large")
		}
		number, err := decimal.NewFromString(text)
		if err != nil || number.Exponent() < -1024 || number.Exponent() > 1024 {
			return nil, interfaces.NewConditionBuildError("Decimal HAVING value must be a finite decimal")
		}
		return number.String(), nil
	}
	if field != nil {
		parsed, err := filter_condition.ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		if err != nil {
			return nil, err
		}
		var instant time.Time
		switch field.Type {
		case interfaces.DataType_Datetime:
			instant = parsed.(time.Time)
		case interfaces.DataType_Date:
			instant, err = time.Parse(time.DateOnly, parsed.(string))
			if err != nil {
				return nil, err
			}
		default:
			return nil, interfaces.NewConditionBuildError("OpenSearch time fields do not support temporal aggregation")
		}
		return float64(instant.UnixMilli()) + float64(instant.Nanosecond()%int(time.Millisecond))/float64(time.Millisecond), nil
	}
	number, ok := common.NumberAsFloat64(value)
	if text, isString := value.(string); isString {
		var err error
		number, err = strconv.ParseFloat(text, 64)
		ok = err == nil
	}
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, interfaces.NewConditionBuildError("HAVING value must be a finite number")
	}
	return number, nil
}

// stringifyJSONQueryFields 只序列化协议已解码的 JSON 字段；文本和空值保持原样。
func stringifyJSONQueryFields(source map[string]any, fieldTypes map[string]string) error {
	names := make([]string, 0, len(fieldTypes))
	for name, fieldType := range fieldTypes {
		if fieldType == interfaces.DataType_Json {
			names = append(names, name)
		}
	}
	// 父 JSON 字段先序列化，避免对子字段转换后再次编码而改变原值。
	sort.Slice(names, func(i, j int) bool {
		iDepth, jDepth := strings.Count(names[i], "."), strings.Count(names[j], ".")
		if iDepth != jDepth {
			return iDepth < jDepth
		}
		return names[i] < names[j]
	})
	var visit func(any, string) error
	visit = func(node any, path string) error {
		switch node := node.(type) {
		case []any:
			for _, item := range node {
				if err := visit(item, path); err != nil {
					return err
				}
			}
		case map[string]any:
			// 优先匹配完整键，保留 _source 中含字面点号的字段语义。
			if value, exists := node[path]; exists {
				text, err := common.JSONQueryValueText(value)
				if err != nil {
					return fmt.Errorf("failed to serialize JSON field %q", path)
				}
				node[path] = text
				return nil
			}
			// 最长前缀优先，同时支持嵌套对象及中间节点的字面点号。
			for split := strings.LastIndex(path, "."); split > 0; split = strings.LastIndex(path[:split], ".") {
				if child, exists := node[path[:split]]; exists {
					return visit(child, path[split+1:])
				}
			}
		}
		return nil
	}
	for _, name := range names {
		if err := visit(source, name); err != nil {
			return err
		}
	}
	return nil
}
