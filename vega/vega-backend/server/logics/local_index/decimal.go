// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package local_index

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var decimalTypePattern = regexp.MustCompile(`(?i)^\s*(?:decimal|numeric|number|dec|fixed)\s*\(\s*([0-9]+)\s*,\s*([0-9]+)\s*\)(?:\s+unsigned)?\s*$`)

// DecimalIndexConfig 解析元数据，不缩小源端可查询的 DECIMAL 类型范围。
// 源列元数据优先，其次是 Dataset 的显式属性及历史资源保存的完整原生类型。
// 未知精度返回 precision=0，只有实际创建/使用托管索引时才拒绝。
func DecimalIndexConfig(property *interfaces.Property, column interfaces.TableColumnMeta) (*interfaces.DecimalIndexConfig, error) {
	config := &interfaces.DecimalIndexConfig{}
	if column.NumPrecision > 0 {
		config.Precision, config.Scale = column.NumPrecision, column.NumScale
		return config, nil
	}
	precision, hasPrecision := property.Attributes["precision"]
	scale, hasScale := property.Attributes["scale"]
	if hasPrecision || hasScale {
		p, validPrecision := common.NumberAsInt64(precision)
		s, validScale := common.NumberAsInt64(scale)
		if !hasPrecision || !hasScale || !validPrecision || !validScale || p < 1 || p > math.MaxInt || s < 0 || s > p {
			return nil, interfaces.NewDecimalIndexError(property.Name, "invalid_precision_scale")
		}
		config.Precision, config.Scale = int(p), int(s)
		return config, nil
	}
	if match := decimalTypePattern.FindStringSubmatch(property.OriginalType); match != nil {
		p, pErr := strconv.Atoi(match[1])
		s, sErr := strconv.Atoi(match[2])
		if pErr != nil || sErr != nil {
			return nil, interfaces.NewDecimalIndexError(property.Name, "invalid_precision_scale")
		}
		config.Precision, config.Scale = p, s
	}
	return config, nil
}

// ValidateDecimalIndexConfig 只校验声明和缩放因子，实际值的编码能力在写入时判断。
func ValidateDecimalIndexConfig(field string, config *interfaces.DecimalIndexConfig) error {
	if config.Precision == 0 {
		return interfaces.NewDecimalIndexError(field, "missing_precision_scale")
	}
	if config.Precision < 1 || config.Scale < 0 || config.Scale > config.Precision {
		return interfaces.NewDecimalIndexError(field, "invalid_precision_scale")
	}
	factor := math.Pow10(config.Scale)
	if math.IsInf(factor, 0) || factor <= 0 {
		return interfaces.NewDecimalIndexError(field, "unsupported_scaling_factor")
	}
	return nil
}

// DecimalIndexSchema 在传入的 schema 中原地补齐 Decimal 字段的缩放元数据。
func DecimalIndexSchema(schema []*interfaces.Property, columns map[string]interfaces.TableColumnMeta) ([]*interfaces.Property, error) {
	for _, property := range schema {
		if property == nil || property.Type != interfaces.DataType_Decimal {
			continue
		}
		config, err := DecimalIndexConfig(property, columns[property.OriginalName])
		if err != nil {
			return nil, err
		}
		if err := ValidateDecimalIndexConfig(property.Name, config); err != nil {
			return nil, err
		}
		if property.Attributes == nil {
			property.Attributes = make(map[string]any, 2)
		}
		property.Attributes["precision"] = config.Precision
		property.Attributes["scale"] = config.Scale
	}
	return schema, nil
}

// ValidateDecimalDocument 在写入前校验完整精度及缩放往返，不改写原始金额。
func ValidateDecimalDocument(resource *interfaces.Resource, document map[string]any) error {
	columns := resource.ColumnMetadata()
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Type != interfaces.DataType_Decimal {
			continue
		}
		value, exists := document[property.Name]
		if !exists || value == nil {
			continue
		}
		config, err := DecimalIndexConfig(property, columns[property.OriginalName])
		if err != nil {
			return err
		}
		if err := ValidateDecimalIndexConfig(property.Name, config); err != nil {
			return err
		}
		factor := math.Pow10(config.Scale)
		if values, ok := value.([]any); ok {
			for _, item := range values {
				if item != nil {
					if err := validateDecimalValue(property.Name, config, factor, item); err != nil {
						return err
					}
				}
			}
		} else if err := validateDecimalValue(property.Name, config, factor, value); err != nil {
			return err
		}
	}
	return nil
}

