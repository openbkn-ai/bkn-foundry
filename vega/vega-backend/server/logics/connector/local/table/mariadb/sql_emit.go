// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// 每一层使用内部字段 ID 命名，避免公开别名、同名 JOIN 列和物理列名相互覆盖。
type sqlRelation struct {
	text   string
	args   []any
	schema qm.Schema
	order  []sqlOrder
}
type sqlOrder struct {
	name      string
	direction qm.SortDirection
	nulls     qm.NullOrder
}
type sqlEmitter struct {
	ctx            context.Context
	target         qm.TargetBinding
	nodes          int
	sequence       int
	parameterBytes int
}

func (e *sqlEmitter) relation(root qm.BoundRelation, depth int) (result sqlRelation, resultErr error) {
	defer func() {
		if len(result.text) > qm.MaxSQLBytes {
			result = sqlRelation{}
			resultErr = sqlUnsupported("目标 SQL 超出生成预算")
		}
	}()
	if err := e.ctx.Err(); err != nil {
		return sqlRelation{}, err
	}
	e.nodes++
	if root == nil || depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return sqlRelation{}, sqlUnsupported("查询关系超出生成预算")
	}
	switch r := root.(type) {
	case *qm.BoundScan:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少来源关系")
		}
		return e.scan(r)
	case *qm.BoundJoin:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少连接关系")
		}
		left, err := e.relation(r.Left, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		right, err := e.relation(r.Right, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		scope := sqlScope(left.schema, "l")
		for id, name := range sqlScope(right.schema, "r") {
			if scope[id] != "" {
				return sqlRelation{}, sqlUnsupported("来源字段身份重复")
			}
			scope[id] = name
		}
		kind := ""
		switch r.Kind {
		case qm.JoinInner:
			kind = "INNER JOIN"
		case qm.JoinLeft:
			kind = "LEFT JOIN"
		case qm.JoinRight:
			kind = "RIGHT JOIN"
		case qm.JoinCross:
			kind = "CROSS JOIN"
		default:
			return sqlRelation{}, sqlUnsupported("连接类型不受支持")
		}
		if r.Kind == qm.JoinCross && r.On != nil {
			return sqlRelation{}, sqlUnsupported("交叉连接不接受条件")
		}
		on := ""
		var args []any
		if r.Kind != qm.JoinCross {
			value, values, err := e.expr(r.On, scope, depth+1)
			if err != nil {
				return sqlRelation{}, err
			}
			on = " ON " + value
			args = values
		}
		columns, err := sqlColumns(r.OutputSchema, scope)
		if err != nil {
			return sqlRelation{}, err
		}
		return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + left.text + ") AS l " + kind + " (" + right.text + ") AS r" + on, args: append(append(left.args, right.args...), args...), schema: r.OutputSchema}, nil
	case *qm.BoundFilter:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少过滤关系")
		}
		input, err := e.relation(r.Input, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		predicate, args, err := e.expr(r.Predicate, sqlScope(input.schema, "q"), depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		columns, err := sqlColumns(r.OutputSchema, sqlScope(input.schema, "q"))
		if err != nil {
			return sqlRelation{}, err
		}
		columns = append(columns, sqlOrderColumns(input.order, "q")...)
		return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + input.text + ") AS q WHERE " + predicate + sqlOrderBy(input.order, "q"), args: append(input.args, args...), schema: r.OutputSchema, order: input.order}, nil
	case *qm.BoundProject:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少投影关系")
		}
		input, err := e.relation(r.Input, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		var columns []string
		var args []any
		for _, column := range r.Columns {
			value, values, err := e.expr(column.Expr, sqlScope(input.schema, "q"), depth+1)
			if err != nil {
				return sqlRelation{}, err
			}
			columns = append(columns, value+" AS "+sqlField(column.Output))
			args = append(args, values...)
		}
		if len(columns) != len(r.OutputSchema) {
			return sqlRelation{}, sqlUnsupported("投影与输出不一致")
		}
		columns = append(columns, sqlOrderColumns(input.order, "q")...)
		return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + input.text + ") AS q" + sqlOrderBy(input.order, "q"), args: append(args, input.args...), schema: r.OutputSchema, order: input.order}, nil
	case *qm.BoundSort:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少排序关系")
		}
		input, err := e.relation(r.Input, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		columns, err := sqlColumns(r.OutputSchema, sqlScope(input.schema, "q"))
		if err != nil {
			return sqlRelation{}, err
		}
		var order []sqlOrder
		var args []any
		for _, key := range r.Keys {
			value, values, err := e.comparisonExpr(key.Expr, sqlScope(input.schema, "q"), depth+1)
			if err != nil {
				return sqlRelation{}, err
			}
			if (key.Direction != qm.SortAscending && key.Direction != qm.SortDescending) || (key.Nulls != qm.NullsFirst && key.Nulls != qm.NullsLast) {
				return sqlRelation{}, sqlUnsupported("排序规则无效")
			}
			e.sequence++
			name := fmt.Sprintf("_sort%d", e.sequence)
			columns = append(columns, value+" AS `"+name+"`")
			args = append(args, values...)
			order = append(order, sqlOrder{name: name, direction: key.Direction, nulls: key.Nulls})
		}
		// 排序别名在本层 SELECT 中定义，ORDER BY 不加输入关系前缀。
		return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + input.text + ") AS q" + sqlOrderBy(order, ""), args: append(args, input.args...), schema: r.OutputSchema, order: order}, nil
	case *qm.BoundLimit:
		if r == nil || r.Offset < 0 || r.Count < 0 {
			return sqlRelation{}, sqlUnsupported("逻辑窗口无效")
		}
		input, err := e.relation(r.Input, depth+1)
		if err != nil {
			return sqlRelation{}, err
		}
		columns, err := sqlColumns(r.OutputSchema, sqlScope(input.schema, "q"))
		if err != nil {
			return sqlRelation{}, err
		}
		columns = append(columns, sqlOrderColumns(input.order, "q")...)
		return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + input.text + ") AS q" + sqlOrderBy(input.order, "q") + " LIMIT ? OFFSET ?", args: append(input.args, r.Count, r.Offset), schema: r.OutputSchema, order: input.order}, nil
	case *qm.BoundSubquery:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少派生关系")
		}
		return e.remap(r.Input, r.OutputSchema, r.Columns, depth)
	case *qm.BoundViewBoundary:
		if r == nil {
			return sqlRelation{}, sqlUnsupported("缺少视图关系")
		}
		return e.remap(r.Input, r.OutputSchema, r.Columns, depth)
	default:
		return sqlRelation{}, sqlUnsupported("关系算子尚不能等价下推")
	}
}

