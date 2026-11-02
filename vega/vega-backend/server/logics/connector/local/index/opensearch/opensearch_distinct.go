// Copyright openbkn.ai. All rights reserved.
// Licensed under the OpenBKN License. See LICENSE in the repository root for details.

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

const (
	exactDistinctPageSize      = 1000
	exactDistinctResponseBytes = 8 << 20
	exactDistinctGroupBytes    = 16 << 20
	exactDistinctTimeout       = 30 * time.Second
)

// 每个进程最多同时持有四个精确枚举；等待也计入整次查询的时间预算。
var exactDistinctSlots = make(chan struct{}, 4)

type exactDistinctGroup struct {
	row       map[string]any
	count     int64
	documents int64
}

// executeExactDistinct 在同一个 PIT 上枚举唯一组合。只保存每组计数，不累积全部唯一值。
func (c *OpenSearchConnector) executeExactDistinct(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	timeout := exactDistinctTimeout
	if params.Timeout > 0 && params.Timeout < timeout {
		timeout = params.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case exactDistinctSlots <- struct{}{}:
		defer func() { <-exactDistinctSlots }()
	case <-ctx.Done():
		return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_time", ctx.Err())
	}

	alias := params.Aggregation.Alias
	if alias == "" {
		alias = "__value"
	}
	field, err := c.exactMatchFieldName(params.Aggregation.Property, resource.SchemaDefinition)
	if err != nil {
		return nil, err
	}
	sources := make([]any, 0, len(params.GroupBy)+1)
	for i, group := range params.GroupBy {
		if group.Property == alias {
			return nil, interfaces.NewConditionBuildError("aggregate alias must not shadow a group_by field")
		}
		groupField, err := c.exactMatchFieldName(group.Property, resource.SchemaDefinition)
		if err != nil {
			return nil, err
		}
		definition := map[string]any{
			"field":          groupField,
			"missing_bucket": true,
		}
		kind := "terms"
		if group.CalendarInterval != "" {
			kind = "date_histogram"
			definition["calendar_interval"] = group.CalendarInterval
		}
		sources = append(sources, map[string]any{strconv.Itoa(i): map[string]any{kind: definition}})
	}

	// missing_bucket 保留只有空值的组；空值桶自身不增加 DISTINCT 计数。
	sources = append(sources, map[string]any{
		"value": map[string]any{
			"terms": map[string]any{
				"field":          field,
				"missing_bucket": true,
			},
		},
	})
	var havingParams map[string]any
	if params.Having != nil {
		selector, err := c.buildHavingBucketSelector(params.Having, alias, nil)
		if err != nil {
			return nil, err
		}
		havingParams = selector["bucket_selector"].(map[string]any)["script"].(map[string]any)["params"].(map[string]any)
	}
	filter := any(map[string]any{"match_all": map[string]any{}})
	if params.ActualFilterCond != nil {
		filter, err = c.ConvertFilterCondition(params.ActualFilterCond, resource.SchemaDefinition)
		if err != nil {
			return nil, err
		}
	}
	if err := validateExactDistinctQuery(resource, params); err != nil {
		return nil, err
	}
	created, err := c.distinctRequest(ctx, http.MethodPost,
		"/"+url.PathEscape(indexName)+"/_search/point_in_time?keep_alive=1m&allow_partial_pit_creation=false", nil)
	if err != nil {
		return nil, err
	}
	pit, ok := created["pit_id"].(string)
	if !ok || pit == "" {
		return nil, fmt.Errorf("exact distinct PIT response is missing its id")
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cleanupCancel()
		if _, err := c.distinctRequest(cleanupCtx, http.MethodDelete, "/_search/point_in_time", map[string]any{"pit_id": []string{pit}}); err != nil {
			logger.Warnf("Failed to close exact distinct PIT: %v", err)
		}
	}()

	if err := validateCompleteAggregationResponse(created); err != nil {
		return nil, err
	}
	if _, ok := created["_shards"].(map[string]any); !ok {
		return nil, fmt.Errorf("exact distinct PIT response is missing shard completion metadata")
	}
	groups := make(map[string]*exactDistinctGroup)
	groupBytes := 0
	if len(params.GroupBy) == 0 {
		groups["[]"] = &exactDistinctGroup{row: map[string]any{}}
	}
	// HAVING count(*) 的分组文档数必须独立于待去重字段的多值数量。
	phases := []bool{false}
	if params.Having != nil && params.Having.Field == "count(*)" && len(params.GroupBy) > 0 {
		phases = []bool{true, false}
	}
	var total int64
	for _, documentsOnly := range phases {
		pageSources := sources
		if documentsOnly {
			pageSources = sources[:len(sources)-1]
		}
		var after map[string]any
		for {
			if err := ctx.Err(); err != nil {
				return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_time", err)
			}
			composite := map[string]any{"size": exactDistinctPageSize, "sources": pageSources}
			if after != nil {
				composite["after"] = after
			}
			needTotal := after == nil && (params.NeedTotal || params.Having != nil && params.Having.Field == "count(*)" && len(params.GroupBy) == 0)
			body := map[string]any{
				"size":             0,
				"query":            filter,
				"timeout":          "30s",
				"track_total_hits": needTotal,
				"pit": map[string]any{
					"id":         pit,
					"keep_alive": "1m",
				},
				"aggs": map[string]any{
					"__distinct": map[string]any{
						"composite": composite,
					},
				},
			}
			response, err := c.distinctRequest(ctx, http.MethodPost, "/_search?allow_partial_search_results=false", body)
			if err != nil {
				return nil, err
			}
			if id, ok := response["pit_id"].(string); ok && id != "" {
				pit = id
			}
			if err := validateCompleteAggregationResponse(response); err != nil {
				return nil, err
			}
			if _, ok := response["_shards"].(map[string]any); !ok {
				return nil, fmt.Errorf("exact distinct search is missing shard completion metadata")
			}
			if needTotal {
				hits, _ := response["hits"].(map[string]any)
				if _, ok := hits["total"].(map[string]any); !ok {
					return nil, fmt.Errorf("exact distinct search is missing its document count")
				}
			}
			if hits, ok := response["hits"].(map[string]any); ok {
				if hitTotal, ok := hits["total"].(map[string]any); ok {
					if hitTotal["relation"] != "eq" {
						return nil, fmt.Errorf("exact distinct search returned an inexact document count")
					}
					var valid bool
					total, valid = openSearchInt64(hitTotal["value"])
					if !valid || total < 0 {
						return nil, fmt.Errorf("exact distinct search returned an invalid document count")
					}
				}
			}
			aggs, _ := response["aggregations"].(map[string]any)
			agg, _ := aggs["__distinct"].(map[string]any)
			buckets, ok := agg["buckets"].([]any)
			if !ok {
				return nil, fmt.Errorf("exact distinct search is missing composite buckets")
			}
			if len(buckets) == 0 {
				break
			}
			for _, bucket := range buckets {
				item, _ := bucket.(map[string]any)
				key, ok := item["key"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("exact distinct search returned an invalid bucket key")
				}
				value, exists := key["value"]
				if !documentsOnly && !exists {
					return nil, fmt.Errorf("exact distinct bucket is missing its value")
				}
				groupKey := make([]any, len(params.GroupBy))
				for i := range params.GroupBy {
					groupKey[i], exists = key[strconv.Itoa(i)]
					if !exists {
						return nil, fmt.Errorf("exact distinct bucket is missing its group key")
					}
				}
				encoded, err := sonic.Marshal(groupKey)
				if err != nil {
					return nil, err
				}
				group := groups[string(encoded)]
				if group == nil {
					groupBytes += 2*len(encoded) + 512 + 128*len(groupKey)
					if groupBytes > exactDistinctGroupBytes {
						return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_group_memory", nil)
					}
					group = &exactDistinctGroup{row: make(map[string]any, len(groupKey)+1)}
					for i, cfg := range params.GroupBy {
						// 解码器的字符串可能引用整页响应；只保留独立的分组键。
						value := groupKey[i]
						switch key := value.(type) {
						case string:
							value = strings.Clone(key)
						case json.Number:
							value = json.Number(strings.Clone(string(key)))
						}
						group.row[cfg.Property] = value
					}
					groups[string(encoded)] = group
				}
				if !documentsOnly && value != nil {
					if group.count == math.MaxInt64 {
						return nil, fmt.Errorf("exact distinct count overflow")
					}
					group.count++
				}
				documents, valid := openSearchInt64(item["doc_count"])
				if !valid || documents < 0 {
					return nil, fmt.Errorf("exact distinct bucket returned an invalid document count")
				}
				if documentsOnly {
					group.documents = documents
				}
			}
			next, ok := agg["after_key"].(map[string]any)
			if !ok || len(next) == 0 {
				return nil, fmt.Errorf("exact distinct search is missing its continuation key")
			}
			if len(next) != len(pageSources) {
				return nil, fmt.Errorf("exact distinct continuation has invalid keys")
			}
			if after != nil {
				comparison := 0
				for i := 0; i < len(pageSources); i++ {
					name := strconv.Itoa(i)
					if i == len(params.GroupBy) {
						name = "value"
					}
					if _, exists := next[name]; !exists {
						return nil, fmt.Errorf("exact distinct continuation is missing a key")
					}
					comparison = compareDistinctValues(after[name], next[name])
					if comparison != 0 {
						break
					}
				}
				if comparison >= 0 {
					return nil, fmt.Errorf("exact distinct continuation did not advance")
				}
			}
			after = next
		}
	}
	rows := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		if params.Having != nil {
			value := group.count
			if params.Having.Field == "count(*)" {
				value = group.documents
				if len(params.GroupBy) == 0 {
					value = total
				}
			}
			if !exactDistinctHaving(value, params.Having.Operation, havingParams) {
				continue
			}
		}
		group.row[alias] = group.count
		rows = append(rows, group.row)
	}
	sort.Slice(rows, func(i, j int) bool {
		for _, field := range params.Sort {
			name := field.Field
			if name == "__value" {
				name = alias
			}
			comparison := compareDistinctValues(rows[i][name], rows[j][name])
			if comparison != 0 {
				if strings.EqualFold(field.Direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		for _, group := range params.GroupBy {
			if comparison := compareDistinctValues(rows[i][group.Property], rows[j][group.Property]); comparison != 0 {
				return comparison < 0
			}
		}
		return false
	})
	offset := min(max(params.Paging.Offset, 0), len(rows))
	rows = rows[offset:]
	if params.Paging.Limit > 0 && len(rows) > params.Paging.Limit {
		rows = rows[:params.Paging.Limit]
	}
	if err := ctx.Err(); err != nil {
		return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_time", err)
	}
	return &interfaces.ResourceDataQueryResult{
		Entries:    rows,
		TotalCount: total,
	}, nil
}

// distinctRequest 限制每页响应体，并保留 JSON 整数，避免续页键超过 2^53 后失真。
func (c *OpenSearchConnector) distinctRequest(ctx context.Context,
	method, path string, body map[string]any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := sonic.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Perform(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_time", ctx.Err())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("exact distinct request failed")
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("exact distinct request returned HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, exactDistinctResponseBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_time", ctx.Err())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read exact distinct response failed")
	}
	if len(encoded) > exactDistinctResponseBytes {
		return nil, interfaces.NewQueryExecutionBudgetError("exact_distinct_response_memory", nil)
	}
	var result map[string]any
	if err := common.UnmarshalPreciseJSON(encoded, &result); err != nil {
		return nil, fmt.Errorf("decode exact distinct response failed")
	}
	return result, nil
}

func validateCompleteAggregationResponse(response map[string]any) error {
	if timedOut, _ := response["timed_out"].(bool); timedOut {
		return interfaces.NewQueryExecutionBudgetError("aggregation_time", nil)
	}
	if shards, ok := response["_shards"].(map[string]any); ok {
		failed, valid := openSearchInt64(shards["failed"])
		if !valid || failed != 0 {
			return fmt.Errorf("aggregation did not complete on all shards")
		}
		total, totalOK := openSearchInt64(shards["total"])
		successful, successfulOK := openSearchInt64(shards["successful"])
		if !totalOK || !successfulOK || total < 0 || successful != total {
			return fmt.Errorf("aggregation did not complete on all shards")
		}
	}
	if terminated, _ := response["terminated_early"].(bool); terminated {
		return fmt.Errorf("aggregation terminated before completion")
	}
	return nil
}

func compareDistinctValues(left, right any) int {
	if left == nil {
		if right == nil {
			return 0
		}
		return -1
	}
	if right == nil {
		return 1
	}
	if _, ok := left.(string); !ok {
		a, aOK := new(big.Rat).SetString(fmt.Sprint(left))
		b, bOK := new(big.Rat).SetString(fmt.Sprint(right))
		if aOK && bOK {
			return a.Cmp(b)
		}
	}
	return strings.Compare(fmt.Sprint(left), fmt.Sprint(right))
}

func exactDistinctHaving(value int64, operation string, params map[string]any) bool {
	compare := func(other any) int { return compareDistinctValues(value, other) }
	switch operation {
	case "==":
		return compare(params["value"]) == 0
	case "!=":
		return compare(params["value"]) != 0
	case ">":
		return compare(params["value"]) > 0
	case ">=":
		return compare(params["value"]) >= 0
	case "<":
		return compare(params["value"]) < 0
	case "<=":
		return compare(params["value"]) <= 0
	case "range", "out_range":
		inside := compare(params["lower"]) >= 0 && compare(params["upper"]) <= 0
		return inside == (operation == "range")
	case "in", "not_in":
		for _, candidate := range params["values"].([]any) {
			if compare(candidate) == 0 {
				return operation == "in"
			}
		}
		return operation == "not_in"
	}
	return false
}

// validateExactDistinctQuery 查找聚合和分组字段，检查逻辑类型后按资源类别分派。
func validateExactDistinctQuery(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	fields := []string{params.Aggregation.Property}
	for _, group := range params.GroupBy {
		fields = append(fields, group.Property)
	}
	for i, field := range fields {
		property := exactMatchProperty(field, resource.SchemaDefinition)
		if property == nil {
			return interfaces.NewConditionBuildError("field %q not found in schema", field)
		}
		scalar := interfaces.DataType_IsNumber(property.Type) || interfaces.DataType_IsString(property.Type) ||
			interfaces.DataType_IsDate(property.Type) || property.Type == interfaces.DataType_Boolean
		if !scalar {
			return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q with its declared type", field)
		}
		var err error
		switch resource.Category {
		case interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset:
			err = validateManagedExactDistinctField(field, property)
		case interfaces.ResourceCategoryIndex:
			// 首项是原值去重字段；分组字段可显式选择日历间隔。
			calendarGroup := i > 0 && params.GroupBy[i-1].CalendarInterval != ""
			err = validateIndexExactDistinctField(field, property, resource.SchemaDefinition, calendarGroup)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// validateManagedExactDistinctField 按托管 schema 契约拒绝带有 ignore_above 的 keyword 特征。
func validateManagedExactDistinctField(field string, property *interfaces.Property) error {
	for _, feature := range property.Features {
		if feature.FeatureType == interfaces.PropertyFeatureType_Keyword {
			return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q: ignore_above may omit values from the index; query the source or allow approximation", field)
		}
	}
	return nil
}

// validateIndexExactDistinctField 检查外部 index 的字段配置和父级结构。
func validateIndexExactDistinctField(field string, property *interfaces.Property, schema []*interfaces.Property, calendarGroup bool) error {
	if property.OriginalType == "date_nanos" && !calendarGroup {
		return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q: date_nanos aggregation uses millisecond precision; query the source or allow approximation", field)
	}
	if property.OriginalType == "alias" {
		return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q with its declared type", field)
	}
	config := property.Attributes
	if property.Type == interfaces.DataType_Text {
		for _, feature := range property.Features {
			if feature.FeatureType == interfaces.PropertyFeatureType_Keyword {
				config = feature.Config
				break
			}
		}
	}
	if config["ignore_above"] != nil {
		return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q: ignore_above may omit values from the index; query the source or allow approximation", field)
	}
	normalizer, _ := config["normalizer"].(string)
	if config["ignore_malformed"] == true || config["null_value"] != nil || normalizer != "" && normalizer != "default" {
		return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q with its declared configuration; query the source or allow approximation", field)
	}
	for _, parent := range schema {
		if parent != nil && (parent.OriginalType == "nested" || parent.Attributes["enabled"] == false) &&
			strings.HasPrefix(propertyPhysicalFieldName(property), propertyPhysicalFieldName(parent)+".") {
			return interfaces.NewConditionBuildError("cannot guarantee exact count_distinct for field %q under its parent configuration", field)
		}
	}
	return nil
}
