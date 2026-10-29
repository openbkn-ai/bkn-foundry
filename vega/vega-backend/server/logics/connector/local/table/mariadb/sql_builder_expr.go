// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

func columnSql(c sb.Column) (string, []any, error) {
	name, err := quoteIdentifier(c.Name)
	if err != nil {
		return "", nil, err
	}
	if c.Qualifier != "" {
		qualifier, err := quoteIdentifier(c.Qualifier)
		if err != nil {
			return "", nil, err
		}
		name = qualifier + "." + name
	}
	return name, nil, nil
}

func starSql(s sb.Star) (string, []any, error) {
	if s.Qualifier == "" {
		return "*", nil, nil
	}
	qualifier, err := quoteIdentifier(s.Qualifier)
	if err != nil {
		return "", nil, err
	}
	return qualifier + ".*", nil, nil
}

func aliasSql(a sb.Alias) (string, []any, error) {
	name := "``"
	if a.Name != "" {
		var err error
		name, err = quoteIdentifier(a.Name)
		if err != nil {
			return "", nil, err
		}
	}
	b := sqlBuffer{}
	b.writePart(a.Expr)
	b.writeString(" AS " + name)
	return b.result()
}

func notSql(n sb.Not) (string, []any, error) {
	b := sqlBuffer{}
	b.writeString("(NOT ")
	b.writePart(n.Expr)
	b.writeByte(')')
	return b.result()
}

func isNullSql(n sb.IsNull) (string, []any, error) {
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(n.Expr)
	if n.Negated {
		b.writeString(" IS NOT NULL)")
	} else {
		b.writeString(" IS NULL)")
	}
	return b.result()
}

func inSql(in sb.In) (string, []any, error) {
	if len(in.Values) == 0 {
		return "", nil, fmt.Errorf("IN list must not be empty")
	}
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(in.Expr)
	if in.Negated {
		b.writeString(" NOT IN (")
	} else {
		b.writeString(" IN (")
	}
	b.writeParts(in.Values, ", ")
	b.writeString("))")
	return b.result()
}

func betweenSql(between sb.Between) (string, []any, error) {
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(between.Expr)
	if between.Negated {
		b.writeString(" NOT BETWEEN ")
	} else {
		b.writeString(" BETWEEN ")
	}
	b.writePart(between.Lower)
	b.writeString(" AND ")
	b.writePart(between.Upper)
	b.writeByte(')')
	return b.result()
}

func likeSql(like sb.Like) (string, []any, error) {
	if like.Escape != nil && (!utf8.ValidString(*like.Escape) || utf8.RuneCountInString(*like.Escape) != 1) {
		return "", nil, fmt.Errorf("LIKE escape must be a single valid character")
	}
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(like.Expr)
	if like.Negated {
		b.writeString(" NOT LIKE ")
	} else {
		b.writeString(" LIKE ")
	}
	b.writePart(like.Pattern)
	if like.Escape != nil {
		b.writeString(" ESCAPE ")
		b.writePart(&sb.Param{Value: *like.Escape})
	}
	b.writeByte(')')
	return b.result()
}

func containsSql(contains sb.Contains) (string, []any, error) {
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(contains.Expr)
	// 固定用 ! 转义，先处理 ! 再处理通配符；不依赖反斜线字符串解析模式。
	// 在 SQL 中转义 Value，因此常量和列引用等动态表达式使用同一语义。
	b.writeString(" LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(")
	b.writePart(contains.Value)
	b.writeString(", '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')")
	return b.result()
}

func arithmeticSql(e sb.Arithmetic) (string, []any, error) {
	switch e.Op {
	case sb.Add, sb.Subtract, sb.Multiply, sb.Divide, sb.Modulo:
		return binarySql(e.Left, string(e.Op), e.Right)
	default:
		return "", nil, fmt.Errorf("invalid arithmetic operator")
	}
}

func unarySql(e sb.Unary) (string, []any, error) {
	if e.Op != sb.Positive && e.Op != sb.Negative {
		return "", nil, fmt.Errorf("invalid unary operator")
	}
	b := sqlBuffer{}
	b.writeString("(" + string(e.Op))
	b.writePart(e.Expr)
	b.writeByte(')')
	return b.result()
}

func functionSql(f sb.Function) (string, []any, error) {
	// 这里只检查名称形状，不在 SQL 构造器中重复维护函数签名及业务白名单。
	for i, c := range f.Name {
		valid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9')
		if !valid {
			return "", nil, fmt.Errorf("invalid function name")
		}
	}
	if f.Name == "" || (f.Distinct && len(f.Args) == 0) {
		return "", nil, fmt.Errorf("function name or DISTINCT arguments are empty")
	}
	name := strings.ToUpper(f.Name)
	// 时间精度属于语法参数，MySQL 不接受 NOW(?)；只输出已验证的 0..6 整数。
	if (name == "NOW" || name == "CURRENT_TIMESTAMP") && len(f.Args) != 0 {
		if len(f.Args) != 1 || f.Distinct {
			return "", nil, fmt.Errorf("invalid temporal function precision")
		}
		param, ok := f.Args[0].(*sb.Param)
		if !ok || param == nil {
			return "", nil, fmt.Errorf("temporal function precision must be an integer literal")
		}
		number, ok := param.Value.(json.Number)
		if !ok {
			return "", nil, fmt.Errorf("temporal function precision must be an integer literal")
		}
		precision, err := strconv.ParseUint(string(number), 10, 8)
		if err != nil || precision > 6 {
			return "", nil, fmt.Errorf("temporal function precision is out of range")
		}
		return name + "(" + strconv.FormatUint(precision, 10) + ")", nil, nil
	}
	b := sqlBuffer{}
	b.writeString(name + "(")
	if f.Distinct {
		b.writeString("DISTINCT ")
	}
	b.writeParts(f.Args, ", ")
	b.writeByte(')')
	return b.result()
}