func (e *sqlEmitter) remap(root qm.BoundRelation, schema qm.Schema, mappings []qm.ColumnMapping, depth int) (sqlRelation, error) {
	input, err := e.relation(root, depth+1)
	if err != nil {
		return sqlRelation{}, err
	}
	scope := sqlScope(input.schema, "q")
	var columns []string
	for _, mapping := range mappings {
		if scope[mapping.Input] == "" {
			return sqlRelation{}, sqlUnsupported("派生字段映射不完整")
		}
		columns = append(columns, scope[mapping.Input]+" AS "+sqlField(mapping.Output))
	}
	if len(columns) != len(schema) {
		return sqlRelation{}, sqlUnsupported("派生输出不一致")
	}
	return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM (" + input.text + ") AS q", args: input.args, schema: schema}, nil
}

func (e *sqlEmitter) scan(scan *qm.BoundScan) (sqlRelation, error) {
	if scan.Resource.CatalogID != e.target.Target.CatalogID {
		return sqlRelation{}, sqlUnsupported("来源与目标目录不一致")
	}
	var identifier qm.PhysicalIdentifier
	for _, relation := range e.target.Relations {
		if relation.Instance == scan.Instance && relation.ResourceID == scan.Resource.ID {
			if identifier != nil {
				return sqlRelation{}, sqlUnsupported("来源映射重复")
			}
			identifier = relation.Identifier
		}
	}
	table, err := sqlIdentifier(identifier)
	if err != nil {
		return sqlRelation{}, err
	}
	var columns []string
	for _, column := range scan.OutputSchema {
		var mapping *qm.FieldBinding
		for i := range e.target.Fields {
			field := &e.target.Fields[i]
			if field.Address.Relation == scan.Instance && field.Address.Field == column.ID {
				if mapping != nil {
					return sqlRelation{}, sqlUnsupported("字段映射重复")
				}
				mapping = field
			}
		}
		if mapping == nil || mapping.MissingValue == qm.MissingDistinct {
			return sqlRelation{}, sqlUnsupported("字段映射不完整")
		}
		matched := false
		for _, field := range scan.Fields {
			if field.ID == column.ID && reflect.DeepEqual(field.NativeType, mapping.NativeType) && field.NativeType.ConnectorType == e.target.Target.ConnectorType {
				matched = true
				break
			}
		}
		if !matched {
			return sqlRelation{}, sqlUnsupported("原生字段类型与目标映射不一致")
		}
		name, err := sqlIdentifier(mapping.Identifier)
		if err != nil {
			return sqlRelation{}, err
		}
		columns = append(columns, name+" AS "+sqlField(column.ID))
	}
	if len(columns) == 0 {
		return sqlRelation{}, sqlUnsupported("来源输出为空")
	}
	return sqlRelation{text: "SELECT " + strings.Join(columns, ", ") + " FROM " + table, schema: scan.OutputSchema}, nil
}

