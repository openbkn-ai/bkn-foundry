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
	"github.com/kweaver-ai/kweaver-go-lib/logger"
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
			Rows:  []map[string]any{},
			Total: total,
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
		Rows:  documents,
		Total: total,
	}, nil
}

// ExecuteRawQuery executes a raw OpenSearch DSL query on the specified index.
func (c *OpenSearchConnector) ExecuteRawQuery(ctx context.Context, index string, query map[string]any) (*interfaces.RawQueryResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	// Convert query to JSON
	queryJSON, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Log the DSL query
	logger.Infof("[OpenSearch DSL Query] Index: %s, Query: %s", index, string(queryJSON))

	// Create search request
	req := opensearchapi.SearchRequest{
		Index: []string{index},
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
				Sort   []any          `json:"sort"` // 添加sort字段
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// If no hits, return empty result
	if len(searchResp.Hits.Hits) == 0 {
		return &interfaces.RawQueryResponse{
			Columns:    []interfaces.ColumnInfo{},
			Entries:    []map[string]any{},
			TotalCount: 0,
			Stats: interfaces.QueryStats{
				IsTimeout: false,
			},
		}, nil
	}

	// 获取索引的mapping信息以确定字段类型
	fieldTypeMap := make(map[string]string)
	if err := c.fetchMappingsForQuery(ctx, index, fieldTypeMap); err != nil {
		// 如果获取mapping失败，使用默认的string类型
		logger.Warnf("failed to fetch index mappings, using default string type: %v", err)
	}

	// Collect all field names from the first hit
	firstHit := searchResp.Hits.Hits[0].Source
	columns := make([]interfaces.ColumnInfo, 0, len(firstHit))
	for fieldName := range firstHit {
		fieldType := "string" // 默认类型
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

	// 构建响应
	// total_count设置为OpenSearch返回的总数据量
	totalCount := searchResp.Hits.Total.Value

	response := &interfaces.RawQueryResponse{
		Columns:    columns,
		Entries:    entries,
		TotalCount: totalCount,
		Stats: interfaces.QueryStats{
			IsTimeout: false,
		},
	}

	// 如果有结果，检查是否需要返回search_after
	if len(searchResp.Hits.Hits) > 0 {
		lastHit := searchResp.Hits.Hits[len(searchResp.Hits.Hits)-1]
		// 如果最后一条记录有sort值，将其作为search_after返回
		if len(lastHit.Sort) > 0 {
			response.Stats.SearchAfter = lastHit.Sort
		}
	}

	return response, nil
}

// fetchMappingsForQuery 获取索引的mapping信息并构建字段类型映射
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

	// 解析 JSON
	var dataMapping map[string]struct {
		Mappings struct {
			Properties map[string]Property `json:"properties"`
		} `json:"mappings"`
	}
	if err := sonic.Unmarshal(bodyBytes, &dataMapping); err != nil {
		return fmt.Errorf("failed to unmarshal mappings: %w", err)
	}

	// 解析字段并构建字段类型映射
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
// ExecuteQuery 执行OpenSearch查询并返回结果
// 参数:
//   - ctx: 上下文信息
//   - resource: 资源信息，包含索引名称等
//   - params: 查询参数，包括输出字段、排序、分页等
//
// 返回值:
//   - *interfaces.QueryResult: 查询结果，包含行数据和总数
//   - error: 错误信息
func (c *OpenSearchConnector) ExecuteQuery(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {

	// Ensure we have a connection
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to OpenSearch: %w", err)
	}

	if indexName == "" {
		return nil, fmt.Errorf("index name is empty in resource")
	}

	// 聚合查询：当Aggregation、GroupBy或Having任一参数存在时执行
	if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		// 构建OpenSearch聚合查询
		query := map[string]any{
			"size": 0, // 聚合查询不需要返回文档
		}

		// 处理过滤条件
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

		// 构建聚合查询
		aggs := map[string]any{}

		// 确定聚合函数和别名
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

		// 分组：自内向外嵌套 terms / date_histogram；度量与 HAVING 挂在最内层桶下。
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
			// 对每一层 terms 应用 sort 映射到的 order（第二维度排序写在内层 terms 上）
			for _, v := range aggs {
				if node, ok := v.(map[string]any); ok {
					c.applyTermsOrderToGroupAggNode(node, params, aggAlias)
				}
			}
		} else if metricBody != nil {
			aggs[aggAlias] = metricBody
		}

		// 将聚合添加到查询
		query["aggs"] = aggs

		// 序列化查询
		queryJSON, err := sonic.Marshal(query)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize aggregate query: %w", err)
		}

		logger.Debugf("OpenSearch aggregate query: %s", string(queryJSON))

		// 执行搜索请求
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

		// 读取响应体用于日志记录
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		logger.Debugf("OpenSearch aggregate response: %s", string(bodyBytes))

		// 解析响应
		var result map[string]any
		if err := sonic.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode aggregate search result: %w", err)
		}

		// 提取文档总数
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

		// 提取聚合结果
		aggregations, ok := result["aggregations"].(map[string]any)
		if !ok {
			return &interfaces.QueryResult{
				Rows:  []map[string]any{},
				Total: totalCount,
			}, nil
		}

		// 处理分组聚合结果（支持多层 group_by 嵌套桶展平）
		var rows []map[string]any
		if len(params.GroupBy) > 0 {
			groupByAggName := "group_by_" + params.GroupBy[0].Property
			if groupByAgg, ok := aggregations[groupByAggName].(map[string]any); ok {
				rows = c.flattenNestedGroupByRows(groupByAgg, params, aggAlias)
			}
		} else {
			// 没有分组，只有聚合
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
			Rows:  rows,
			Total: totalCount,
		}, nil
	}

	// 明细查询
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
	logger.Debugf("Executing query: %s", string(queryJSON))

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
			Rows:  []map[string]any{},
			Total: int64(total),
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
			continue
		}

		source["_id"] = hitMap["_id"]
		// Add _score field if present
		if score, ok := hitMap["_score"].(float64); ok {
			source["_score"] = score
		}
		documents = append(documents, source)
	}

	return &interfaces.QueryResult{
		Rows:  documents,
		Total: int64(total),
	}, nil
}

