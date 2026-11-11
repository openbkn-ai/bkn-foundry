// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package table provides base implementation for relational database connectors.
package table

import (
	"database/sql"
	"fmt"

	"github.com/bytedance/sonic"

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

// ScanRows scans SQL rows into ResourceDataQueryResult.
func ScanRows(rows *sql.Rows) (*interfaces.ResourceDataQueryResult, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := &interfaces.ResourceDataQueryResult{
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

	result.TotalCount = int64(len(result.Entries))
	return result, nil
}

// NormalizeQueryResult 归一化 JSON 空值，并标记内部查询未请求读取的 Binary。
func NormalizeQueryResult(result *interfaces.ResourceDataQueryResult,
	resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) {
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

// QuerySortFields 解析聚合值引用，并按分组顺序补齐并列排序；不修改调用方的排序参数。
func QuerySortFields(params *interfaces.ResourceDataQueryParams, aggregateAlias string) []*interfaces.SortField {
	fields := make([]*interfaces.SortField, 0, len(params.Sort)+len(params.GroupBy))
	covered := make(map[string]bool, len(params.Sort)+len(params.GroupBy))
	for _, field := range params.Sort {
		resolved := *field
		if aggregateAlias != "" && resolved.Field == "__value" {
			resolved.Field = aggregateAlias
		}
		fields = append(fields, &resolved)
		covered[resolved.Field] = true
	}
	for _, group := range params.GroupBy {
		if !covered[group.Property] {
			fields = append(fields, &interfaces.SortField{Field: group.Property, Direction: interfaces.ASC_DIRECTION})
			covered[group.Property] = true
		}
	}
	return fields
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

// ScanSQLRow reads one SQL result row using standard database/sql driver values.
// Binary bytes stay intact; other byte values retain their exact text representation.
func ScanSQLRow(rows *sql.Rows, columns []interfaces.ColumnInfo) (map[string]any, error) {
	if !rows.Next() {
		return nil, rows.Err()
	}
	values := make([]any, len(columns))
	destinations := make([]any, len(values))
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(values))
	for i, column := range columns {
		value := values[i]
		if data, ok := value.([]byte); ok && column.Type != interfaces.DataType_Binary {
			value = string(data)
		}
		row[column.Name] = value
	}
	return row, nil
}

// ValidateFilterability 使用外部解析的真实源列校验显式开启字段，缺列时返回错误。
func ValidateFilterability(resource *interfaces.Resource, connector interfaces.TableConnector, columns map[string]interfaces.TableColumnMeta) error {
	for _, property := range resource.SchemaDefinition {
		if property.Filterable == nil || !*property.Filterable {
			continue
		}
		name := property.OriginalName
		if name == "" {
			name = property.Name
		}
		column, exists := columns[name]
		if !exists {
			return interfaces.NewConditionBuildError("field %q does not exist in source column metadata", property.Name)
		}
		if column.Type == "" {
			return interfaces.NewConditionBuildError("cannot confirm filterability for field %q without source type metadata", property.Name)
		}
		if !connector.IsFilterable(&column) {
			return interfaces.NewConditionBuildError("field %q does not support filterable=true", property.Name)
		}
	}
	return nil
}

// ColumnMetadata 还原已加载资源中的源列元数据；缺失时返回 nil，损坏时返回错误。
func ColumnMetadata(r *interfaces.Resource) (map[string]interfaces.TableColumnMeta, error) {
	if r == nil || r.SourceMetadata == nil {
		return nil, nil //nolint:nilnil // 历史资源缺少列元数据是正常缺省状态。
	}
	raw, ok := r.SourceMetadata["columns"]
	if !ok {
		return nil, nil //nolint:nilnil // 历史资源缺少列元数据是正常缺省状态。
	}
	columns, ok := raw.([]interfaces.TableColumnMeta)
	if !ok {
		encoded, err := sonic.Marshal(raw)
		if err != nil || sonic.Unmarshal(encoded, &columns) != nil {
			return nil, fmt.Errorf("invalid stored source column metadata")
		}
	}
	if columns == nil {
		return nil, fmt.Errorf("invalid stored source column metadata")
	}
	result := make(map[string]interfaces.TableColumnMeta, len(columns))
	for _, column := range columns {
		result[column.Name] = column
	}
	return result, nil
}
