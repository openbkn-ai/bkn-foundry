// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/functions"
	"reflect"
	"strings"
	"unicode/utf8"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func (b *queryBinder) expr(e *qm.Expr, scope *bindingScope, allowAggregate bool) (*qm.BoundExpr, error) {
	if err := b.enter(e, qm.Origin{}); err != nil {
		return nil, err
	}
	defer b.leave(e)
	groups := scope.groups
	groupInput := scope.groupInput
	if scope.fallback != nil && !referencesLocalName(e, scope, make(map[*qm.Expr]bool)) {
		groups = scope.fallback.groups
		groupInput = scope.fallback.groupInput
	}
	for _, group := range groups {
		if sameGroupingExpr(e, group.source, groupInput) {
			return columnExpr(group.column, e.Origin), nil
		}
	}
	result := &qm.BoundExpr{Origin: e.Origin, Domain: qm.DomainVega, Distinct: e.Distinct, Negated: e.Negated, CaseBase: e.CaseBase, CaseElse: e.CaseElse}
	switch e.Kind {
	case qm.ExprName:
		if len(e.Args) != 0 {
			return nil, bindFailure(e.Origin, "字段引用结构非法")
		}
		column, err := lookupColumn(scope, e.Name, e.Origin)
		if err != nil {
			return nil, err
		}
		return columnExpr(column, e.Origin), nil
	case qm.ExprLiteral:
		if len(e.Args) != 0 {
			return nil, bindFailure(e.Origin, "常量结构非法")
		}
		return bindLiteral(e.Literal, e.Origin)
	case qm.ExprAggregateRef:
		if len(e.Args) != 0 || e.AggregateSlot < 0 || e.AggregateSlot >= len(scope.aggregates) {
			return nil, bindFailure(e.Origin, "聚合引用不在当前查询块")
		}
		copy := *scope.aggregates[e.AggregateSlot]
		copy.Origin = e.Origin
		return &copy, nil
	case qm.ExprWildcard:
		return nil, bindFailure(e.Origin, "当前位置不接受通配符")
	case qm.ExprCall:
		return b.function(e, scope, allowAggregate)
	}
	// 有符号数值常量先精确折叠，避免最小 int64 经 uint64 中间类型丢失符号。
	if e.Kind == qm.ExprUnary && len(e.Args) == 1 && (e.Op == "+" || e.Op == "-") && e.Args[0] != nil && e.Args[0].Kind == qm.ExprLiteral && e.Args[0].Literal != nil && len(e.Args[0].Args) == 0 && (e.Args[0].Literal.Kind == qm.LiteralInteger || e.Args[0].Literal.Kind == qm.LiteralDecimal) {
		value := *e.Args[0].Literal
		if e.Op == "-" {
			if strings.HasPrefix(value.Text, "-") {
				value.Text = strings.TrimPrefix(value.Text, "-")
			} else {
				value.Text = "-" + strings.TrimPrefix(value.Text, "+")
			}
		}
		return bindLiteral(&value, e.Origin)
	}
	for _, arg := range e.Args {
		bound, err := b.expr(arg, scope, false)
		if err != nil {
			return nil, err
		}
		result.Args = append(result.Args, bound)
	}
	arity := len(result.Args)
	requireArity := func(n int) error {
		if arity != n {
			return bindFailure(e.Origin, "表达式参数数量无效")
		}
		return nil
	}
	switch e.Kind {
	case qm.ExprUnary:
		if err := requireArity(1); err != nil {
			return nil, err
		}
		arg := result.Args[0]
		result.Kind = qm.BoundUnary
		result.Operator = qm.ScalarOperator(e.Op)
		result.Nullable = arg.Nullable
		switch e.Op {
		case "NOT":
			if arg.Type.Kind != qm.TypeBoolean && arg.Type.Kind != qm.TypeNull {
				return nil, bindFailure(e.Origin, "NOT 只接受布尔表达式")
			}
			result.Type.Kind = qm.TypeBoolean
		case "+", "-":
			if !numericType(arg.Type) && arg.Type.Kind != qm.TypeNull {
				return nil, bindFailure(e.Origin, "正负号只接受数值")
			}
			result.Type = arg.Type
			if e.Op == "-" && arg.Type.Kind == qm.TypeInteger && arg.Type.Unsigned {
				signed := arg.Type
				signed.Unsigned = false
				typ, err := commonType(signed, arg.Type, e.Origin)
				if err != nil {
					return nil, err
				}
				result.Args[0] = implicitCast(arg, typ)
				result.Type = typ
			}
		default:
			return nil, bindFailure(e.Origin, "未知一元运算符")
		}
	case qm.ExprBinary:
		if err := requireArity(2); err != nil {
			return nil, err
		}
		result.Kind = qm.BoundBinary
		op := e.Op
		if op == "!=" {
			op = "<>"
		}
		result.Operator = qm.ScalarOperator(op)
		result.Nullable = result.Args[0].Nullable || result.Args[1].Nullable
		switch result.Operator {
		case qm.OperatorAnd, qm.OperatorOr:
			for _, arg := range result.Args {
				if arg.Type.Kind != qm.TypeBoolean && arg.Type.Kind != qm.TypeNull {
					return nil, bindFailure(e.Origin, "逻辑运算只接受布尔表达式")
				}
			}
			result.Type.Kind = qm.TypeBoolean
		case qm.OperatorEqual, qm.OperatorNotEqual, qm.OperatorLess, qm.OperatorLessEqual, qm.OperatorGreater, qm.OperatorGreaterEqual:
			if err := coerceArguments(result, true); err != nil {
				return nil, err
			}
			result.Type.Kind = qm.TypeBoolean
		case qm.OperatorAdd, qm.OperatorSubtract, qm.OperatorMultiply, qm.OperatorDivide, qm.OperatorModulo:
			if err := coerceArithmetic(result); err != nil {
				return nil, err
			}
			typ, err := arithmeticType(result.Operator, result.Args[0].Type, result.Args[1].Type, e.Origin)
			if err != nil {
				return nil, err
			}
			result.Type = typ
			result.Nullable = result.Nullable || result.Operator == qm.OperatorDivide || result.Operator == qm.OperatorModulo
		default:
			return nil, bindFailure(e.Origin, "未知二元运算符")
		}
	case qm.ExprIsNull:
		if err := requireArity(1); err != nil {
			return nil, err
		}
		result.Kind = qm.BoundIsNull
		result.Type.Kind = qm.TypeBoolean
	case qm.ExprBetween:
		if err := requireArity(3); err != nil {
			return nil, err
		}
		result.Kind = qm.BoundBetween
		if err := coerceArguments(result, true); err != nil {
			return nil, err
		}
		result.Type.Kind = qm.TypeBoolean
	case qm.ExprIn:
		if arity < 2 || arity-1 > qm.MaxINValues {
			return nil, bindFailure(e.Origin, "IN 常量列表数量无效")
		}
		for _, arg := range e.Args[1:] {
			if !constantIR(arg) {
				return nil, bindFailure(e.Origin, "IN 只接受常量列表")
			}
		}
		result.Kind = qm.BoundIn
		if err := coerceArguments(result, true); err != nil {
			return nil, err
		}
		result.Type.Kind = qm.TypeBoolean
	case qm.ExprLike:
		if err := requireArity(2); err != nil {
			return nil, err
		}
		if e.Escape == nil || utf8.RuneCountInString(*e.Escape) != 1 {
			return nil, bindFailure(e.Origin, "LIKE 转义符无效")
		}
		for _, arg := range result.Args {
			if arg.Type.Kind != qm.TypeString && arg.Type.Kind != qm.TypeText && arg.Type.Kind != qm.TypeNull {
				return nil, bindFailure(e.Origin, "LIKE 只接受字符串")
			}
		}
		escape := *e.Escape
		result.Escape = &escape
		result.Kind = qm.BoundLike
		result.Type.Kind = qm.TypeBoolean
		for _, arg := range result.Args {
			result.Nullable = result.Nullable || arg.Nullable
		}
	case qm.ExprCase:
		return bindCase(result, e)
	case qm.ExprCast:
		if err := requireArity(1); err != nil {
			return nil, err
		}
		typ, err := castType(e)
		if err != nil {
			return nil, err
		}
		source := result.Args[0].Type
		if !supportedCast(source, typ) {
			return nil, bindFailure(e.Origin, "当前类型转换尚未支持")
		}
		result.Kind = qm.BoundCast
		result.Type = typ
		result.Nullable = result.Args[0].Nullable
		result.Requirements = []qm.SemanticRequirement{{Name: "invalid_cast", Value: "error"}, {Name: "decimal_rounding", Value: "half_away_from_zero"}}
	default:
		return nil, bindFailure(e.Origin, "未知表达式节点")
	}
	return result, nil
}

