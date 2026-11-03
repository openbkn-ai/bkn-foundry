// Copyright openbkn.ai. All rights reserved.
// Licensed under the OpenBKN License. See LICENSE in the repository root for details.

package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/shopspring/decimal"
)

const (
	groupedPageSize   = 1000
	groupedStateBytes = 16 << 20
	groupedTimeout    = 30 * time.Second
)

// executeGroupedQuery 在同一快照上枚举已有分组。NULL 键由引擎直接返回，不使用替代值。
// 聚合值排序需要所有分组；先完成扫描和 HAVING，再执行稳定排序与业务分页。
func (c *OpenSearchConnector) executeGroupedQuery(ctx context.Context, indexName string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, query, metric map[string]any) (*interfaces.ResourceDataQueryResult, error) {
	alias := params.AggregateOutputAlias()
	timeout := groupedTimeout
	if params.Timeout > 0 && params.Timeout < timeout {
		timeout = params.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case aggregationSlots <- struct{}{}:
		defer func() { <-aggregationSlots }()
	case <-ctx.Done():
		return nil, interfaces.NewQueryExecutionBudgetError("group_aggregation_time", ctx.Err())
	}
	sources := make([]any, 0, len(params.GroupBy))
	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, field := range resource.SchemaDefinition {
		fields[field.Name] = field
	}
	seen := make(map[string]bool, len(params.GroupBy))
	for i, group := range params.GroupBy {
		if seen[group.Property] {
			return nil, interfaces.NewConditionBuildError("duplicate group_by field %q", group.Property)
		}
		seen[group.Property] = true
		if alias != "" && group.Property == alias {
			return nil, interfaces.NewConditionBuildError("aggregate alias must not shadow a group_by field")
		}
		field, err := c.exactMatchFieldName(group.Property, resource.SchemaDefinition)
		if err != nil {
			return nil, err
		}
		definition := map[string]any{"field": field, "missing_bucket": true}
		kind := "terms"
		if group.CalendarInterval != "" {
			kind = "date_histogram"
			definition["calendar_interval"] = group.CalendarInterval
		}
		sources = append(sources, map[string]any{strconv.Itoa(i): map[string]any{kind: definition}})
	}
	for _, field := range params.Sort {
		if !seen[field.Field] && (alias == "" || field.Field != alias && field.Field != "__value") {
			return nil, interfaces.NewConditionBuildError("sort field %q is not a group or aggregate output", field.Field)
		}
	}
	var havingParams map[string]any
	metricField := filter_condition.HavingTemporalField(params, fields)
	if agg := params.Aggregation; agg != nil && (agg.Aggr == "sum" || agg.Aggr == "min" || agg.Aggr == "max") {
		if field := fields[agg.Property]; field != nil && field.Type == interfaces.DataType_Decimal {
			metricField = field
		}
	}
	decimalMetric := metricField != nil && metricField.Type == interfaces.DataType_Decimal
	var decimalScale int32
	if decimalMetric {
		scale, valid := common.NumberAsInt64(metricField.Attributes["scale"])
		if !valid || scale < 0 || scale > 308 {
			return nil, interfaces.NewConditionBuildError("Decimal aggregation requires a valid field scale")
		}
		decimalScale = int32(scale)
	}
	if params.Having != nil {
		// 复用既有阈值解析与校验；不向 composite 请求发送 pipeline script。
		selector, err := c.buildHavingBucketSelector(params.Having, alias, metricField)
		if err != nil {
			return nil, err
		}
		havingParams = selector["bucket_selector"].(map[string]any)["script"].(map[string]any)["params"].(map[string]any)
	}
	children := map[string]any{}
	if params.Aggregation != nil {
		if metric == nil {
			return nil, interfaces.NewConditionBuildError("unsupported aggregation: %s", params.Aggregation.Aggr)
		}
		children["__vega_metric"] = metric
		if params.Aggregation.Aggr == "sum" {
			children["__vega_nonnull"] = map[string]any{"value_count": map[string]any{"field": metric["sum"].(map[string]any)["field"]}}
		}
	}
	created, err := c.aggregationRequest(ctx, http.MethodPost, "/"+url.PathEscape(indexName)+"/_search/point_in_time?keep_alive=1m&allow_partial_pit_creation=false", nil, "group_aggregation")
	if err != nil {
		return nil, err
	}
	pit, ok := created["pit_id"].(string)
	if !ok || pit == "" {
		return nil, fmt.Errorf("group aggregation PIT response is missing its id")
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cleanupCancel()
		if _, err := c.aggregationRequest(cleanupCtx, http.MethodDelete, "/_search/point_in_time", map[string]any{"pit_id": []string{pit}}, "group_aggregation"); err != nil {
			logger.Warnf("Failed to close group aggregation PIT: %v", err)
		}
	}()
	if err := validateCompleteAggregationResponse(created); err != nil {
		return nil, err
	}
	if _, ok := created["_shards"].(map[string]any); !ok {
		return nil, fmt.Errorf("group aggregation PIT response is missing shard completion metadata")
	}
	rows := make([]map[string]any, 0)
	var after map[string]any
	groupKeys := make(map[string]struct{})
	var total int64
	stateBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, interfaces.NewQueryExecutionBudgetError("group_aggregation_time", err)
		}
		composite := map[string]any{"size": groupedPageSize, "sources": sources}
		if after != nil {
			composite["after"] = after
		}
		group := map[string]any{"composite": composite}
		if len(children) > 0 {
			group["aggs"] = children
		}
		query["aggs"] = map[string]any{"__vega_groups": group}
		query["timeout"] = "30s"
		query["pit"] = map[string]any{"id": pit, "keep_alive": "1m"}
		query["track_total_hits"] = params.NeedTotal && after == nil
		response, err := c.aggregationRequest(ctx, http.MethodPost, "/_search?allow_partial_search_results=false", query, "group_aggregation")
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
			return nil, fmt.Errorf("group aggregation response is missing shard completion metadata")
		}
		if params.NeedTotal && after == nil {
			hits, _ := response["hits"].(map[string]any)
			count, _ := hits["total"].(map[string]any)
			var valid bool
			total, valid = openSearchInt64(count["value"])
			if !valid || total < 0 || count["relation"] != "eq" {
				return nil, fmt.Errorf("group aggregation response is missing an exact document count")
			}
		}
		aggs, _ := response["aggregations"].(map[string]any)
		groups, _ := aggs["__vega_groups"].(map[string]any)
		buckets, ok := groups["buckets"].([]any)
		if !ok {
			return nil, fmt.Errorf("group aggregation response is missing composite buckets")
		}
		if len(buckets) == 0 {
			break
		}
		for _, bucket := range buckets {
			item, _ := bucket.(map[string]any)
			key, _ := item["key"].(map[string]any)
			values, err := compositeGroupKey(key, len(params.GroupBy))
			if err != nil {
				return nil, err
			}
			encodedKey, err := sonic.Marshal(values)
			if err != nil {
				return nil, err
			}
			if _, duplicate := groupKeys[string(encodedKey)]; duplicate {
				return nil, fmt.Errorf("group aggregation returned a duplicate group")
			}
			stateBytes += len(encodedKey) + 128
			if stateBytes > groupedStateBytes {
				return nil, interfaces.NewQueryExecutionBudgetError("group_aggregation_group_memory", nil)
			}
			groupKeys[string(encodedKey)] = struct{}{}
			documents, valid := openSearchInt64(item["doc_count"])
			if !valid || documents <= 0 {
				return nil, fmt.Errorf("group aggregation bucket has an invalid document count")
			}
			row := make(map[string]any, len(values)+1)
			for i, cfg := range params.GroupBy {
				// 字符串与 json.Number 不得引用整页响应，避免隐含保留所有响应体。
				value := values[i]
				switch v := value.(type) {
				case string:
					value = strings.Clone(v)
				case json.Number:
					value = json.Number(strings.Clone(string(v)))
				}
				row[cfg.Property] = value
			}
			var value any
			if params.Aggregation != nil {
				result, _ := item["__vega_metric"].(map[string]any)
				var exists bool
				value, exists = result["value"]
				if !exists {
					return nil, fmt.Errorf("group aggregation bucket is missing its metric")
				}
				if params.Aggregation.Aggr == "sum" {
					count, _ := item["__vega_nonnull"].(map[string]any)
					nonnull, valid := openSearchInt64(count["value"])
					if !valid || nonnull < 0 {
						return nil, fmt.Errorf("group aggregation bucket is missing its non-null count")
					}
					if nonnull == 0 {
						value = nil
					}
				}
				if value != nil {
					switch number := value.(type) {
					case json.Number:
						value = json.Number(strings.Clone(string(number)))
					default:
						return nil, fmt.Errorf("group aggregation returned a nonnumeric metric")
					}
				}
				if value != nil && decimalMetric {
					number, err := decimal.NewFromString(fmt.Sprint(value))
					if err != nil {
						return nil, fmt.Errorf("group aggregation returned an invalid decimal metric")
					}
					// 输出、HAVING、排序及分页必须使用同一个按字段 scale 归一化的值。
					value = json.Number(number.Round(decimalScale).String())
				}
				row[alias] = value
			} else if params.Having != nil && params.Having.Field == "count(*)" {
				row[alias] = documents
			}
			if params.Having != nil {
				havingValue := value
				if params.Having.Field == "count(*)" {
					havingValue = documents
				}
				if !matchesAggregationHaving(havingValue, params.Having.Operation, havingParams) {
					continue
				}
			}
			encoded, err := sonic.Marshal(row)
			if err != nil {
				return nil, err
			}
			stateBytes += 2*len(encoded) + 512 + 128*len(values)
			if stateBytes > groupedStateBytes {
				return nil, interfaces.NewQueryExecutionBudgetError("group_aggregation_group_memory", nil)
			}
			rows = append(rows, row)
		}
		next, _ := groups["after_key"].(map[string]any)
		nextValues, err := compositeGroupKey(next, len(params.GroupBy))
		if err != nil {
			return nil, err
		}
		if after != nil {
			prior, err := compositeGroupKey(after, len(params.GroupBy))
			if err != nil {
				return nil, err
			}
			if compareGroupKeys(prior, nextValues) == 0 {
				return nil, fmt.Errorf("group aggregation continuation did not advance")
			}
		}
		after = next
	}
	sort.Slice(rows, func(i, j int) bool {
		for _, field := range params.Sort {
			name := field.Field
			if alias != "" && name == "__value" {
				name = alias
			}
			if comparison := compareAggregationValues(rows[i][name], rows[j][name]); comparison != 0 {
				if strings.EqualFold(field.Direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		for _, group := range params.GroupBy {
			if comparison := compareAggregationValues(rows[i][group.Property], rows[j][group.Property]); comparison != 0 {
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
		return nil, interfaces.NewQueryExecutionBudgetError("group_aggregation_time", err)
	}
	return &interfaces.ResourceDataQueryResult{Entries: rows, TotalCount: total, IsApproximate: params.Aggregation != nil && params.Aggregation.Aggr == "count_distinct"}, nil
}

func compositeGroupKey(key map[string]any, dimensions int) ([]any, error) {
	if len(key) != dimensions {
		return nil, fmt.Errorf("group aggregation response has invalid composite keys")
	}
	values := make([]any, dimensions)
	for i := range values {
		value, exists := key[strconv.Itoa(i)]
		if !exists {
			return nil, fmt.Errorf("group aggregation response is missing a group key")
		}
		switch value.(type) {
		case nil, string, json.Number, bool:
		default:
			return nil, fmt.Errorf("group aggregation response has a nonscalar group key")
		}
		values[i] = value
	}
	return values, nil
}

func compareGroupKeys(left, right []any) int {
	for i := range left {
		if comparison := compareAggregationValues(left[i], right[i]); comparison != 0 {
			return comparison
		}
	}
	return 0
}