func (e *sqlEmitter) expr(expr *qm.BoundExpr, scope map[qm.FieldID]string, depth int) (string, []any, error) {
	if err := e.ctx.Err(); err != nil {
		return "", nil, err
	}
	e.nodes++
	if expr == nil || depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return "", nil, sqlUnsupported("表达式超出生成预算")
	}
	if expr.Domain != qm.DomainVega && expr.Domain != "" || len(expr.Requirements) != 0 {
		return "", nil, sqlUnsupported("表达式语义尚不能兑现")
	}
	switch expr.Kind {
	case qm.ExprField:
		if scope[expr.Field] == "" {
			return "", nil, sqlUnsupported("字段不在当前输入作用域")
		}
		return scope[expr.Field], nil, nil
	case qm.ExprLiteral:
		if expr.Literal != nil {
			if data, ok := expr.Literal.Data.(qm.StringValue); ok {
				e.parameterBytes += len(data)
				if e.parameterBytes > qm.MaxSQLBytes {
					return "", nil, sqlUnsupported("参数字节数超出生成预算")
				}
			}
		}
		value, err := sqlLiteral(expr.Literal)
		return "?", []any{value}, err
	case qm.ExprUnary:
		if expr.Operator != qm.OperatorNot || len(expr.Args) != 1 {
			return "", nil, sqlUnsupported("一元运算尚不支持")
		}
		value, args, err := e.expr(expr.Args[0], scope, depth+1)
		return "(NOT " + value + ")", args, err
	case qm.ExprBinary:
		if len(expr.Args) != 2 {
			return "", nil, sqlUnsupported("二元表达式结构无效")
		}
		switch expr.Operator {
		case qm.OperatorAnd, qm.OperatorOr, qm.OperatorEqual, qm.OperatorNotEqual, qm.OperatorLess, qm.OperatorLessEqual, qm.OperatorGreater, qm.OperatorGreaterEqual:
		default:
			return "", nil, sqlUnsupported("数值运算尚无精确溢出保证")
		}
		left, a, err := e.comparisonExpr(expr.Args[0], scope, depth+1)
		if err != nil {
			return "", nil, err
		}
		right, b, err := e.comparisonExpr(expr.Args[1], scope, depth+1)
		if err != nil {
			return "", nil, err
		}
		return "(" + left + " " + string(expr.Operator) + " " + right + ")", append(a, b...), nil
	case qm.ExprCast:
		if len(expr.Args) == 1 && sqlScalarType(expr.Type) {
			arg := expr.Args[0]
			if arg.Type.Kind == qm.TypeNull || ((arg.Type.Kind == qm.TypeString || arg.Type.Kind == qm.TypeText) && (expr.Type.Kind == qm.TypeString || expr.Type.Kind == qm.TypeText)) {
				return e.expr(arg, scope, depth+1)
			}
		}
		return "", nil, sqlUnsupported("类型转换尚不能等价下推")
	case qm.ExprIsNull:
		if len(expr.Args) != 1 {
			return "", nil, sqlUnsupported("NULL 判断结构无效")
		}
		value, args, err := e.expr(expr.Args[0], scope, depth+1)
		op := " IS NULL"
		if expr.Negated {
			op = " IS NOT NULL"
		}
		return "(" + value + op + ")", args, err
	case qm.ExprIn, qm.ExprBetween:
		if (expr.Kind == qm.ExprIn && (len(expr.Args) < 2 || len(expr.Args) > qm.MaxINValues+1)) || (expr.Kind == qm.ExprBetween && len(expr.Args) != 3) {
			return "", nil, sqlUnsupported("集合或范围结构无效")
		}
		var values []string
		var args []any
		for _, arg := range expr.Args {
			value, a, err := e.comparisonExpr(arg, scope, depth+1)
			if err != nil {
				return "", nil, err
			}
			values = append(values, value)
			args = append(args, a...)
		}
		op := ""
		if expr.Negated {
			op = " NOT"
		}
		if expr.Kind == qm.ExprIn {
			return "(" + values[0] + op + " IN (" + strings.Join(values[1:], ", ") + "))", args, nil
		}
		return "(" + values[0] + op + " BETWEEN " + values[1] + " AND " + values[2] + ")", args, nil
	default:
		return "", nil, sqlUnsupported("表达式尚不能等价下推")
	}
}

