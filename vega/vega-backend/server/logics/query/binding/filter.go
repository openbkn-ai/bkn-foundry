// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type filterBuilder struct{ nodes int }

// FilterExpression 将外部或保存的条件构造为 IR，不读取字段元数据、不修改输入。
// nil/空对象表示 TRUE；已删除的 real_value、编译条件和未知字段不进入此边界。
func FilterExpression(raw any, origin qm.Origin) (*qm.Expr, error) {
	if raw == nil {
		return boolean(true, origin), nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, invalid(origin, "过滤条件无法解码")
	}
	if len(encoded) > qm.MaxSQLBytes {
		return nil, &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "过滤条件字节数超限"}
	}
	nodes := 0
	if err := validateFilterJSON(encoded, origin, 1, &nodes); err != nil {
		return nil, err
	}
	var input interfaces.FilterCondCfg
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, invalid(origin, "过滤条件包含未知字段或无效结构")
	}
	builder := filterBuilder{}
	return builder.expression(&input, origin, 1)
}

func (b *filterBuilder) consume(n int, origin qm.Origin) error {
	b.nodes += n
	if b.nodes > qm.MaxNodes {
		return &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "过滤条件节点数量超限"}
	}
	return nil
}

func invalid(origin qm.Origin, message string) error {
	return &qm.CompileError{Kind: qm.ErrorInvalidTree, Origin: origin, Message: message}
}
func path(origin qm.Origin, suffix string) qm.Origin { origin.Path += suffix; return origin }
func field(name string, origin qm.Origin) *qm.Expr {
	return &qm.Expr{Kind: qm.ExprName, Name: []string{name}, Origin: origin}
}
func boolean(value bool, origin qm.Origin) *qm.Expr {
	text := "false"
	if value {
		text = "true"
	}
	return &qm.Expr{Kind: qm.ExprLiteral, Literal: &qm.Literal{Kind: qm.LiteralBoolean, Text: text}, Origin: origin}
}
func binary(op string, left, right *qm.Expr, origin qm.Origin) *qm.Expr {
	return &qm.Expr{Kind: qm.ExprBinary, Op: op, Args: []*qm.Expr{left, right}, Origin: origin}
}

// validateFilterJSON 在解码共享条件结构前检查公开字段及深度，避免接受内部状态。
func validateFilterJSON(encoded json.RawMessage, origin qm.Origin, depth int, nodes *int) error {
	*nodes += 1
	if depth > qm.MaxDepth || *nodes > qm.MaxNodes {
		return &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "过滤条件复杂度超限"}
	}
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return invalid(origin, "过滤条件必须为对象")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return invalid(origin, "过滤条件结构无效")
	}
	if _, hasValue := fields["value"]; hasValue {
		var operation string
		if err := json.Unmarshal(fields["operation"], &operation); err != nil || operation == "" {
			return invalid(origin, "过滤条件缺少算子")
		}
	}
	for key, value := range fields {
		switch key {
		case "field", "operation", "value_from", "value":
		case "sub_conditions":
			var children []json.RawMessage
			if err := json.Unmarshal(value, &children); err != nil {
				return invalid(origin, "子条件必须为数组")
			}
			for i, child := range children {
				if err := validateFilterJSON(child, path(origin, fmt.Sprintf(".sub_conditions[%d]", i)), depth+1, nodes); err != nil {
					return err
				}
			}
		default:
			return invalid(origin, "过滤条件包含未知字段")
		}
	}
	return nil
}

