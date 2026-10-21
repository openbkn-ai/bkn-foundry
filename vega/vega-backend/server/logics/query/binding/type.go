// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

var decimalNativeType = regexp.MustCompile(`(?i)^(?:DECIMAL|NUMERIC|NUMBER)\s*\(\s*([0-9]+)\s*(?:,\s*([0-9]+)\s*)?\)(?:\s+UNSIGNED)?$`)
var signedNumber = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// NewSemanticContext 解析编排层传入的服务 TZ，不读取环境变量，不接受客户端时区。
// 无效 TZ 属于服务配置错误；空值按已批准规则回退 UTC。
func NewSemanticContext(zone string) (qm.SemanticContext, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return qm.SemanticContext{}, &qm.CompileError{Kind: qm.ErrorConfiguration, Message: "服务查询时区配置无效"}
	}
	digest := sha256.Sum256([]byte(qm.CurrentSemanticsVersion + "\x00" + zone))
	fingerprint := hex.EncodeToString(digest[:])
	return qm.SemanticContext{Version: qm.CurrentSemanticsVersion, TimeZone: zone, Location: location, TimeZoneRevision: fingerprint,
		Comparison: qm.ComparisonPolicy{StringRule: "utf8_binary", CaseSensitive: true, TrailingSpaceSignificant: true}, TypeRulesVersion: qm.CurrentSemanticsVersion, ConfigFingerprint: fingerprint}, nil
}

func propertyType(property *interfaces.Property, origin qm.Origin) (qm.LogicalType, qm.NativeType, error) {
	native := qm.NativeType{Name: property.OriginalType, Domain: qm.DomainVega}
	var typ qm.LogicalType
	switch property.Type {
	case interfaces.DataType_Integer:
		typ = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}
	case interfaces.DataType_UnsignedInteger:
		typ = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}
	case interfaces.DataType_Float:
		typ = qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}
	case interfaces.DataType_Decimal:
		parts := decimalNativeType.FindStringSubmatch(strings.TrimSpace(property.OriginalType))
		if parts == nil {
			name := strings.ToUpper(strings.TrimSpace(property.OriginalType))
			if name == "DECIMAL" || name == "NUMERIC" || name == "NUMBER" {
				typ.Kind, native.Domain = qm.TypeOther, qm.DomainNativeNumeric
				return typ, native, nil
			}
			return typ, native, bindFailure(origin, "缺少明确的 decimal 精度与 scale")
		}
		precision, err := strconv.Atoi(parts[1])
		if err != nil {
			return typ, native, bindFailure(origin, "decimal 精度无效")
		}
		scale := 0
		if parts[2] != "" {
			scale, err = strconv.Atoi(parts[2])
			if err != nil {
				return typ, native, bindFailure(origin, "decimal scale 无效")
			}
		}
		if precision < 1 || scale < 0 || scale > precision {
			return typ, native, bindFailure(origin, "decimal 精度超出当前语义范围")
		}
		typ = qm.LogicalType{Kind: qm.TypeDecimal, Precision: precision, Scale: scale}
		native.Precision = &precision
		native.Scale = &scale
		if precision > 38 {
			typ.Kind, native.Domain = qm.TypeOther, qm.DomainNativeNumeric
		}
	case interfaces.DataType_String:
		typ.Kind = qm.TypeString
	case interfaces.DataType_Text:
		typ.Kind = qm.TypeText
	case interfaces.DataType_Boolean:
		typ.Kind = qm.TypeBoolean
	case interfaces.DataType_Date:
		typ.Kind = qm.TypeDate
	case interfaces.DataType_Datetime, interfaces.DataType_Timestamp, interfaces.DataType_Time:
		return temporalPropertyType(property, origin)
	case interfaces.DataType_Binary:
		typ.Kind = qm.TypeBinary
	case interfaces.DataType_Json:
		typ.Kind = qm.TypeJSON
	case interfaces.DataType_Vector:
		typ.Kind = qm.TypeVector
	case interfaces.DataType_Other, interfaces.DataType_Ip, interfaces.DataType_Point, interfaces.DataType_Shape:
		typ.Kind = qm.TypeOther
	default:
		return typ, native, bindFailure(origin, "源字段类型尚未支持")
	}
	// 原生 LARGEINT 不能因公开类型为 string 被当作普通字符串参与计算。
	if strings.EqualFold(strings.TrimSpace(property.OriginalType), "LARGEINT") {
		typ = qm.LogicalType{Kind: qm.TypeOther}
		native.Domain = qm.DomainNativeNumeric
	}
	return typ, native, nil
}

func outputEncoding(typ qm.LogicalType) qm.OutputEncoding {
	kind := qm.EncodingScalar
	if typ.Kind == qm.TypeDecimal {
		kind = qm.EncodingExactNumberString
	}
	if typ.Kind == qm.TypeOther {
		kind = qm.EncodingNativeOther
	}
	public := string(typ.Kind)
	switch typ.Kind {
	case qm.TypeInteger:
		if typ.Unsigned {
			public = interfaces.DataType_UnsignedInteger
		}
	case qm.TypeLocalDateTime:
		public = interfaces.DataType_Datetime
	case qm.TypeInstant:
		public = interfaces.DataType_Timestamp
	case qm.TypeTimeOfDay:
		public = interfaces.DataType_Time
	}
	return qm.OutputEncoding{Kind: kind, PublicType: public}
}