func singleName(name []string) string {
	if len(name) != 1 {
		return ""
	}
	return name[0]
}
func constantIR(e *qm.Expr) bool {
	if e == nil {
		return false
	}
	if e.Kind == qm.ExprLiteral {
		return true
	}
	return e.Kind == qm.ExprUnary && (e.Op == "+" || e.Op == "-") && len(e.Args) == 1 && e.Args[0] != nil && e.Args[0].Kind == qm.ExprLiteral && e.Args[0].Literal != nil && (e.Args[0].Literal.Kind == qm.LiteralInteger || e.Args[0].Literal.Kind == qm.LiteralDecimal)
}
func coerceArguments(expr *qm.BoundExpr, comparison bool) error {
	typ := qm.LogicalType{Kind: qm.TypeNull}
	for _, arg := range expr.Args {
		var err error
		typ, err = commonType(typ, arg.Type, expr.Origin)
		if err != nil {
			return err
		}
		expr.Nullable = expr.Nullable || arg.Nullable
	}
	if comparison && !comparableType(typ) {
		return bindFailure(expr.Origin, "比较类型不支持")
	}
	for i, arg := range expr.Args {
		expr.Args[i] = implicitCast(arg, typ)
	}
	return nil
}

func (b *queryBinder) function(e *qm.Expr, scope *bindingScope, allowAggregate bool) (*qm.BoundExpr, error) {
	signature, ok := functions.Lookup(singleName(e.Name))
	if !ok {
		return nil, bindFailure(e.Origin, "函数未开放")
	}
	count := len(e.Args)
	if count < signature.MinArgs || (signature.MaxArgs > 0 && count > signature.MaxArgs) || (signature.MaxArgs == 0 && signature.MinArgs == 0 && count != 0) || (e.Distinct && !signature.AllowDistinct) {
		return nil, bindFailure(e.Origin, "函数参数或 DISTINCT 无效")
	}
	if signature.Evaluation == qm.EvaluationAggregate && !allowAggregate {
		return nil, bindFailure(e.Origin, "聚合位置或嵌套非法")
	}
	if signature.Evaluation == qm.EvaluationStable {
		precision := 6
		typ := qm.LogicalType{Kind: qm.TypeInstant, TimePrecision: &precision, TimeZone: qm.TimeZoneInstant}
		var slot qm.RuntimeSlotID
		if len(b.slots) > 0 {
			slot = b.slots[0].ID
		} else {
			slot = 1
			b.slots = append(b.slots, qm.RuntimeSlot{ID: slot, Type: typ, Evaluation: qm.EvaluationStable, Function: "CURRENT_TIMESTAMP", Source: qm.RuntimeQueryStart, Origin: e.Origin})
		}
		return &qm.BoundExpr{Kind: qm.BoundRuntimeSlot, RuntimeSlot: slot, Type: typ, Domain: qm.DomainVega, Origin: e.Origin}, nil
	}
	result := &qm.BoundExpr{Kind: qm.BoundCall, Function: qm.FunctionID(signature.Name), Distinct: e.Distinct, Origin: e.Origin, Domain: qm.DomainVega}
	for _, arg := range e.Args {
		if arg != nil && arg.Kind == qm.ExprWildcard {
			if !signature.AllowStar || e.Distinct || len(arg.Name) != 0 || len(arg.Args) != 0 {
				return nil, bindFailure(e.Origin, "函数不接受当前通配符")
			}
			result.Function = "COUNT_STAR"
			continue
		}
		bound, err := b.expr(arg, scope, false)
		if err != nil {
			return nil, err
		}
		result.Args = append(result.Args, bound)
	}
	switch result.Function {
	case "COUNT", "COUNT_STAR":
		result.Type = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}
		if e.Distinct && !comparableType(result.Args[0].Type) {
			return nil, bindFailure(e.Origin, "COUNT DISTINCT 类型不支持")
		}
	case "MIN", "MAX":
		arg := result.Args[0]
		if !comparableType(arg.Type) {
			return nil, bindFailure(e.Origin, "MIN/MAX 类型不支持")
		}
		result.Type = arg.Type
		result.Nullable = true
	case "SUM", "AVG":
		arg := result.Args[0]
		if !numericType(arg.Type) {
			return nil, bindFailure(e.Origin, "数值聚合只接受明确数值类型")
		}
		result.Type = arg.Type
		if arg.Type.Kind != qm.TypeFloat {
			typ := decimalType(arg.Type)
			scale := typ.Scale
			if result.Function == "AVG" {
				scale = max(6, scale)
			}
			result.Type = qm.LogicalType{Kind: qm.TypeDecimal, Precision: 38, Scale: scale}
		}
		result.Nullable = true
	case "ABS":
		arg := result.Args[0]
		if !numericType(arg.Type) && arg.Type.Kind != qm.TypeNull {
			return nil, bindFailure(e.Origin, "ABS 只接受数值")
		}
		result.Type = arg.Type
		result.Nullable = arg.Nullable
	case "COALESCE", "IFNULL":
		if err := coerceArguments(result, false); err != nil {
			return nil, err
		}
		result.Type = result.Args[0].Type
		result.Nullable = true
		for _, arg := range result.Args {
			result.Nullable = result.Nullable && arg.Nullable
		}
	case "NULLIF":
		typ := result.Args[0].Type
		if err := coerceArguments(result, true); err != nil {
			return nil, err
		}
		result.Type = typ
		result.Nullable = true
	default:
		return nil, bindFailure(e.Origin, "函数签名尚未绑定")
	}
	return result, nil
}

