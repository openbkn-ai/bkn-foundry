// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

var temporalNativeType = regexp.MustCompile(`(?i)^(DATETIME2?|SMALLDATETIME|SECONDDATE|TIMESTAMP|TIMESTAMPTZ|DATETIMEOFFSET|TIME|SECONDTIME)(?:\s*\(\s*([0-9]+)\s*\))?(?:\s+(WITHOUT TIME ZONE|WITH TIME ZONE|WITH LOCAL TIME ZONE))?$`)

// temporalPropertyType 只依据明确的原生语义分类。跨连接器含义不同的简写保持原值投影，
// 等资源适配器提供足够元数据后再开放计算，不从常量值或查询目标反推整列类型。
func temporalPropertyType(property *interfaces.Property, origin qm.Origin) (qm.LogicalType, qm.NativeType, error) {
	native := qm.NativeType{Name: property.OriginalType, Domain: qm.DomainVega}
	typ := qm.LogicalType{Kind: qm.TypeOther}
	name := strings.ToUpper(strings.TrimSpace(property.OriginalType))
	if name == "DATE_NANOS" && property.Type == interfaces.DataType_Datetime {
		precision := 9
		typ = qm.LogicalType{Kind: qm.TypeInstant, TimeZone: qm.TimeZoneInstant, TimePrecision: &precision}
		native.TimeZone, native.TimePrecision = typ.TimeZone, typ.TimePrecision
		return typ, native, nil
	}
	parts := temporalNativeType.FindStringSubmatch(name)
	if parts == nil {
		return typ, native, nil
	}
	if parts[2] != "" {
		precision, err := strconv.Atoi(parts[2])
		if err != nil || precision < 0 || precision > 9 {
			return typ, native, bindFailure(origin, "源时间精度无效")
		}
		native.TimePrecision = &precision
	}
	switch {
	case property.Type == interfaces.DataType_Time:
		if (parts[1] == "TIME" && parts[3] == "WITHOUT TIME ZONE") || parts[1] == "SECONDTIME" {
			typ.Kind, native.TimeZone = qm.TypeTimeOfDay, qm.TimeZoneNone
		}
	case parts[1] == "TIMESTAMPTZ" || parts[1] == "DATETIMEOFFSET" || parts[3] == "WITH TIME ZONE" || parts[3] == "WITH LOCAL TIME ZONE":
		// 带时区的日内时间不能误作完整时间点。
		if parts[1] != "TIME" && parts[1] != "SECONDTIME" {
			typ.Kind, native.TimeZone = qm.TypeInstant, qm.TimeZoneInstant
		}
	case parts[1] == "DATETIME" || parts[1] == "DATETIME2" || parts[1] == "SMALLDATETIME" || parts[1] == "SECONDDATE" || (parts[1] == "TIMESTAMP" && parts[3] == "WITHOUT TIME ZONE"):
		typ.Kind, native.TimeZone = qm.TypeLocalDateTime, qm.TimeZoneLocal
	}
	if typ.Kind != qm.TypeOther {
		typ.TimeZone, typ.TimePrecision = native.TimeZone, native.TimePrecision
	}
	return typ, native, nil
}