func bindLiteral(literal *qm.Literal, origin qm.Origin) (*qm.BoundExpr, error) {
	if literal == nil {
		return nil, bindFailure(origin, "缺少常量值")
	}
	value := qm.Value{}
	switch literal.Kind {
	case qm.LiteralNull:
		value.Type.Kind = qm.TypeNull
		value.Data = qm.NullValue{}
	case qm.LiteralBoolean:
		if literal.Text != "true" && literal.Text != "false" {
			return nil, bindFailure(origin, "布尔常量无效")
		}
		value.Type.Kind = qm.TypeBoolean
		value.Data = qm.BooleanValue(literal.Text == "true")
	case qm.LiteralString:
		value.Type.Kind = qm.TypeString
		value.Data = qm.StringValue(literal.Text)
	case qm.LiteralInteger:
		if !signedNumber.MatchString(literal.Text) || strings.ContainsAny(literal.Text, ".eE") {
			return nil, bindFailure(origin, "整数常量无效")
		}
		digits := strings.TrimLeft(strings.TrimLeft(literal.Text, "+-"), "0")
		if len(digits) > 38 {
			return nil, bindFailure(origin, "整数常量超出精确数值范围")
		}
		integer, ok := new(big.Int).SetString(literal.Text, 10)
		if !ok {
			return nil, bindFailure(origin, "整数常量无效")
		}
		if integer.IsInt64() {

			value.Type = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}
			value.Data = qm.IntegerValue(integer.String())
		} else if integer.IsUint64() {
			value.Type = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}
			value.Data = qm.IntegerValue(integer.String())
		} else {
			digits := len(strings.TrimPrefix(integer.String(), "-"))
			if digits > 38 {
				return nil, bindFailure(origin, "整数常量超出精确数值范围")
			}
			value.Type = qm.LogicalType{Kind: qm.TypeDecimal, Precision: digits}
			value.Data = qm.DecimalValue(integer.String())
		}
	case qm.LiteralDecimal:
		text, precision, scale, err := decimalLiteral(literal.Text, origin)
		if err != nil {
			return nil, err
		}
		value.Type = qm.LogicalType{Kind: qm.TypeDecimal, Precision: precision, Scale: scale}
		value.Data = qm.DecimalValue(text)
	default:
		return nil, bindFailure(origin, "未知常量类型")
	}
	return &qm.BoundExpr{
		Kind:     qm.ExprLiteral,
		Type:     value.Type,
		Nullable: value.Type.Kind == qm.TypeNull,
		Literal:  &value,
		Domain:   qm.DomainVega,
		Origin:   origin,
	}, nil
}

func decimalLiteral(text string, origin qm.Origin) (string, int, int, error) {
	invalid := func() (string, int, int, error) {
		return "", 0, 0, bindFailure(origin, "decimal 常量无效或超出精确数值范围")
	}
	if !signedNumber.MatchString(text) {
		return invalid()
	}
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimLeft(text, "+-")
	exponent := 0
	if parts := strings.FieldsFunc(text, func(r rune) bool { return r == 'e' || r == 'E' }); len(parts) == 2 {
		var err error
		exponent, err = strconv.Atoi(parts[1])
		if err != nil || exponent > 38 || exponent < -38 {
			return invalid()
		}
		text = parts[0]
	}
	scale := 0
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		scale = len(text) - dot - 1
		text = text[:dot] + text[dot+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		text = "0"
	}
	scale -= exponent
	if scale < 0 {
		if len(text)-scale > 38 {
			return invalid()
		}
		text += strings.Repeat("0", -scale)
		scale = 0
	}
	precision := max(len(text), scale)
	if precision > 38 {
		return invalid()
	}
	if scale > 0 {
		if len(text) <= scale {
			text = strings.Repeat("0", scale-len(text)+1) + text
		}
		point := len(text) - scale
		text = text[:point] + "." + text[point:]
	}
	if negative {
		text = "-" + text
	}
	return text, precision, scale, nil
}