func caseSql(c sb.Case) (string, []any, error) {
	if len(c.WhenParts) == 0 {
		return "", nil, fmt.Errorf("CASE requires a WHEN branch")
	}
	b := sqlBuffer{}
	b.writeString("CASE")
	if c.Operand != nil {
		b.writeByte(' ')
		b.writePart(c.Operand)
	}
	for _, when := range c.WhenParts {
		b.writeString(" WHEN ")
		b.writePart(when.Condition)
		b.writeString(" THEN ")
		b.writePart(when.Result)
	}
	if c.Else != nil {
		b.writeString(" ELSE ")
		b.writePart(c.Else)
	}
	b.writeString(" END")
	return b.result()
}

func castSql(c sb.Cast) (string, []any, error) {
	var target string
	switch c.Type {
	case sb.CastInteger:
		target = "SIGNED"
	case sb.CastUnsignedInteger:
		target = "UNSIGNED"
	case sb.CastDecimal:
		target = "DECIMAL"
	case sb.CastString:
		target = "CHAR"
	case sb.CastBinary:
		target = "BINARY"
	case sb.CastDate:
		target = "DATE"
	case sb.CastDateTime:
		target = "DATETIME"
	case sb.CastTime:
		target = "TIME"
	case sb.CastFloat:
		target = "FLOAT"
	case sb.CastDouble:
		target = "DOUBLE"
	case sb.CastJSON:
		target = "JSON"
	default:
		return "", nil, fmt.Errorf("logical cast type has no MySQL mapping")
	}
	parameters := c.Parameters
	maxParameters := 0
	switch target {
	case "SIGNED", "UNSIGNED", "DATE", "JSON", "DOUBLE":
	case "CHAR", "BINARY":
		maxParameters = 1
	case "FLOAT":
		maxParameters = 1
		if len(parameters) > 0 && parameters[0] > 53 {
			return "", nil, fmt.Errorf("FLOAT precision is out of range")
		}
	case "TIME", "DATETIME":
		maxParameters = 1
		if len(parameters) > 0 && parameters[0] > 6 {
			return "", nil, fmt.Errorf("temporal precision is out of range")
		}
	case "DECIMAL":
		maxParameters = 2
		if len(parameters) > 0 && (parameters[0] == 0 || parameters[0] > 65) {
			return "", nil, fmt.Errorf("DECIMAL precision is out of range")
		}
		if len(parameters) > 1 && (parameters[1] > 30 || parameters[1] > parameters[0]) {
			return "", nil, fmt.Errorf("DECIMAL scale is out of range")
		}
	default:
		return "", nil, fmt.Errorf("CAST target type is not supported")
	}
	if len(parameters) > maxParameters {
		return "", nil, fmt.Errorf("invalid CAST type parameter count")
	}
	if len(parameters) > 0 {
		parts := make([]string, len(parameters))
		for i, parameter := range parameters {
			parts[i] = strconv.FormatUint(parameter, 10)
		}
		target += "(" + strings.Join(parts, ",") + ")"
	}
	b := sqlBuffer{}
	b.writeString("CAST(")
	b.writePart(c.Expr)
	b.writeString(" AS " + target + ")")
	return b.result()
}

func binarySql(left sb.SqlNode, operator string, right sb.SqlNode) (string, []any, error) {
	b := sqlBuffer{}
	b.writeByte('(')
	b.writePart(left)
	b.writeString(" " + operator + " ")
	b.writePart(right)
	b.writeByte(')')
	return b.result()
}

func conditionsSql(conditions []sb.SqlNode, separator, empty string) (string, []any, error) {
	if len(conditions) == 0 {
		return empty, nil, nil
	}
	b := sqlBuffer{}
	b.writeByte('(')
	b.writeParts(conditions, separator)
	b.writeByte(')')
	return b.result()
}

// numericLiteralSQL 为精确小数及超出整数范围的字面量生成显式 DECIMAL 类型。
func numericLiteralSQL(number json.Number) (string, []any, error) {
	text := string(number)
	if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return "", nil, fmt.Errorf("invalid numeric literal")
	}
	if strings.ContainsAny(text, "eE") {
		return "?", []any{number}, nil
	}
	_, signedErr := strconv.ParseInt(text, 10, 64)
	_, unsignedErr := strconv.ParseUint(text, 10, 64)
	if !strings.Contains(text, ".") && (signedErr == nil || unsignedErr == nil) {
		return "?", []any{number}, nil
	}
	digits := strings.TrimPrefix(text, "-")
	precision, scale := len(digits), 0
	if dot := strings.IndexByte(digits, '.'); dot >= 0 {
		precision--
		scale = len(digits) - dot - 1
	}
	if precision < 1 || precision > 65 || scale > 30 {
		return "", nil, fmt.Errorf("numeric literal exceeds the supported DECIMAL range")
	}
	return "CAST(? AS DECIMAL(" + strconv.Itoa(precision) + "," + strconv.Itoa(scale) + "))", []any{number}, nil
}
