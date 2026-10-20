// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package filter_condition

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var (
	localDatetimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
	rfc3339Pattern       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)
	timePattern          = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
)

// ValidateTemporalPrecision 限制公开 datetime/time 输入最多 9 位小数秒，不做静默截断。
// 数字时间戳的整数性由格式解析器检查，time.Time 仅存在于服务内部。
func ValidateTemporalPrecision(fieldType string, value any) error {
	if fieldType != interfaces.DataType_Datetime && fieldType != interfaces.DataType_Time {
		return nil
	}
	switch typed := value.(type) {
	case string:
		if dot := strings.IndexByte(typed, '.'); dot >= 0 {
			end := dot + 1
			for end < len(typed) && typed[end] >= '0' && typed[end] <= '9' {
				end++
			}
			if end-dot-1 > 9 {
				return interfaces.NewConditionBuildError("invalid %s value %q; supports at most 9 fractional digits", fieldType, typed)
			}
		}
	case []any:
		for _, item := range typed {
			if err := ValidateTemporalPrecision(fieldType, item); err != nil {
				return err
			}
		}
	case []string:
		for _, item := range typed {
			if err := ValidateTemporalPrecision(fieldType, item); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseTemporalValue 统一解析时间字面值与内部运行时值，保留最多 9 位小数秒。
// datetime 返回绝对时刻，使用服务时区表示供无时区数据库列绑定；date/time 保留日历字符串。
// time.Time 用于服务内部的源端值，HTTP JSON 输入不能表达此类型。
func ParseTemporalValue(fieldType string, value any, timeZone string) (any, error) {
	if err := ValidateTemporalPrecision(fieldType, value); err != nil {
		return nil, err
	}
	switch fieldType {
	case interfaces.DataType_Datetime:
		return parseDatetimeValue(value, timeZone)
	case interfaces.DataType_Date, interfaces.DataType_Time:
		layout := time.DateOnly
		if fieldType == interfaces.DataType_Time {
			layout = common.TimeLayout
		}
		if native, ok := value.(time.Time); ok {
			return native.Format(layout), nil
		}
		if text, ok := value.(string); ok {
			validShape := len(text) == 10
			if fieldType == interfaces.DataType_Time {
				validShape = timePattern.MatchString(text)
			}
			if validShape {
				if parsed, err := time.Parse(layout, text); err == nil {
					return parsed.Format(layout), nil
				}
			}
		}
		format := "YYYY-MM-DD"
		if fieldType == interfaces.DataType_Time {
			format = "HH:mm:ss[.fraction]"
		}
		return nil, interfaces.NewConditionBuildError("invalid %s value %v; supported format: %s", fieldType, value, format)
	default:
		return nil, fmt.Errorf("unsupported temporal type %q", fieldType)
	}
}

func parseDatetimeValue(value any, timeZone string) (time.Time, error) {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return time.Time{}, interfaces.NewServerTimeZoneError()
	}
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.In(location), nil
	}
	if text, ok := value.(string); ok {
		if rfc3339Pattern.MatchString(text) {
			if timestamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
				return timestamp.In(location), nil
			}
		}
		if localDatetimePattern.MatchString(text) {
			if timestamp, err := time.ParseInLocation(common.DatetimeLayout, text, location); err == nil && timestamp.Format(time.DateTime) == text[:19] {
				return timestamp, nil
			}
		}
		if millis, err := strconv.ParseInt(text, 10, 64); err == nil {
			return time.UnixMilli(millis).In(location), nil
		}
	}
	// 精确解码的 JSON 数字必须直接解析为 int64，不能经过 float64 舍入。
	if number, ok := value.(json.Number); ok {
		if millis, err := number.Int64(); err == nil {
			return time.UnixMilli(millis).In(location), nil
		}
	} else if millis, ok := common.NumberAsInt64(value); ok {
		return time.UnixMilli(millis).In(location), nil
	}
	return time.Time{}, interfaces.NewConditionBuildError("invalid datetime value %v; supported formats: RFC3339, YYYY-MM-DD HH:mm:ss[.fraction], or integer epoch milliseconds", value)
}