// sourceTemporalPropertyType 在目录类型明确后解释跨源含义不同的 TIME/TIMESTAMP。
func sourceTemporalPropertyType(property *interfaces.Property, connector string, origin qm.Origin) (qm.LogicalType, qm.NativeType, error) {
	typ, native, err := temporalPropertyType(property, origin)
	if err != nil {
		return typ, native, err
	}
	name := strings.ToUpper(strings.TrimSpace(property.OriginalType))
	base := name
	if index := strings.IndexByte(base, '('); index >= 0 {
		base = strings.TrimSpace(base[:index])
	}
	if (base == "TIME" || base == "TIMESTAMP") && !temporalNativeType.MatchString(name) {
		return typ, native, nil
	}
	if property.Type == interfaces.DataType_Time && base != "TIME" && base != "SECONDTIME" {
		return typ, native, nil
	}
	// 显式时区限定已由通用解析器识别，不能被简写规则覆盖。
	if strings.Contains(name, " TIME ZONE") || strings.Contains(name, "TIMESTAMPTZ") || strings.HasPrefix(name, "DATETIMEOFFSET") {
		return typ, native, nil
	}
	switch connector {
	case interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB:
		if base == "TIMESTAMP" {
			typ.Kind, native.TimeZone = qm.TypeInstant, qm.TimeZoneInstant
		}
		if base == "TIME" && property.Type == interfaces.DataType_Time {
			typ.Kind, native.TimeZone = qm.TypeDuration, qm.TimeZoneNone
		}
	case interfaces.ConnectorTypePostgreSQL:
		if base == "TIMESTAMP" {
			typ.Kind, native.TimeZone = qm.TypeLocalDateTime, qm.TimeZoneLocal
		}
		if base == "TIME" && property.Type == interfaces.DataType_Time {
			typ.Kind, native.TimeZone = qm.TypeTimeOfDay, qm.TimeZoneNone
		}
	case interfaces.ConnectorTypeOracle:
		if base == "DATE" || base == "TIMESTAMP" {
			typ.Kind, native.TimeZone = qm.TypeLocalDateTime, qm.TimeZoneLocal
		}
	case interfaces.ConnectorTypeHANA:
		if base == "TIMESTAMP" || base == "LONGDATE" {
			typ.Kind, native.TimeZone = qm.TypeLocalDateTime, qm.TimeZoneLocal
		}
		if base == "TIME" && property.Type == interfaces.DataType_Time {
			typ.Kind, native.TimeZone = qm.TypeTimeOfDay, qm.TimeZoneNone
		}
	case interfaces.ConnectorTypeSQLServer:
		if base == "TIME" && property.Type == interfaces.DataType_Time {
			typ.Kind, native.TimeZone = qm.TypeTimeOfDay, qm.TimeZoneNone
		}
	case interfaces.ConnectorTypeDoris:
		if base == "DATETIMEV2" {
			copy := *property
			copy.OriginalType = strings.Replace(name, "DATETIMEV2", "DATETIME", 1)
			typ, native, err := temporalPropertyType(&copy, origin)
			native.Name = property.OriginalType
			return typ, native, err
		}
	case interfaces.ConnectorTypeOpenSearch:
		if base == "DATE" {
			precision := 3
			typ.Kind, native.TimeZone, native.TimePrecision = qm.TypeInstant, qm.TimeZoneInstant, &precision
		}
	}
	if typ.Kind != qm.TypeOther {
		typ.TimeZone, typ.TimePrecision = native.TimeZone, native.TimePrecision
	}
	return typ, native, nil
}

func temporalType(typ qm.LogicalType) bool {
	switch typ.Kind {
	case qm.TypeDate, qm.TypeLocalDateTime, qm.TypeInstant, qm.TypeTimeOfDay:
		return true
	default:
		return false
	}
}