func (e *sqlEmitter) comparisonExpr(expr *qm.BoundExpr, scope map[qm.FieldID]string, depth int) (string, []any, error) {
	value, args, err := e.expr(expr, scope, depth)
	if err != nil {
		return "", nil, err
	}
	if !sqlScalarType(expr.Type) {
		return "", nil, sqlUnsupported("比较类型尚不能等价下推")
	}
	// 显式使用二进制字符串，避免源 collation 忽略大小写或尾空格。
	if expr.Type.Kind == qm.TypeString || expr.Type.Kind == qm.TypeText {
		value = "CAST(CONVERT(" + value + " USING utf8mb4) AS BINARY)"
	}
	return value, args, nil
}
func sqlScalarType(typ qm.LogicalType) bool {
	switch typ.Kind {
	case qm.TypeInteger:
		return typ.BitWidth == 64
	case qm.TypeNull, qm.TypeBoolean, qm.TypeString, qm.TypeText:
		return true
	default:
		return false
	}
}
func sqlLiteral(value *qm.Value) (any, error) {
	if value == nil {
		return nil, sqlUnsupported("缺少绑定常量")
	}
	switch data := value.Data.(type) {
	case qm.NullValue:
		if value.Type.Kind == qm.TypeNull {
			return sql.NullInt64{}, nil
		}
	case qm.BooleanValue:
		if value.Type.Kind == qm.TypeBoolean {
			return bool(data), nil
		}
	case qm.StringValue:
		if value.Type.Kind == qm.TypeString || value.Type.Kind == qm.TypeText {
			return string(data), nil
		}
	case qm.IntegerValue:
		if value.Type.Kind == qm.TypeInteger && value.Type.BitWidth == 64 {
			if value.Type.Unsigned {
				v, err := strconv.ParseUint(string(data), 10, 64)
				if err != nil {
					return nil, sqlUnsupported("无符号整数绑定值无效")
				}
				return v, nil
			}
			v, err := strconv.ParseInt(string(data), 10, 64)
			if err != nil {
				return nil, sqlUnsupported("整数绑定值无效")
			}
			return v, nil
		}
	}
	return nil, sqlUnsupported("绑定值尚不能精确传递")
}
func sqlScope(schema qm.Schema, alias string) map[qm.FieldID]string {
	scope := make(map[qm.FieldID]string, len(schema))
	for _, column := range schema {
		scope[column.ID] = alias + "." + sqlField(column.ID)
	}
	return scope
}
func sqlColumns(schema qm.Schema, scope map[qm.FieldID]string) ([]string, error) {
	columns := make([]string, 0, len(schema))
	for _, column := range schema {
		if column.ID == 0 || scope[column.ID] == "" {
			return nil, sqlUnsupported("输出字段不在输入作用域")
		}
		columns = append(columns, scope[column.ID]+" AS "+sqlField(column.ID))
	}
	return columns, nil
}
func sqlField(id qm.FieldID) string { return fmt.Sprintf("`f%d`", id) }
func sqlIdentifier(parts qm.PhysicalIdentifier) (string, error) {
	if len(parts) == 0 {
		return "", sqlUnsupported("缺少原生标识符")
	}
	quoted := make([]string, len(parts))
	for i, part := range parts {
		if part == "" || !utf8.ValidString(part) || strings.ContainsRune(part, '\x00') {
			return "", sqlUnsupported("原生标识符无效")
		}
		quoted[i] = "`" + strings.ReplaceAll(part, "`", "``") + "`"
	}
	return strings.Join(quoted, "."), nil
}
func sqlOrderColumns(order []sqlOrder, alias string) []string {
	columns := make([]string, 0, len(order))
	for _, key := range order {
		columns = append(columns, alias+".`"+key.name+"` AS `"+key.name+"`")
	}
	return columns
}
func sqlOrderBy(order []sqlOrder, alias string) string {
	if len(order) == 0 {
		return ""
	}
	var keys []string
	for _, key := range order {
		name := "`" + key.name + "`"
		if alias != "" {
			name = alias + "." + name
		}
		nullDirection := "DESC"
		if key.nulls == qm.NullsLast {
			nullDirection = "ASC"
		}
		keys = append(keys, "("+name+" IS NULL) "+nullDirection, name+" "+strings.ToUpper(string(key.direction)))
	}
	return " ORDER BY " + strings.Join(keys, ", ")
}
func sqlUnsupported(reason string) error {
	return interfaces.NewUnsupportedOperationError("sql_prepare", fmt.Errorf("%s", reason))
}