// normalizeTemporalCondition 创建运行时副本，原始配置及数组元素保持不变。
// before/current 使用相对日历参数，字段比较不包含时间字面值。
func normalizeTemporalCondition(cfg *interfaces.FilterCondCfg, fields map[string]*interfaces.Property) (*interfaces.FilterCondCfg, error) {
	field := fields[cfg.Name]
	if field == nil || (field.Type != interfaces.DataType_Date && field.Type != interfaces.DataType_Datetime && field.Type != interfaces.DataType_Time) || cfg.ValueFrom != interfaces.ValueFrom_Const {
		return cfg, nil
	}
	switch cfg.Operation {
	case OperationEqual, OperationEqual2, OperationNotEqual, OperationNotEqual2,
		OperationGt, OperationGt2, OperationGte, OperationGte2, OperationLt, OperationLt2, OperationLte, OperationLte2,
		OperationIn, OperationNotIn, OperationRange, OperationOutRange, OperationBetween:
	default:
		return cfg, nil
	}
	copy := *cfg
	values := []any{cfg.Value}
	array, isArray := cfg.Value.([]any)
	if isArray {
		values = array
	}
	normalized := make([]any, len(values))
	for i, value := range values {
		if native, ok := value.(time.Time); ok && field.Type == interfaces.DataType_Datetime {
			nativeType := strings.ToLower(field.OriginalType)
			if nativeType != "" && nativeType != "timestamptz" && !strings.Contains(nativeType, "with time zone") && !strings.Contains(nativeType, "with local time zone") && !strings.Contains(nativeType, "datetimeoffset") {
				// 无时区源列的原生游标承载墙上时间，不把驱动附带的 UTC 载体误认成绝对时刻。
				location, err := time.LoadLocation(common.ServerTimeZone())
				if err != nil {
					return nil, interfaces.NewServerTimeZoneError()
				}
				value = time.Date(native.Year(), native.Month(), native.Day(), native.Hour(), native.Minute(), native.Second(), native.Nanosecond(), location)
			}
		}
		parsed, err := ParseTemporalValue(field.Type, value, common.ServerTimeZone())
		if err != nil {
			return nil, fmt.Errorf("condition [%s] field %q: %w", cfg.Operation, cfg.Name, err)
		}
		normalized[i] = parsed
	}
	if isArray {
		copy.Value = normalized
	} else {
		copy.Value = normalized[0]
	}
	return &copy, nil
}

// HavingTemporalField 返回 min/max 保留的时间类型；计数等聚合结果仍为数值。
func HavingTemporalField(params *interfaces.ResourceDataQueryParams, fields map[string]*interfaces.Property) *interfaces.Property {
	if params.Having == nil || params.Aggregation == nil || params.Having.Field == "count(*)" {
		return nil
	}
	if params.Aggregation.Aggr != "min" && params.Aggregation.Aggr != "max" {
		return nil
	}
	field := fields[params.Aggregation.Property]
	if field == nil || (field.Type != interfaces.DataType_Date &&
		field.Type != interfaces.DataType_Datetime &&
		field.Type != interfaces.DataType_Time) {
		return nil
	}
	return field
}

// ValidateHavingTemporalValues 在查询前校验时间聚合条件的完整输入契约，不修改原始参数。
func ValidateHavingTemporalValues(params *interfaces.ResourceDataQueryParams, fields map[string]*interfaces.Property) error {
	field := HavingTemporalField(params, fields)
	if field == nil {
		return nil
	}
	values := []any{params.Having.Value}
	switch array := params.Having.Value.(type) {
	case []any:
		values = array
	case []string:
		values = make([]any, len(array))
		for i, value := range array {
			values[i] = value
		}
	}
	for _, value := range values {
		if _, err := ParseTemporalValue(field.Type, value, common.ServerTimeZone()); err != nil {
			return fmt.Errorf("HAVING field %q: %w", field.Name, err)
		}
	}
	return nil
}
