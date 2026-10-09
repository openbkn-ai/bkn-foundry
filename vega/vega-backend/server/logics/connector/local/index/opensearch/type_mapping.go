// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package opensearch provides OpenSearch/ElasticSearch connector implementation.
package opensearch

import (
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// TypeMapping maps OpenSearch native types to VEGA types.
var TypeMapping = map[string]string{
	// 字符串
	"keyword":               interfaces.DataType_String,
	"constant_keyword":      interfaces.DataType_String,
	"wildcard":              interfaces.DataType_String,
	"icu_collation_keyword": interfaces.DataType_String,
	"completion":            interfaces.DataType_String,
	"version":               interfaces.DataType_String,
	"murmur3":               interfaces.DataType_String,

	// 全文文本
	"text":               interfaces.DataType_Text,
	"match_only_text":    interfaces.DataType_Text,
	"search_as_you_type": interfaces.DataType_Text,

	// 整数
	"byte":        interfaces.DataType_Integer,
	"short":       interfaces.DataType_Integer,
	"integer":     interfaces.DataType_Integer,
	"long":        interfaces.DataType_Integer,
	"token_count": interfaces.DataType_Integer,

	// 无符号整数
	"unsigned_long": interfaces.DataType_UnsignedInteger,

	// 浮点数
	"float":        interfaces.DataType_Float,
	"half_float":   interfaces.DataType_Float,
	"scaled_float": interfaces.DataType_Float,
	"double":       interfaces.DataType_Float,
	"rank_feature": interfaces.DataType_Float,

	// 布尔值
	"boolean": interfaces.DataType_Boolean,

	// 日期时间
	"date":       interfaces.DataType_Datetime,
	"date_nanos": interfaces.DataType_Datetime,

	// 二进制
	"binary": interfaces.DataType_Binary,

	// JSON 与结构化数据
	// 区间值是结构化数据，不能按标量字符串或数值声明。
	"integer_range": interfaces.DataType_Json,
	"float_range":   interfaces.DataType_Json,
	"long_range":    interfaces.DataType_Json,
	"double_range":  interfaces.DataType_Json,
	"date_range":    interfaces.DataType_Json,
	"ip_range":      interfaces.DataType_Json,
	"object":        interfaces.DataType_Json,
	"nested":        interfaces.DataType_Json,
	"percolator":    interfaces.DataType_Json,
	"join":          interfaces.DataType_Json,
	"rank_features": interfaces.DataType_Json,
	// 稀疏向量是键值对象，不能复用稠密向量的 knn 条件。
	"sparse_vector":           interfaces.DataType_Json,
	"flattened":               interfaces.DataType_Json,
	"flat_object":             interfaces.DataType_Json,
	"aggregate_metric_double": interfaces.DataType_Json,

	// 暂不启用 IP 和地理专用类型，以下字段统一走默认的 other。
	// "ip":        interfaces.DataType_Ip,
	// "geo_point": interfaces.DataType_Point,
	// "geo_shape": interfaces.DataType_Shape,

	// 稠密向量
	"knn_vector": interfaces.DataType_Vector,

	// 别名需要根据 path 解析目标类型；缺少上下文时保守返回 other。
	"alias": interfaces.DataType_Other,
}

// MapType returns VEGA type for OpenSearch native type.
func (c *OpenSearchConnector) MapType(nativeType string) string {
	if vegaType, ok := TypeMapping[nativeType]; ok {
		return vegaType
	}
	return interfaces.DataType_Other // default
}