// bindTemporalLiteral 复用公开输入的纯解析规则，但只使用本次查询冻结的时区。
// 字面值保留自己的精度；commonType 再推导共同类型，不把列精度当作截断规则。
func bindTemporalLiteral(expr *qm.Sqlizer, target qm.LogicalType, semantics qm.SemanticContext) (*qm.Sqlizer, error) {
	if expr.Kind != qm.ExprLiteral || expr.Value == nil {
		return nil, bindFailure(expr.Origin, "时间常量结构无效")
	}
	var raw any
	switch value := expr.Value.Data.(type) {
	case qm.StringValue:
		raw = string(value)
	case qm.IntegerValue:
		millis, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return nil, bindFailure(expr.Origin, "时间戳必须为 int64 毫秒整数")
		}
		raw = millis
	default:
		return nil, bindFailure(expr.Origin, "时间常量类型无效")
	}
	fieldType := interfaces.DataType_Datetime
	switch target.Kind {
	case qm.TypeDate:
		fieldType = interfaces.DataType_Date
	case qm.TypeTimeOfDay, qm.TypeDuration:
		fieldType = interfaces.DataType_Time
	}
	parsed, err := filter_condition.ParseTemporalValue(fieldType, raw, semantics.TimeZone)
	if err != nil {
		return nil, bindFailure(expr.Origin, "时间常量格式、精度或值域无效")
	}
	value := qm.Value{Type: target}
	precision := temporalLiteralPrecision(raw)
	switch target.Kind {
	case qm.TypeDate:
		day, parseErr := time.Parse(time.DateOnly, parsed.(string))
		if parseErr != nil {
			return nil, bindFailure(expr.Origin, "日期常量无效")
		}
		value.Data = qm.DateValue{Year: day.Year(), Month: day.Month(), Day: day.Day()}
	case qm.TypeTimeOfDay, qm.TypeDuration:
		clock, parseErr := time.Parse(common.TimeLayout, parsed.(string))
		if parseErr != nil {
			return nil, bindFailure(expr.Origin, "日内时间常量无效")
		}
		value.Data = qm.TimeOfDayValue{Hour: clock.Hour(), Minute: clock.Minute(), Second: clock.Second(), Nanosecond: clock.Nanosecond()}
		if target.Kind == qm.TypeDuration {
			nanos := int64(clock.Hour()*3600+clock.Minute()*60+clock.Second())*int64(time.Second) + int64(clock.Nanosecond())
			value.Data = qm.DurationValue(strconv.FormatInt(nanos, 10))
		}
		value.Type.TimePrecision = &precision
	case qm.TypeLocalDateTime:
		instant := parsed.(time.Time).In(semantics.Location)
		value.Data = qm.LocalDateTimeValue{Date: qm.DateValue{Year: instant.Year(), Month: instant.Month(), Day: instant.Day()},
			Time: qm.TimeOfDayValue{Hour: instant.Hour(), Minute: instant.Minute(), Second: instant.Second(), Nanosecond: instant.Nanosecond()}}
		value.Type.TimePrecision = &precision
	case qm.TypeInstant:
		value.Data = qm.InstantValue{Time: parsed.(time.Time).UTC()}
		value.Type.TimePrecision = &precision
	default:
		return nil, bindFailure(expr.Origin, "时间常量的目标类型无效")
	}
	return &qm.Sqlizer{Kind: qm.ExprLiteral, Type: value.Type, Value: &value, Origin: expr.Origin, Domain: qm.DomainVega}, nil
}

func temporalLiteralPrecision(raw any) int {
	text, ok := raw.(string)
	if !ok {
		return 3 // 整数输入的单位是毫秒。
	}
	if _, err := strconv.ParseInt(text, 10, 64); err == nil {
		return 3
	}
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		end := dot + 1
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		return end - dot - 1
	}
	return 0
}

func (b *queryBinder) coerceComparison(expr *qm.Sqlizer, scope *bindingScope) error {
	if (expr.Origin.Kind == qm.OriginDTO || expr.Origin.Kind == qm.OriginSavedView) && len(expr.Args) > 1 {
		target := expr.Args[0].Type
		public := expressionEncoding(expr.Args[0], scope).PublicType
		// 公共 timestamp 不自动继承 datetime 的 HTTP 输入接受集。
		if (temporalType(target) || target.Kind == qm.TypeDuration) && (public == interfaces.DataType_Date || public == interfaces.DataType_Datetime || public == interfaces.DataType_Time) {
			for i, arg := range expr.Args[1:] {
				if arg.Kind != qm.ExprLiteral || arg.Type.Kind == qm.TypeNull {
					continue
				}
				bound, err := bindTemporalLiteral(arg, target, b.semantics)
				if err != nil {
					return err
				}
				expr.Args[i+1] = bound
			}
		}
	}
	return coerceArguments(expr, true)
}

func bindTemporalCastLiteral(arg *qm.Sqlizer, target qm.LogicalType, semantics qm.SemanticContext) (*qm.Sqlizer, error) {
	bound, err := bindTemporalLiteral(arg, target, semantics)
	if err != nil {
		return nil, err
	}
	nanos := 0
	switch value := bound.Value.Data.(type) {
	case qm.TimeOfDayValue:
		nanos = value.Nanosecond
	case qm.LocalDateTimeValue:
		nanos = value.Time.Nanosecond
	}
	if target.TimePrecision != nil {
		unit := 1
		for precision := *target.TimePrecision; precision < 9; precision++ {
			unit *= 10
		}
		if nanos%unit != 0 {
			return nil, bindFailure(arg.Origin, "时间 CAST 不能静默丢失小数秒精度")
		}
	}
	bound.Type, bound.Value.Type = target, target
	return bound, nil
}
