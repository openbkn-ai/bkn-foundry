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
	"fmt"
	"io"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"vega-backend/interfaces"
)

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
	if err := sonic.Unmarshal([]byte(dsl), &dslMap); err != nil {
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
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	// Extract total count
	var total int64
	if totalMap, ok := hits["total"].(map[string]any); ok {
		if value, ok := totalMap["value"].(float64); ok {
			total = int64(value)
		} else if value, ok := totalMap["value"].(int64); ok {
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
		if score, ok := hitMap["_score"].(float64); ok {
			source["_score"] = score
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

	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
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
		// Fall back to the default string type if mapping retrieval fails.
		logger.Warnf("failed to fetch index mappings, using default string type: %v", err)
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

	// Parse the fields and construct field type mappings
	fields := make(map[string]interfaces.IndexFieldMeta)
	if idxData, ok := dataMapping[indexName]; ok {
		parseProperties("", idxData.Mappings.Properties, fields)
	}
	for fieldName, meta := range fields {
		fieldTypeMap[fieldName] = c.MapType(meta.Type)
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

		// Grouping: Nested terms/date_histogram from the inside out; The metric and HAVING are placed under the innermost bucket.
		if len(params.GroupBy) > 0 {
			leafAggs := make(map[string]any)
			if metricBody != nil {
				leafAggs[aggAlias] = metricBody
			}
			if params.Having != nil && params.Aggregation != nil {
				leafAggs["having_filter"] = c.buildHavingBucketSelector(params.Having, aggAlias)
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
					bucket = map[string]any{
						"terms": map[string]any{
							"field": gb.Property,
							"size":  nestedTermsSize(i, n, params.Limit),
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
		} else if metricBody != nil {
			aggs[aggAlias] = metricBody
		}

		// Add the aggregation to the query
		query["aggs"] = aggs

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
		if err := sonic.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode aggregate search result: %w", err)
		}

		// Extract the total number of documents
		var totalCount int64
		if hits, ok := result["hits"].(map[string]any); ok {
			if totalMap, ok := hits["total"].(map[string]any); ok {
				if value, ok := totalMap["value"].(float64); ok {
					totalCount = int64(value)
				} else if value, ok := totalMap["value"].(int64); ok {
					totalCount = value
				}
			}
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
			keyword, _ := c.getKeywordSuffix(s.Field, resource.SchemaDefinition)
			sort = append(sort, map[string]any{
				s.Field + keyword: map[string]any{
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
		if params.Offset > 0 && params.SearchAfter == nil {
			query["from"] = params.Offset
		}

		if params.Limit > 0 {
			query["size"] = params.Limit
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
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search result: %w", err)
	}

	hits, ok := result["hits"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid search result format: missing hits")
	}

	total, ok := hits["total"].(map[string]any)["value"].(float64)
	if !ok {
		total = 0
	}

	hitsArray, ok := hits["hits"].([]any)
	if !ok {
		return &interfaces.QueryResult{
			Entries: []map[string]any{},
			Total:   int64(total),
		}, nil
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
		if score, ok := hitMap["_score"].(float64); ok {
			source["_score"] = score
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
	if params.Limit > 0 && len(rows) > params.Limit {
		rows = rows[:params.Limit]
	}
	return rows
}

// BuildHavingBucketSelector building HAVING bucket_selector polymerization conditions
func (c *OpenSearchConnector) buildHavingBucketSelector(having *interfaces.HavingClause, aggAlias string) map[string]any {
	// OpenSearch implements HAVING using the bucket_selector aggregation
	script := ""
	switch having.Operation {
	case "==":
		script = fmt.Sprintf("params.%s == %v", aggAlias, having.Value)
	case "!=":
		script = fmt.Sprintf("params.%s != %v", aggAlias, having.Value)
	case ">":
		script = fmt.Sprintf("params.%s > %v", aggAlias, having.Value)
	case ">=":
		script = fmt.Sprintf("params.%s >= %v", aggAlias, having.Value)
	case "<":
		script = fmt.Sprintf("params.%s < %v", aggAlias, having.Value)
	case "<=":
		script = fmt.Sprintf("params.%s <= %v", aggAlias, having.Value)
	case "in":
		if values, ok := having.Value.([]any); ok {
			script = fmt.Sprintf("%s.contains(params.%s.toString())", formatInValuesForScript(values), aggAlias)
		}
	case "not_in":
		if values, ok := having.Value.([]any); ok {
			script = fmt.Sprintf("!%s.contains(params.%s.toString())", formatInValuesForScript(values), aggAlias)
		}
	case "range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			script = fmt.Sprintf("params.%s >= %v && params.%s <= %v", aggAlias, values[0], aggAlias, values[1])
		}
	case "out_range":
		if values, ok := having.Value.([]any); ok && len(values) == 2 {
			script = fmt.Sprintf("params.%s < %v || params.%s > %v", aggAlias, values[0], aggAlias, values[1])
		}
	}

	return map[string]any{
		"bucket_selector": map[string]any{
			"buckets_path": map[string]any{
				aggAlias: aggAlias,
			},
			"script": map[string]any{
				"source": script,
			},
		},
	}
}

// formatInValuesForScript formats the list of values for IN operations into Painless script format
func formatInValuesForScript(values []any) string {
	if len(values) == 0 {
		return "[]"
	}

	var strValues []string
	for _, v := range values {
		switch val := v.(type) {
		case string:
			strValues = append(strValues, fmt.Sprintf("'%s'", val))
		default:
			strValues = append(strValues, fmt.Sprintf("%v", val))
		}
	}

	return fmt.Sprintf("[%s]", strings.Join(strValues, ", "))
}

// applyFulltextFeature to enable full-text search capabilities for fields.
//
//	The -string field: The main field retains the keyword (exact match/sort/aggregate semantics unchanged).
//	  Add an extra "text" subfield for word segmentation full-text search; The query side hits with 'field name.< Subfield name >'.
//	The -text field: The main field itself is text (full text), and only set analyzer to the main field.
//
// Parameters such as analyzer are injected from feature-.config; When there is no config, use the default word segmenter of OpenSearch.
func applyFulltextFeature(fieldProps map[string]any, columnType string, feature interfaces.PropertyFeature) {
	switch columnType {
	case interfaces.DataType_String:
		subName := feature.FeatureName
		if subName == "" {
			subName = "fulltext"
		}
		sub := map[string]any{"type": "text"}
		for k, v := range feature.Config {
			sub[k] = v
		}
		fields, ok := fieldProps["fields"].(map[string]any)
		if !ok {
			fields = map[string]any{}
			fieldProps["fields"] = fields
		}
		fields[subName] = sub
	case interfaces.DataType_Text:
		// The main field is already text (word segmentation). Set analyzer and others directly on the main field
		for k, v := range feature.Config {
			fieldProps[k] = v
		}
	}
}

// build field mapping
func (c *OpenSearchConnector) buildFieldMappings(schemaDefinition []*interfaces.Property) (map[string]any, bool, error) {
	properties := map[string]any{}
	hasVectorField := false

	for _, prop := range schemaDefinition {
		fieldType := prop.Type
		switch prop.Type {
		case interfaces.DataType_Integer:
			fieldType = "long"
		case interfaces.DataType_UnsignedInteger:
			fieldType = "unsigned_long"
		case interfaces.DataType_Float:
			fieldType = "double"
		case interfaces.DataType_Decimal:
			fieldType = "scaled_float"
		case interfaces.DataType_String:
			fieldType = "keyword"
		case interfaces.DataType_Datetime, interfaces.DataType_Timestamp:
			fieldType = "date"
		case interfaces.DataType_Time:
			fieldType = "keyword"
		case interfaces.DataType_Json:
			fieldType = "object"
		case interfaces.DataType_Vector:
			hasVectorField = true
			fieldType = "knn_vector"
		case interfaces.DataType_Point:
			fieldType = "geo_point"
		case interfaces.DataType_Shape:
			fieldType = "geo_shape"
		case interfaces.DataType_Other:
			return nil, false, fmt.Errorf("unsupported schema field %q: type %s (original_type: %s)", prop.Name, prop.Type, prop.OriginalType)
		default:
			// Keep the fieldType unchanged
		}

		// Create field attribute mappings
		fieldProps := map[string]any{
			"type": fieldType,
		}

		// Add the scaling_factor parameter to the decimal type
		if prop.Type == interfaces.DataType_Decimal {
			fieldProps["scaling_factor"] = 1000000000000000000.0 // 18 decimal places
		}

		// Handle field characteristics
		if prop.Features != nil {
			for _, feature := range prop.Features {
				// fulltext does not rely on config (it can use the default tokenizer without analyzer) and is processed independently
				if feature.FeatureType == interfaces.PropertyFeatureType_Fulltext {
					applyFulltextFeature(fieldProps, prop.Type, feature)
					continue
				}
				if feature.Config != nil {
					switch feature.FeatureType {
					case interfaces.PropertyFeatureType_Keyword:
						fieldsAdded := false
						for k, v := range feature.Config {
							if prop.Type == interfaces.DataType_Text {
								if !fieldsAdded {
									// Add subfields
									fieldProps["fields"] = map[string]any{
										feature.FeatureName: map[string]any{
											"type": "keyword",
										},
									}
									fieldsAdded = true
								}
								// Add to the subfield attribute
								if fields, ok := fieldProps["fields"].(map[string]any); ok {
									if subField, ok := fields[feature.FeatureName].(map[string]any); ok {
										subField[k] = v
									}
								}
							} else {
								// Add it directly to the field attribute
								fieldProps[k] = v
							}
						}
					case interfaces.PropertyFeatureType_Vector:
						// A vector feature on a source field describes VEGA's embedding
						// workflow. Only the generated *_vector field is an OpenSearch
						// vector mapping; its configuration must not be applied to the
						// source field (for example, a keyword field cannot accept
						// embedding_model).
						if prop.Type != interfaces.DataType_Vector {
							continue
						}
						for k, v := range feature.Config {
							fieldProps[k] = v
						}
					default:
						return nil, false, fmt.Errorf("unsupported feature type: %s", feature.FeatureType)
					}
				}
			}
		}

		properties[prop.Name] = fieldProps
	}

	return properties, hasVectorField, nil
}