// nestedTermsSize 为嵌套 group_by 中每一层 terms 设置 size：最内层用 limit 控制「每个父桶下」的行数，外层用较大上限以展开组合。
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

// applyTermsOrderToGroupAggNode 递归为子树中每个 terms 桶写入 order（多维度时第二维 sort 落在内层 terms）。
// 按度量排序仅在该 terms 的直接子 aggs 中包含度量名时生效，避免外层 terms 引用嵌套过深的子聚合导致 DSL 非法。
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

// collectGroupByRowsFromBucket 自外层桶递归展开为多行（每行包含各维度键与可选度量）。
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
	// OpenSearch bucket 的子聚合结果直接平铺在 bucket 下，而不是挂在 bucket["aggs"] 中。
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

// flattenNestedGroupByRows 读取最外层 group_by 聚合并展平为结果行，最后按 limit 截断。
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

// buildHavingBucketSelector 构建HAVING条件的bucket_selector聚合
func (c *OpenSearchConnector) buildHavingBucketSelector(having *interfaces.HavingClause, aggAlias string) map[string]any {
	// OpenSearch使用bucket_selector聚合实现HAVING
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

// formatInValuesForScript 格式化IN操作的值列表为Painless脚本格式
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

// buildFieldMappings 构建字段映射
func (c *OpenSearchConnector) buildFieldMappings(schemaDefinition []*interfaces.Property) (map[string]any, bool, error) {
	properties := map[string]any{}
	hasVectorField := false

	for _, column := range schemaDefinition {
		fieldType := column.Type
		switch column.Type {
		case "integer":
			fieldType = "long"
		case "unsigned_integer":
			fieldType = "unsigned_long"
		case "float":
			fieldType = "double"
		case "decimal":
			fieldType = "scaled_float"
		case "string":
			fieldType = "keyword"
		case "datetime":
			fieldType = "date"
		case "time":
			fieldType = "keyword"
		case "json":
			fieldType = "object"
		case "vector":
			hasVectorField = true
			fieldType = "knn_vector"
		case "point":
			fieldType = "geo_point"
		case "shape":
			fieldType = "geo_shape"
		default:
			// 保持 fieldType 不变
		}

		// 创建字段属性映射
		fieldProps := map[string]any{
			"type": fieldType,
		}

		// 为decimal类型添加scaling_factor参数
		if column.Type == "decimal" {
			fieldProps["scaling_factor"] = 1000000000000000000.0 // 18位小数
		}

		// 处理字段特性
		if column.Features != nil {
			for _, feature := range column.Features {
				if feature.Config != nil {
					switch feature.FeatureType {
					case "keyword":
						fieldsAdded := false
						for k, v := range feature.Config {
							if column.Type == "text" {
								if !fieldsAdded {
									// 添加子字段
									fieldProps["fields"] = map[string]any{
										feature.FeatureName: map[string]any{
											"type": "keyword",
										},
									}
									fieldsAdded = true
								}
								// 添加到子字段属性中
								if fields, ok := fieldProps["fields"].(map[string]any); ok {
									if subField, ok := fields[feature.FeatureName].(map[string]any); ok {
										subField[k] = v
									}
								}
							} else {
								// 直接添加到字段属性中
								fieldProps[k] = v
							}
						}
					case "vector":
						for k, v := range feature.Config {
							fieldProps[k] = v
						}
					case "fulltext":
						continue
					default:
						return nil, false, fmt.Errorf("unsupported feature type: %s", feature.FeatureType)
					}
				}
			}
		}

		properties[column.Name] = fieldProps
	}

	return properties, hasVectorField, nil
}