func bindCase(result *qm.BoundExpr, e *qm.Expr) (*qm.BoundExpr, error) {
	end := len(result.Args)
	if e.CaseElse {
		end--
	}
	start := 0
	if e.CaseBase {
		start = 1
	}
	if end-start < 2 || (end-start)%2 != 0 {
		return nil, bindFailure(e.Origin, "CASE 结构无效")
	}
	var args []*qm.BoundExpr
	var branches []*qm.BoundExpr
	for i := start; i < end; i += 2 {
		when := result.Args[i]
		if e.CaseBase {
			when = &qm.BoundExpr{Kind: qm.BoundBinary, Operator: qm.OperatorEqual, Args: []*qm.BoundExpr{result.Args[0], when}, Origin: e.Origin, Domain: qm.DomainVega}
			if err := coerceArguments(when, true); err != nil {
				return nil, err
			}
			when.Type.Kind = qm.TypeBoolean
		} else if when.Type.Kind != qm.TypeBoolean && when.Type.Kind != qm.TypeNull {
			return nil, bindFailure(e.Origin, "CASE WHEN 必须为布尔表达式")
		}
		args = append(args, when, result.Args[i+1])
		branches = append(branches, result.Args[i+1])
	}
	fallback, err := bindLiteral(&qm.Literal{Kind: qm.LiteralNull}, e.Origin)
	if err != nil {
		return nil, err
	}
	if e.CaseElse {
		fallback = result.Args[len(result.Args)-1]
	}
	branches = append(branches, fallback)
	merged := &qm.BoundExpr{Args: branches, Origin: e.Origin}
	if err := coerceArguments(merged, false); err != nil {
		return nil, err
	}
	for i := range args {
		if i%2 == 1 {
			args[i] = merged.Args[i/2]
		}
	}
	args = append(args, merged.Args[len(merged.Args)-1])
	result.Args = args
	result.Type = merged.Args[0].Type
	result.Nullable = merged.Nullable
	result.Kind = qm.BoundCase
	result.CaseBase = false
	result.CaseElse = true
	return result, nil
}

