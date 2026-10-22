// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package table provides base implementation for relational database connectors.
package table

import (
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// convertValue converts []byte to string for MariaDB driver compatibility
func convertValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// ScanRows scans SQL rows into QueryResult.
func ScanRows(rows *sql.Rows) (*interfaces.QueryResult, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := &interfaces.QueryResult{
		Columns: columns,
		Entries: make([]map[string]any, 0),
	}

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = convertValue(values[i])
		}
		result.Entries = append(result.Entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result.Total = int64(len(result.Entries))
	return result, nil
}

// NormalizeQueryResult 归一化 JSON 空值，并标记内部查询未请求读取的 Binary。
func NormalizeQueryResult(result *interfaces.QueryResult, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) {
	if result == nil || resource == nil || params == nil || params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		return
	}
	selected := make(map[string]bool, len(params.OutputFields))
	for _, name := range params.OutputFields {
		selected[name] = true
	}
	for _, field := range resource.SchemaDefinition {
		if field == nil ||
			(len(selected) > 0 && !selected[field.Name]) {
			continue
		}
		if field.Type == interfaces.DataType_Json {
			for _, entry := range result.Entries {
				if value, exists := entry[field.Name]; exists {
					entry[field.Name] = common.JSONSourceValueText(value)
				}
			}
			continue
		}
		if field.Type != interfaces.DataType_Binary || params.BinaryMode != nil {
			continue
		}
		for _, entry := range result.Entries {
			if _, exists := entry[field.Name]; exists {
				entry[field.Name] = interfaces.ResourceValue{
					Mode:   interfaces.ResourceValueModeUnavailable,
					Reason: interfaces.ResourceValueReasonNotRequested,
				}
			}
		}
	}
}

// ValidateAggregateAlias 拒绝聚合列与同时输出的分组列重名，不限制未输出的源字段名。
func ValidateAggregateAlias(alias string, groups []*interfaces.GroupByItem) error {
	if alias == "" {
		return nil
	}
	for _, group := range groups {
		if group != nil && group.Property == alias {
			return interfaces.NewConditionBuildError("aggregate alias conflicts with a group field")
		}
	}
	return nil
}

// QueryBinaryContentColumns 按实际投影确定需要保留二进制内容的结果列。
func QueryBinaryContentColumns(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) map[string]bool {
	columns := make(map[string]bool)
	if params.BinaryMode == nil || *params.BinaryMode != interfaces.BinaryModeContent {
		return columns
	}
	binaryFields := make(map[string]bool)
	for _, field := range resource.SchemaDefinition {
		if field != nil && field.Type == interfaces.DataType_Binary {
			binaryFields[field.Name] = true
		}
	}
	if params.Aggregation == nil && len(params.GroupBy) == 0 && params.Having == nil {
		return binaryFields
	}
	for _, group := range params.GroupBy {
		if group != nil && binaryFields[group.Property] {
			columns[group.Property] = true
		}
	}
	if agg := params.Aggregation; agg != nil && (agg.Aggr == "min" || agg.Aggr == "max") && binaryFields[agg.Property] {
		alias := agg.Alias
		if alias == "" {
			alias = "__value"
		}
		columns[alias] = true
	}
	return columns
}