func validateDecimalValue(field string, config *interfaces.DecimalIndexConfig, factor float64, value any) error {
	precision, scale := config.Precision, config.Scale
	if precision < 1 || scale < 0 || scale > precision {
		return interfaces.NewDecimalIndexError(field, "invalid_precision_scale")
	}
	if scale > 308 {
		return interfaces.NewDecimalIndexError(field, "unsupported_scaling_factor")
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = string(v)
	case float64:
		text = strconv.FormatFloat(v, 'g', -1, 64)
	case float32:
		text = strconv.FormatFloat(float64(v), 'g', -1, 32)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		text = fmt.Sprint(v)
	default:
		return interfaces.NewDecimalIndexError(field, "invalid_value")
	}
	// 在解析前限制输入与指数，避免恶意声明引起无界十进制展开。
	if len(text) == 0 || len(text) > 1024 || strings.TrimSpace(text) != text {
		return interfaces.NewDecimalIndexError(field, "invalid_value")
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.ParseInt(text[index+1:], 10, 32)
		if err != nil || exponent < -1024 || exponent > 1024 {
			return interfaces.NewDecimalIndexError(field, "invalid_value")
		}
	}
	number, err := decimal.NewFromString(text)
	if err != nil {
		return interfaces.NewDecimalIndexError(field, "invalid_value")
	}
	scaled := number.Shift(int32(scale))
	if !scaled.Equal(scaled.Truncate(0)) || scaled.LessThan(decimal.NewFromInt(math.MinInt64)) || scaled.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return interfaces.NewDecimalIndexError(field, "value_out_of_range")
	}
	// long 最多 19 位；更大的声明精度无需构造可能极大的 10^precision。
	if precision < 19 && scaled.Abs().GreaterThanOrEqual(decimal.New(1, int32(precision))) {
		return interfaces.NewDecimalIndexError(field, "value_out_of_range")
	}
	floating, err := strconv.ParseFloat(text, 64)
	scaledFloat := floating * factor
	if err != nil || math.IsNaN(scaledFloat) || scaledFloat >= 0x1p63 || scaledFloat < -0x1p63 {
		return interfaces.NewDecimalIndexError(field, "unsafe_scaled_value")
	}
	// OpenSearch 使用 Java Math.round：半值向正无穷方向舍入。
	rounded := math.Floor(scaledFloat)
	if scaledFloat-rounded >= 0.5 {
		rounded++
	}
	encoded := int64(rounded)
	if !scaled.Equal(decimal.NewFromInt(encoded)) {
		return interfaces.NewDecimalIndexError(field, "unsafe_scaled_value")
	}
	// doc values 将 long 乘以缩放因子的倒数。按声明小数位恢复后也必须与原值一致。
	decoded := float64(encoded) * (1 / factor)
	if !decimal.NewFromFloat(decoded).Round(int32(scale)).Equal(number) {
		return interfaces.NewDecimalIndexError(field, "unsafe_scaled_value")
	}
	return nil
}

// decimalAggregateConfig 解析需要按输入 scale 归一化的聚合定义；其他聚合返回零值。
func decimalAggregateConfig(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (interfaces.DecimalIndexConfig, error) {
	if params == nil || params.Aggregation == nil {
		return interfaces.DecimalIndexConfig{}, nil
	}
	aggregation := params.Aggregation
	if aggregation.Aggr != "sum" && aggregation.Aggr != "min" && aggregation.Aggr != "max" {
		return interfaces.DecimalIndexConfig{}, nil
	}
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name != aggregation.Property || property.Type != interfaces.DataType_Decimal {
			continue
		}
		config, err := DecimalIndexConfig(property, resource.ColumnMetadata()[property.OriginalName])
		if err != nil {
			return interfaces.DecimalIndexConfig{}, err
		}
		if err := ValidateDecimalIndexConfig(property.Name, config); err != nil {
			return interfaces.DecimalIndexConfig{}, err
		}
		return *config, nil
	}
	return interfaces.DecimalIndexConfig{}, nil
}

// normalizeDecimalAggregate 与引擎内 Decimal HAVING 使用相同的 HALF_UP 和字段 scale。
func normalizeDecimalAggregate(config interfaces.DecimalIndexConfig, params *interfaces.ResourceDataQueryParams, result *interfaces.ResourceDataQueryResult) error {
	if config.Precision == 0 {
		return nil
	}
	scale := config.Scale
	if scale < 0 || scale > 308 {
		return interfaces.NewDecimalIndexError(params.Aggregation.Property, "unsupported_scaling_factor")
	}
	alias := params.Aggregation.Alias
	if alias == "" {
		alias = "__value"
	}
	for _, entry := range result.Entries {
		value := entry[alias]
		if value == nil {
			continue
		}
		number, err := decimal.NewFromString(fmt.Sprint(value))
		if err != nil {
			return interfaces.NewDecimalIndexError(params.Aggregation.Property, "invalid_aggregate_value")
		}
		entry[alias] = json.Number(number.Round(int32(scale)).String())
	}
	return nil
}