func castType(e *qm.Expr) (qm.LogicalType, error) {
	typ := qm.LogicalType{}
	switch e.CastType {
	case "SIGNED", "INTEGER", "INT", "BIGINT":
		typ = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}
	case "UNSIGNED":
		typ = qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}
	case "FLOAT", "DOUBLE":
		typ = qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}
	case "CHAR", "VARCHAR", "STRING":
		typ.Kind = qm.TypeString
	case "BOOLEAN":
		typ.Kind = qm.TypeBoolean
	case "DECIMAL":
		if len(e.CastParameters) < 1 || len(e.CastParameters) > 2 {
			return typ, bindFailure(e.Origin, "DECIMAL CAST 需要明确 precision 和可选 scale")
		}
		typ.Kind = qm.TypeDecimal
		typ.Precision = e.CastParameters[0]
		if len(e.CastParameters) == 2 {
			typ.Scale = e.CastParameters[1]
		}
		if typ.Precision < 1 || typ.Precision > 38 || typ.Scale < 0 || typ.Scale > typ.Precision {
			return typ, bindFailure(e.Origin, "CAST decimal 精度无效")
		}
		return typ, nil
	default:
		return typ, bindFailure(e.Origin, "此 CAST 类型尚未接入")
	}
	if len(e.CastParameters) != 0 {
		return typ, bindFailure(e.Origin, "此 CAST 不接受类型参数")
	}
	return typ, nil
}
func supportedCast(from, to qm.LogicalType) bool {
	if from.Kind == qm.TypeNull || reflect.DeepEqual(from, to) {
		return true
	}
	if numericType(from) && numericType(to) {
		return true
	}
	if (from.Kind == qm.TypeString || from.Kind == qm.TypeText) && (numericType(to) || to.Kind == qm.TypeString) {
		return true
	}
	return (numericType(from) || from.Kind == qm.TypeBoolean) && to.Kind == qm.TypeString
}