func (b *filterBuilder) expression(input *interfaces.FilterCondCfg, origin qm.Origin, depth int) (*qm.Expr, error) {
	b.nodes++
	if depth > qm.MaxDepth || b.nodes > qm.MaxNodes {
		return nil, &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "过滤条件复杂度超限"}
	}
	if input.Operation == "" && input.Name == "" && input.ValueFrom == "" && input.Value == nil && len(input.SubConds) == 0 {
		return boolean(true, origin), nil
	}
	if input.Operation == "and" || input.Operation == "or" {
		if input.Name != "" || (input.ValueFrom != "" && input.ValueFrom != interfaces.ValueFrom_Const) || input.Value != nil || len(input.SubConds) == 0 || len(input.SubConds) > interfaces.MaxSubCondition {
			return nil, invalid(origin, "逻辑条件结构无效")
		}
		if err := b.consume(max(0, len(input.SubConds)-2), origin); err != nil {
			return nil, err
		}
		var result *qm.Expr
		op := strings.ToUpper(input.Operation)
		for i, child := range input.SubConds {
			expr, err := b.expression(child, path(origin, fmt.Sprintf(".sub_conditions[%d]", i)), depth+1)
			if err != nil {
				return nil, err
			}
			if result == nil {
				result = expr
			} else {
				result = binary(op, result, expr, origin)
			}
		}
		return result, nil
	}
	if input.Name == "" || len(input.SubConds) > 0 {
		return nil, invalid(origin, "条件字段或子条件结构无效")
	}
	if err := b.consume(1, origin); err != nil {
		return nil, err
	}
	lhs := field(input.Name, path(origin, ".field"))
	if input.Operation == "null" || input.Operation == "not_null" || input.Operation == "true" || input.Operation == "false" {
		if input.Value != nil || (input.ValueFrom != "" && input.ValueFrom != interfaces.ValueFrom_Const) {
			return nil, invalid(origin, "此条件不接受 value 或 value_from")
		}
		if input.Operation == "true" || input.Operation == "false" {
			if err := b.consume(1, origin); err != nil {
				return nil, err
			}
			return binary("=", lhs, boolean(input.Operation == "true", origin), origin), nil
		}
		return &qm.Expr{Kind: qm.ExprIsNull, Args: []*qm.Expr{lhs}, Negated: input.Operation == "not_null", Origin: origin}, nil
	}
	if input.Value == nil {
		return nil, invalid(path(origin, ".value"), "条件缺少值")
	}
	valueFrom := input.ValueFrom
	if valueFrom == "" {
		valueFrom = interfaces.ValueFrom_Const
	}
	value := input.Value
	comparison := map[string]string{"==": "=", "eq": "=", "!=": "<>", "not_eq": "<>", ">": ">", "gt": ">", ">=": ">=", "gte": ">=", "<": "<", "lt": "<", "<=": "<=", "lte": "<="}
	if op, ok := comparison[input.Operation]; ok {
		if err := b.consume(1, origin); err != nil {
			return nil, err
		}
		var rhs *qm.Expr
		var err error
		switch valueFrom {
		case interfaces.ValueFrom_Field:
			name, ok := value.(string)
			if !ok || name == "" {
				return nil, invalid(origin, "字段比较必须引用字段名称")
			}
			rhs = field(name, path(origin, ".value"))
		case interfaces.ValueFrom_Const:
			rhs, err = literal(value, path(origin, ".value"))
		default:
			return nil, invalid(origin, "此条件值来源尚未支持")
		}
		if err != nil {
			return nil, err
		}
		return binary(op, lhs, rhs, origin), nil
	}
	if valueFrom != interfaces.ValueFrom_Const {
		return nil, invalid(origin, "此算子只接受常量值")
	}
	switch input.Operation {
	case "in", "not_in", "range", "out_range":
		values, ok := value.([]any)
		if !ok || len(values) == 0 || len(values) > qm.MaxINValues {
			return nil, invalid(origin, "条件必须为非空且有界的常量列表")
		}
		if (input.Operation == "range" || input.Operation == "out_range") && len(values) != 2 {
			return nil, invalid(origin, "范围条件必须有两个边界")
		}
		if err := b.consume(len(values), origin); err != nil {
			return nil, err
		}
		args := []*qm.Expr{lhs}
		for i, item := range values {
			if item == nil {
				return nil, invalid(origin, "集合及范围条件不接受 NULL")
			}
			expr, err := literal(item, path(origin, fmt.Sprintf(".value[%d]", i)))
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
		if input.Operation == "range" || input.Operation == "out_range" {
			return &qm.Expr{Kind: qm.ExprBetween, Args: args, Negated: input.Operation == "out_range", Origin: origin}, nil
		}
		return &qm.Expr{Kind: qm.ExprIn, Args: args, Negated: input.Operation == "not_in", Origin: origin}, nil
	case "like", "not_like":
		if err := b.consume(1, origin); err != nil {
			return nil, err
		}
		text, ok := value.(string)
		if !ok {
			return nil, invalid(origin, "字面子串条件只接受字符串")
		}
		rhs, _ := literal(text, path(origin, ".value"))
		return &qm.Expr{Kind: qm.ExprLiteralContains, Args: []*qm.Expr{lhs, rhs}, Negated: input.Operation == "not_like", Origin: origin}, nil
	default:
		return nil, invalid(origin, "此过滤算子尚未接入 IR")
	}
}

func literal(value any, origin qm.Origin) (*qm.Expr, error) {
	input := &qm.Literal{}
	switch v := value.(type) {
	case string:
		input.Kind = qm.LiteralString
		input.Text = v
	case bool:
		input.Kind = qm.LiteralBoolean
		input.Text = "false"
		if v {
			input.Text = "true"
		}
	case json.Number:
		input.Kind = qm.LiteralInteger
		input.Text = v.String()
		if strings.ContainsAny(input.Text, ".eE") {
			input.Kind = qm.LiteralDecimal
		}
	default:
		return nil, invalid(origin, "条件值必须为标量")
	}
	return &qm.Expr{Kind: qm.ExprLiteral, Literal: input, Origin: origin}, nil
}