func numericType(typ qm.LogicalType) bool {
	return typ.Kind == qm.TypeInteger || typ.Kind == qm.TypeDecimal || typ.Kind == qm.TypeFloat
}
func comparableType(typ qm.LogicalType) bool {
	switch typ.Kind {
	case qm.TypeNull, qm.TypeBoolean, qm.TypeInteger, qm.TypeDecimal, qm.TypeFloat, qm.TypeString, qm.TypeText, qm.TypeDate, qm.TypeLocalDateTime, qm.TypeInstant, qm.TypeTimeOfDay, qm.TypeDuration:
		return true
	default:
		return false
	}
}
func decimalType(typ qm.LogicalType) qm.LogicalType {
	if typ.Kind == qm.TypeDecimal {
		return typ
	}
	digits := map[int]int{8: 3, 16: 5, 32: 10, 64: 19}[typ.BitWidth]
	if typ.Unsigned && typ.BitWidth == 64 {
		digits = 20
	}
	return qm.LogicalType{Kind: qm.TypeDecimal, Precision: digits}
}
func commonType(a, b qm.LogicalType, origin qm.Origin) (qm.LogicalType, error) {
	if a.Kind == qm.TypeOther || b.Kind == qm.TypeOther || a.Kind == qm.TypeVector || b.Kind == qm.TypeVector {
		return qm.LogicalType{}, bindFailure(origin, "原生投影或向量类型不支持此标量运算")
	}
	if a.Kind == qm.TypeNull {
		return b, nil
	}
	if b.Kind == qm.TypeNull {
		return a, nil
	}
	if reflect.DeepEqual(a, b) {
		return a, nil
	}
	if temporalType(a) && a.Kind == b.Kind && a.TimeZone == b.TimeZone {
		merged := a
		merged.TimePrecision = nil
		if a.TimePrecision != nil && b.TimePrecision != nil {
			precision := max(*a.TimePrecision, *b.TimePrecision)
			merged.TimePrecision = &precision
		}
		return merged, nil
	}
	if (a.Kind == qm.TypeString || a.Kind == qm.TypeText) && (b.Kind == qm.TypeString || b.Kind == qm.TypeText) {
		return qm.LogicalType{Kind: qm.TypeText}, nil
	}
	if a.Kind == qm.TypeInteger && b.Kind == qm.TypeInteger {
		if a.Unsigned == b.Unsigned {
			return qm.LogicalType{Kind: qm.TypeInteger, BitWidth: max(a.BitWidth, b.BitWidth), Unsigned: a.Unsigned}, nil
		}
		signed, unsigned := a, b
		if signed.Unsigned {
			signed, unsigned = b, a
		}
		width := max(signed.BitWidth, unsigned.BitWidth+1)
		for _, bits := range []int{8, 16, 32, 64} {
			if width <= bits {
				return qm.LogicalType{Kind: qm.TypeInteger, BitWidth: bits}, nil
			}
		}
		return qm.LogicalType{Kind: qm.TypeDecimal, Precision: 20}, nil
	}
	if numericType(a) && numericType(b) && a.Kind != qm.TypeFloat && b.Kind != qm.TypeFloat {
		a, b = decimalType(a), decimalType(b)
		scale := max(a.Scale, b.Scale)
		precision := max(a.Precision-a.Scale, b.Precision-b.Scale) + scale
		if precision <= 38 {
			return qm.LogicalType{Kind: qm.TypeDecimal, Precision: precision, Scale: scale}, nil
		}
	}
	return qm.LogicalType{}, bindFailure(origin, "表达式类型不兼容，需要明确的 CAST")
}
func implicitCast(expr *qm.BoundExpr, typ qm.LogicalType) *qm.BoundExpr {
	if reflect.DeepEqual(expr.Type, typ) {
		return expr
	}
	return &qm.BoundExpr{
		Kind:     qm.ExprCast,
		Type:     typ,
		Nullable: expr.Nullable,
		Args:     []*qm.BoundExpr{expr},
		Domain:   expr.Domain,
		Origin:   expr.Origin,
	}
}

func arithmeticType(op qm.ScalarOperator, a, b qm.LogicalType, origin qm.Origin) (qm.LogicalType, error) {
	if a.Kind == qm.TypeNull {
		a = b
	}
	if b.Kind == qm.TypeNull {
		b = a
	}
	if a.Kind == qm.TypeNull {
		return a, nil
	}
	if !numericType(a) || !numericType(b) {
		return qm.LogicalType{}, bindFailure(origin, "算术运算只接受数值")
	}
	common, err := commonType(a, b, origin)
	if err != nil {
		return qm.LogicalType{}, err
	}
	if common.Kind != qm.TypeDecimal && op != qm.OperatorDivide {
		return common, nil
	}
	if common.Kind == qm.TypeFloat {
		return common, nil
	}
	a, b = decimalType(a), decimalType(b)
	p, s := common.Precision, common.Scale
	switch op {
	case qm.OperatorAdd, qm.OperatorSubtract:
		s = max(a.Scale, b.Scale)
		p = max(a.Precision-a.Scale, b.Precision-b.Scale) + s + 1
	case qm.OperatorMultiply:
		p = a.Precision + b.Precision
		s = a.Scale + b.Scale
	case qm.OperatorDivide:
		s = max(6, a.Scale+b.Precision+1)
		p = a.Precision - a.Scale + b.Scale + s
	case qm.OperatorModulo:
		s = max(a.Scale, b.Scale)
		p = min(a.Precision-a.Scale, b.Precision-b.Scale) + s
	}
	if p > 38 || p < 1 || s > p {
		return qm.LogicalType{}, bindFailure(origin, "计算结果超出 decimal 精度范围，需要明确缩窄 CAST")
	}
	return qm.LogicalType{Kind: qm.TypeDecimal, Precision: p, Scale: s}, nil
}