// 排序输出别名优先于输入分组表达式；循环由 expr 的预算检查拒绝。
func referencesLocalName(e *qm.Expr, scope *bindingScope, seen map[*qm.Expr]bool) bool {
	pending := []*qm.Expr{e}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node == nil || seen[node] || len(seen) >= qm.MaxNodes {
			return true
		}
		seen[node] = true
		if node.Kind == qm.ExprName {
			for _, c := range scope.columns {
				if len(node.Name) == 1 && node.Name[0] == c.column.Name {
					return true
				}
			}
		}
		if len(pending)+len(node.Args) > qm.MaxNodes {
			return true
		}
		pending = append(pending, node.Args...)
	}
	return false
}

func coerceArithmetic(e *qm.BoundExpr) error {
	common, err := commonType(e.Args[0].Type, e.Args[1].Type, e.Origin)
	if err != nil {
		return err
	}
	for i, arg := range e.Args {
		typ := common
		if common.Kind != qm.TypeNull && common.Kind != qm.TypeFloat && (common.Kind == qm.TypeDecimal || e.Operator == qm.OperatorDivide) {
			input := arg.Type
			if input.Kind == qm.TypeNull {
				input = common
			}
			typ = decimalType(input)
			if e.Operator == qm.OperatorDivide && arg.Kind == qm.BoundLiteral && arg.Literal != nil && arg.Type.Kind == qm.TypeInteger {
				digits := len(strings.TrimPrefix(string(arg.Literal.Data.(qm.IntegerValue)), "-"))
				typ = qm.LogicalType{Kind: qm.TypeDecimal, Precision: digits}
			}
		}
		e.Args[i] = implicitCast(arg, typ)
	}
	return nil
}
