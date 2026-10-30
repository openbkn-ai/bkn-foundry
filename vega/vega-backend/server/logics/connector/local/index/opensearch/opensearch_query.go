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
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

const singleHavingGroup = "__vega_single_group"

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

func (c *OpenSearchConnector) ExecuteQueryWithDsl(ctx context.Context, resourceName string, dsl string) (*interfaces.QueryResult, error) {
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
	if err := common.UnmarshalPreciseJSON([]byte(dsl), &dslMap); err != nil {
		return nil, fmt.Errorf("invalid DSL JSON: %w", err)
	}

	// Execute search request with the provided DSL
	// resourceID is used as the index name
	req := opensearchapi.SearchRequest{
		Index: []string{resourceName},
		Body:  strings.NewReader(dsl),
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("search failed: %s", resp.String())
	}

	// Parse response
	var result map[string]any
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	if err := common.UnmarshalPreciseJSON(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	// Extract total count
	var total int64
	if totalMap, ok := hits["total"].(map[string]any); ok {
		if value, ok := openSearchInt64(totalMap["value"]); ok {
			total = value
		}
	}

	hitsArray, ok := hits["hits"].([]any)
	if !ok {
		return &interfaces.QueryResult{
			Entries: []map[string]any{},
			Total:   total,
		}, nil
	}

	fieldTypes := make(map[string]string)
	if err := c.fetchMappingsForQuery(ctx, resourceName, fieldTypes); err != nil {
		return nil, fmt.Errorf("failed to read JSON field types: %w", err)
	}

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

	return &interfaces.QueryResult{
		Entries: documents,
		Total:   total,
	}, nil
}

// ExecuteRawQuery executes a raw OpenSearch DSL query on the specified index.
func (c *OpenSearchConnector) ExecuteRawQuery(ctx context.Context, indexName string, query map[string]any) (*interfaces.RawQueryResponse, error) {
	aggregationPlan, err := compileRawAggregationPlan(query)
	if err != nil {
		return nil, err
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	// Convert query to JSON
	queryJSON, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	logger.Debugf("Executing OpenSearch DSL query")

	// Create search request
	req := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(queryJSON)),
	}

	// Execute search
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("opensearch API error: %s", resp.String())
	}

	// Parse response
	var searchResp struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source map[string]any `json:"_source"`
				Sort   []any          `json:"sort"` // Add the "sort" field
			} `json:"hits"`
		} `json:"hits"`
		Aggregations map[string]any `json:"aggregations"`
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read raw query response: %w", err)
	}
	if err := common.UnmarshalPreciseJSON(responseBody, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	totalCount := searchResp.Hits.Total.Value
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

	// If no hits, return empty result
	if len(searchResp.Hits.Hits) == 0 {
		return &interfaces.RawQueryResponse{
			Columns:    []interfaces.ColumnInfo{},
			Entries:    []map[string]any{},
			TotalCount: &totalCount,
		}, nil
	}

	// Obtain the mapping information of the index to determine the field type
	fieldTypeMap := make(map[string]string)
	if err := c.fetchMappingsForQuery(ctx, indexName, fieldTypeMap); err != nil {
		return nil, fmt.Errorf("failed to read JSON field types: %w", err)
	}

	// Collect all field names from the first hit
	firstHit := searchResp.Hits.Hits[0].Source
	columns := make([]interfaces.ColumnInfo, 0, len(firstHit))
	for fieldName := range firstHit {
		fieldType := "string" // Default type
		if mappedType, ok := fieldTypeMap[fieldName]; ok {
			fieldType = mappedType
		}
		columns = append(columns, interfaces.ColumnInfo{
			Name: fieldName,
			Type: fieldType,
		})
	}

	// Convert hits to entries
	entries := make([]map[string]any, 0, len(searchResp.Hits.Hits))
	for _, hit := range searchResp.Hits.Hits {
		if err := stringifyJSONQueryFields(hit.Source, fieldTypeMap); err != nil {
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

// fetchMappingsForQuery retrieves the mapping information of the index and builds the field type mapping
func (c *OpenSearchConnector) fetchMappingsForQuery(ctx context.Context, indexName string, fieldTypeMap map[string]string) error {
	req := opensearchapi.IndicesGetMappingRequest{
		Index: []string{indexName},
	}
	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return fmt.Errorf("opensearch API error: %s", resp.String())
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse JSON
	var dataMapping map[string]struct {
		Mappings struct {
			Properties map[string]Property `json:"properties"`
		} `json:"mappings"`
	}
	if err := sonic.Unmarshal(bodyBytes, &dataMapping); err != nil {
		return fmt.Errorf("failed to unmarshal mappings: %w", err)
	}

	// 别名和通配符响应使用实际索引名，逐索引解析后再合并类型。
	indexNames := make([]string, 0, len(dataMapping))
	for name := range dataMapping {
		indexNames = append(indexNames, name)
	}
	sort.Strings(indexNames)
	mergedTypes := make(map[string]string)
	for _, name := range indexNames {
		properties := dataMapping[name].Mappings.Properties
		fields := make(map[string]interfaces.IndexFieldMeta)
		parseProperties("", properties, fields)
		// 发现流程展开 object 子字段；查询还需保留顶层 object 的 JSON 类型。
		for fieldName, property := range properties {
			if property.Type == "object" || (property.Type == "" && property.Properties != nil) {
				fields[fieldName] = interfaces.IndexFieldMeta{Type: "object"}
			}
		}
		resolveIndexAliasTypes(fields)
		fieldNames := make([]string, 0, len(fields))
		for fieldName := range fields {
			fieldNames = append(fieldNames, fieldName)
		}
		sort.Strings(fieldNames)
		for _, fieldName := range fieldNames {
			meta := fields[fieldName]
			nativeType := meta.Type
			if meta.ResolvedType != "" {
				nativeType = meta.ResolvedType
			}
			fieldType := c.MapType(nativeType)
			if previous, exists := mergedTypes[fieldName]; exists && previous != fieldType {
				return fmt.Errorf("conflicting mapping types for field %q", fieldName)
			}
			mergedTypes[fieldName] = fieldType
		}
	}
	for fieldName, fieldType := range mergedTypes {
		fieldTypeMap[fieldName] = fieldType
	}

	return nil
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
//   - *interfaces.QueryResult: Query result, including row data and total number
//     -error: Error message
func (c *OpenSearchConnector) ExecuteQuery(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

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

	// Aggregation query: Executed when any of the parameters Aggregation, GroupBy, or Having exists
	if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		countAll := params.Aggregation != nil && params.Aggregation.Aggr == "count" && params.Aggregation.Property == "*"
		if countAll && (len(params.GroupBy) != 0 || params.Having != nil) {
			return nil, fmt.Errorf("count(*) with grouping or having is not supported by this query path")
		}
		// Build OpenSearch aggregated queries
		query := map[string]any{
			"size": 0, // Aggregation queries do not need to return documents
		}
		if params.NeedTotal || countAll {
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
			if !countAll {
				resolvedField, err := c.exactMatchFieldName(aggField, resource.SchemaDefinition)
				if err != nil {
					return nil, fmt.Errorf("resolve aggregation field %q: %w", aggField, err)
				}
				aggField = resolvedField
			}
			aggFunc := params.Aggregation.Aggr

			switch aggFunc {
			case "count":
				if !countAll {
					metricBody = map[string]any{
						"value_count": map[string]any{"field": aggField},
					}
				}
			case "count_distinct":
				metricBody = map[string]any{
					"cardinality": map[string]any{
						"field": aggField,
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

		var havingSelector map[string]any
		if params.Having != nil {
			if aggAlias == "" {
				aggAlias = "__value"
			}
			fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
			for _, field := range resource.SchemaDefinition {
				fields[field.Name] = field
			}
			selector, err := c.buildHavingBucketSelector(params.Having, aggAlias, filter_condition.HavingTemporalField(params, fields))
			if err != nil {
				return nil, fmt.Errorf("failed to build HAVING condition: %w", err)
			}
			havingSelector = selector
		}

		// HAVING 作用于分组末级；无 GROUP BY 时使用单桶 filters，仍在引擎内执行过滤。
		if len(params.GroupBy) > 0 {
			leafAggs := make(map[string]any)
			if metricBody != nil {
				leafAggs[aggAlias] = metricBody
			}
			if havingSelector != nil {
				leafAggs["having_filter"] = havingSelector
			}

			innerNode := leafAggs
			n := len(params.GroupBy)
			for i := n - 1; i >= 0; i-- {
				gb := params.GroupBy[i]
				name := "group_by_" + gb.Property
				var bucket map[string]any
				if gb.CalendarInterval != "" {
					bucket = map[string]any{
						"date_histogram": map[string]any{
							"field":             gb.Property,
							"calendar_interval": gb.CalendarInterval,
						},
					}
				} else {
					groupField := gb.Property
					resolvedField, err := c.exactMatchFieldName(groupField, resource.SchemaDefinition)
					if err != nil {
						return nil, fmt.Errorf("resolve group_by field %q: %w", groupField, err)
					}
					groupField = resolvedField
					bucket = map[string]any{
						"terms": map[string]any{
							"field": groupField,
							"size":  nestedTermsSize(i, n, params.Paging.Limit),
						},
					}
				}
				if len(innerNode) > 0 {
					bucket["aggs"] = innerNode
				}
				innerNode = map[string]any{name: bucket}
			}
			for k, v := range innerNode {
				aggs[k] = v
			}
			// Apply the order mapped to sort to each layer of terms (the second-dimensional sort is written on the inner layer terms)
			for _, v := range aggs {
				if node, ok := v.(map[string]any); ok {
					c.applyTermsOrderToGroupAggNode(node, params, aggAlias)
				}
			}
		} else if havingSelector != nil {
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
		if len(aggs) > 0 {
			query["aggs"] = aggs
		}

		// Serialized query
		queryJSON, err := sonic.Marshal(query)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize aggregate query: %w", err)
		}

		logger.Debugf("Executing OpenSearch aggregate query")

		// Execute the search request
		req := opensearchapi.SearchRequest{
			Index: []string{indexName},
			Body:  bytes.NewReader(queryJSON),
		}

		resp, err := req.Do(ctx, c.client)
		if err != nil {
			return nil, fmt.Errorf("failed to execute aggregate search: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.IsError() {
			return nil, fmt.Errorf("aggregate search failed: %s", resp.String())
		}

		// Read the response body for diagnostic logging.
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		logger.Debugf("OpenSearch aggregate response: %s", string(bodyBytes))

		// Parsing response
		var result map[string]any
		if err := common.UnmarshalPreciseJSON(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode aggregate search result: %w", err)
		}

		// Extract the total number of documents
		var totalCount int64
		hasExactTotal := false
		if hits, ok := result["hits"].(map[string]any); ok {
			if totalMap, ok := hits["total"].(map[string]any); ok {
				if value, ok := openSearchInt64(totalMap["value"]); ok {
					totalCount = value
					hasExactTotal = totalMap["relation"] == "eq"
				}
			}
		}
		if countAll {
			if !hasExactTotal {
				return nil, fmt.Errorf("count(*) did not receive an exact total hit count")
			}
			alias := params.Aggregation.Alias
			if alias == "" {
				alias = "__value"
			}
			return &interfaces.QueryResult{Entries: []map[string]any{
				{alias: json.Number(strconv.FormatInt(totalCount, 10))}}, Total: totalCount}, nil
		}

		// Extract the aggregation results
		aggregations, ok := result["aggregations"].(map[string]any)
		if !ok {
			return &interfaces.QueryResult{
				Entries: []map[string]any{},
				Total:   totalCount,
			}, nil
		}

		// Handle group aggregation results (support multi-level group_by nested bucket flattening)
		var rows []map[string]any
		if len(params.GroupBy) > 0 {
			groupByAggName := "group_by_" + params.GroupBy[0].Property
			if groupByAgg, ok := aggregations[groupByAggName].(map[string]any); ok {
				rows = c.flattenNestedGroupByRows(groupByAgg, params, aggAlias)
			}
		} else if params.Having != nil {
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

		return &interfaces.QueryResult{
			Entries: rows,
			Total:   totalCount,
		}, nil
	}

	// Detailed inquiry
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

	// Serialize query
	queryJSON, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize query: %w", err)
	}
	logger.Debugf("Executing OpenSearch query")

	// Execute search request
	req := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  bytes.NewReader(queryJSON),
	}

	resp, err := req.Do(ctx, c.client)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.IsError() {
		return nil, fmt.Errorf("search failed: %s", resp.String())
	}

	// Parse response
	var result map[string]any
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	if err := common.UnmarshalPreciseJSON(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	total, _ := openSearchInt64(hits["total"].(map[string]any)["value"])

	hitsArray, ok := hits["hits"].([]any)
	if !ok {
		return &interfaces.QueryResult{
			Entries: []map[string]any{},
			Total:   int64(total),
		}, nil
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
			continue
		}

		source, ok := hitMap["_source"].(map[string]any)
		if !ok {
			continue
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
		if sort, ok := hitMap["sort"].([]any); ok {
			searchAfter = sort
		}
	}

	return &interfaces.QueryResult{
		Entries:     documents,
		Total:       int64(total),
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

// nestedTermsSize sets the size for each layer of terms in the nested group_by: the innermost layer uses limit to control the number of rows "under each parent bucket", and the outer layer uses a larger upper limit to expand the combination.
func nestedTermsSize(levelIndex, numLevels, limit int) int {
	if numLevels <= 1 {
		if limit > 0 {
			return limit
		}
		return 10
	}
	if levelIndex == numLevels-1 {
		if limit > 0 {
			return limit
		}
		return 10
	}
	outer := 1000
	if limit > 0 {
		if x := limit * 100; x > 10000 {
			outer = 10000
		} else if x < 100 {
			outer = 100
		} else {
			outer = x
		}
	}
	return outer
}

// ApplyTermsOrderToGroupAggNode recursive terms for each subtree of barrel written order (dimensional layer 2 d sort to fall, when the terms).
// Sorting by metric only takes effect when the direct sub-aggs of that term contain the metric name, avoiding the illegal DSL caused by the outer terms referring to deeply nested sub-aggregations.
func (c *OpenSearchConnector) applyTermsOrderToGroupAggNode(node map[string]any, params *interfaces.ResourceDataQueryParams, aggAlias string) {
	if terms, ok := node["terms"].(map[string]any); ok {
		field, _ := terms["field"].(string)
		sub, _ := node["aggs"].(map[string]any)
		metricDirectChild := aggAlias != "" && sub != nil && sub[aggAlias] != nil

		var orderList []map[string]any
		for _, sortItem := range params.Sort {
			dir := strings.ToLower(sortItem.Direction)
			if dir != "asc" && dir != "desc" {
				dir = "asc"
			}
			if params.Aggregation != nil && metricDirectChild && (sortItem.Field == aggAlias || sortItem.Field == "__value") {
				orderList = append(orderList, map[string]any{aggAlias: dir})
			}
			if sortItem.Field == field {
				orderList = append(orderList, map[string]any{"_key": dir})
			}
		}
		if len(orderList) > 0 {
			terms["order"] = orderList
		}
	}
	sub, ok := node["aggs"].(map[string]any)
	if !ok {
		return
	}
	for name, child := range sub {
		if name == "having_filter" {
			continue
		}
		if childMap, ok := child.(map[string]any); ok {
			c.applyTermsOrderToGroupAggNode(childMap, params, aggAlias)
		}
	}
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

// CollectGroupByRowsFromBucket since the outer barrel recursive on multiple lines (each row contains the dimension key and optional metrics).
func (c *OpenSearchConnector) collectGroupByRowsFromBucket(bucket map[string]any, level int, params *interfaces.ResourceDataQueryParams, aggAlias string, rowSoFar map[string]any) []map[string]any {
	if level < 0 || level >= len(params.GroupBy) {
		return nil
	}
	gb := params.GroupBy[level]
	row := make(map[string]any, len(rowSoFar)+2)
	for k, v := range rowSoFar {
		row[k] = v
	}
	if key, ok := bucket["key"]; ok {
		row[gb.Property] = key
	} else if keyStr, ok := bucket["key_as_string"]; ok {
		row[gb.Property] = keyStr
	}

	if level == len(params.GroupBy)-1 {
		if params.Aggregation != nil {
			c.mergeMetricIntoRowFromBucket(bucket, row, aggAlias)
		} else if params.Having != nil && params.Having.Field == "count(*)" {
			row["__value"] = bucket["doc_count"]
		}
		return []map[string]any{row}
	}

	nextName := "group_by_" + params.GroupBy[level+1].Property
	// The sub-aggregation results of OpenSearch Buckets are directly tiled under the buckets instead of being hung in bucket["aggs"].
	childAgg, ok := bucket[nextName].(map[string]any)
	if !ok {
		return []map[string]any{row}
	}
	nextBuckets, ok := childAgg["buckets"].([]any)
	if !ok {
		return []map[string]any{row}
	}
	var out []map[string]any
	for _, nb := range nextBuckets {
		nbm, ok := nb.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, c.collectGroupByRowsFromBucket(nbm, level+1, params, aggAlias, row)...)
	}
	return out
}

// read the outermost group_by aggregation and flatten it into result rows, and then truncate it by limit.
func (c *OpenSearchConnector) flattenNestedGroupByRows(rootAgg map[string]any, params *interfaces.ResourceDataQueryParams, aggAlias string) []map[string]any {
	buckets, ok := rootAgg["buckets"].([]any)
	if !ok {
		return []map[string]any{}
	}
	var rows []map[string]any
	for _, b := range buckets {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, c.collectGroupByRowsFromBucket(bm, 0, params, aggAlias, nil)...)
	}
	if params.Paging.Limit > 0 && len(rows) > params.Paging.Limit {
		rows = rows[:params.Paging.Limit]
	}
	return rows
}

// buildHavingBucketSelector 使用数值参数比较聚合结果，不把输入值或别名拼入脚本。
func (c *OpenSearchConnector) buildHavingBucketSelector(having *interfaces.HavingClause, aggAlias string, temporalField *interfaces.Property) (map[string]any, error) {
	source := ""
	scriptParams := map[string]any{}
	metricPath := aggAlias
	if having.Field == "count(*)" {
		metricPath = "_count"
		temporalField = nil
	}
	switch having.Operation {
	case "==", "!=", ">", ">=", "<", "<=":
		value, err := indexHavingValue(temporalField, having.Value)
		if err != nil {
			return nil, err
		}
		source = "params.metric " + having.Operation + " params.value"
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
			parsed, err := indexHavingValue(temporalField, value)
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
		case "range", "out_range":
			scriptParams["lower"], scriptParams["upper"] = numeric[0], numeric[1]
			source = "params.metric >= params.lower && params.metric <= params.upper"
			if having.Operation == "out_range" {
				source = "params.metric < params.lower || params.metric > params.upper"
			}
		}
	default:
		return nil, interfaces.NewConditionBuildError("unsupported HAVING operation: %s", having.Operation)
	}
	return map[string]any{
		"bucket_selector": map[string]any{
			"buckets_path": map[string]any{"metric": metricPath},
			"script":       map[string]any{"source": source, "params": scriptParams},
		},
	}, nil
}

// 日期聚合的数值接口使用 epoch milliseconds，包括 date_nanos 的聚合值。
// 保留输入的亚毫秒部分；最终比较精度受 OpenSearch double 聚合值限制。
func indexHavingValue(field *interfaces.Property, value any) (any, error) {
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
