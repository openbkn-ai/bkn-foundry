// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

const sqlBatchRows = 128

var _ interfaces.SQLQueryConnector = (*MariaDBConnector)(nil)

// sqlRenderResult 仅保存本次 SQL 生成结果；字段定义直接取自 Bound IR。
type sqlRenderResult struct {
	text  string
	args  []any
	order []sqlSortColumn
}

type sqlSortColumn struct {
	name      string
	direction qm.SortDirection
	nulls     qm.NullOrder
}

// mysqlSQLRenderer 使用内部字段 ID 生成别名，并维护本次生成的预算和排序槽。
type mysqlSQLRenderer struct {
	connector      string
	catalogID      string
	mysql          bool
	ctx            context.Context
	nodes          int
	sequence       int
	parameterBytes int
}

// 流按列位置解码，不依赖源返回的列名。正常 EOF 与失败/提前关闭严格区分。
type sqlResultStream struct {
	rows       *sql.Rows
	schema     qm.Schema
	offset     int64
	limit      *int64
	read       int64
	done       bool
	closed     bool
	failure    error
	completion qm.SQLReadCompletion
}

// Prepare 只校验标准查询对象和目标能力，不生成 SQL、不保存参数、不创建连接。
func (c *MariaDBConnector) Prepare(ctx context.Context, req *qm.SelectBuilder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if req == nil || len(req.ColumnsPart) == 0 {
		return sqlUnsupported("缺少完整绑定查询")
	}
	if c.config != nil {
		if charset, ok := c.config.Options["charset"]; ok && charset != "utf8mb4" {
			return sqlUnsupported("当前 SQL 链路要求 utf8mb4 连接字符集")
		}
		if collation, ok := c.config.Options["collation"]; ok {
			name, valid := collation.(string)
			if !valid || !strings.HasPrefix(name, "utf8mb4_") {
				return sqlUnsupported("当前 SQL 链路要求 utf8mb4 连接字符集")
			}
		}
	}
	renderer := mysqlSQLRenderer{connector: c.GetType(), ctx: ctx, mysql: c.GetType() == interfaces.ConnectorTypeMySQL}
	if err := renderer.checkSelect(req, 1); err != nil {
		return err
	}
	for _, column := range req.Schema() {
		if !renderer.scalarType(column.Type) {
			return sqlUnsupported("输出类型尚无精确解码支持")
		}
	}
	return nil
}

// Execute 消费标准 Bound IR 查询对象；每次读取独立生成 SQL 和参数，不修改基础 IR。
// 调用层仍负责在每次执行前复核当前权限、资源状态及依赖版本。
func (c *MariaDBConnector) Execute(ctx context.Context, query *qm.SelectBuilder, read qm.SQLReadRequest) (interfaces.SQLResultStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil {
		return nil, sqlUnsupported("缺少查询对象")
	}
	text, args, schema, err := c.ToSQL(ctx, query, read)
	if err != nil {
		return nil, err
	}
	stream := &sqlResultStream{schema: schema, offset: read.Offset}
	if read.Limit != nil {
		stream.limit = new(*read.Limit)
	}
	if read.Kind == qm.SQLReadCount {
		stream.limit = nil
	}
	if read.Limit != nil && *read.Limit == 0 {
		stream.completion = qm.SQLReadCompletion{Kind: qm.CompletionWindowEnd, NextOffset: new(read.Offset)}
		stream.done = true
		return stream, nil
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("SQL 查询连接失败: %w", mariadbQueryError(err))
	}
	rows, err := c.db.QueryContext(ctx, text, args...) //nolint:rowserrcheck // Rows 的错误和正常 EOF 由 sqlResultStream.Next 检查。
	if err != nil {
		return nil, fmt.Errorf("SQL 查询执行失败: %w", mariadbQueryError(err))
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = rows.Close()
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("SQL 查询列读取失败: %w", err)
	}
	if len(columns) != len(schema) {
		return nil, sqlUnsupported("源结果列数与输出 Schema 不一致")
	}
	stream.rows = rows
	transferred = true
	return stream, nil
}

// ToSQL 仅在读取时遍历 IR 并绑定字面量；临时 SQL 结果不进入查询对象。
func (c *MariaDBConnector) ToSQL(ctx context.Context, query *qm.SelectBuilder, read qm.SQLReadRequest) (string, []any, qm.Schema, error) {
	if err := c.Prepare(ctx, query); err != nil {
		return "", nil, nil, err
	}

	if read.Offset < 0 || (read.Limit != nil && (*read.Limit < 0 || *read.Limit == math.MaxInt64 || read.Offset > math.MaxInt64-*read.Limit)) {
		return "", nil, nil, sqlUnsupported("读取窗口无效")
	}
	renderer := mysqlSQLRenderer{connector: c.GetType(), ctx: ctx, mysql: c.GetType() == interfaces.ConnectorTypeMySQL}
	relation, err := renderer.selectQuery(query, 1, read.Kind == qm.SQLReadPage || read.Limit != nil || read.Offset != 0)
	if err != nil {
		return "", nil, nil, err
	}
	args := relation.args
	if read.Kind == qm.SQLReadCount {
		if read.Offset != 0 || read.Limit != nil {
			return "", nil, nil, sqlUnsupported("count 不接受读取窗口")
		}
		schema := qm.Schema{{ID: 1, Name: "count", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, Encoding: qm.OutputEncoding{Kind: qm.EncodingScalar, PublicType: interfaces.DataType_Integer}}}
		text := "SELECT COUNT(*) AS `f1` FROM (" + relation.text + ") AS q"
		if len(text) > qm.MaxSQLBytes {
			return "", nil, nil, sqlUnsupported("目标 SQL 超出生成预算")
		}
		return text, args, schema, nil
	}
	if read.Kind != qm.SQLReadPage && read.Kind != qm.SQLReadRelation {
		return "", nil, nil, sqlUnsupported("读取类型无效")
	}
	if read.Kind == qm.SQLReadRelation && read.Limit == nil && read.Offset == 0 && len(relation.order) == 0 {
		return relation.text, relation.args, query.Schema(), nil
	}
	schema := query.Schema()
	columns, err := sqlColumns(schema, sqlScope(schema, "q"))
	if err != nil {
		return "", nil, nil, err
	}
	text := "SELECT " + strings.Join(columns, ", ") + " FROM (" + relation.text + ") AS q" + sqlOrderBy(relation.order, "q")
	if read.Limit != nil {
		text += " LIMIT ? OFFSET ?"
		args = append(args, *read.Limit+1, read.Offset)
	} else if read.Offset != 0 {
		text += " LIMIT 18446744073709551615 OFFSET ?"
		args = append(args, read.Offset)
	}
	if len(text) > qm.MaxSQLBytes {
		return "", nil, nil, sqlUnsupported("目标 SQL 超出生成预算")
	}
	return text, args, schema, nil
}

func cloneSQLSchema(schema qm.Schema) qm.Schema {
	schema = append(qm.Schema(nil), schema...)
	for i := range schema {
		column := &schema[i]
		column.Lineage = append([]qm.FieldID(nil), column.Lineage...)
		if column.Type.TimePrecision != nil {
			column.Type.TimePrecision = new(*column.Type.TimePrecision)
		}
	}
	return schema
}

// checkSelect 仅检查查询子句和表达式能力，不生成 SQL。
func (e *mysqlSQLRenderer) checkSelect(query *qm.SelectBuilder, depth int) error {
	if query == nil || depth > qm.MaxDepth {
		return sqlUnsupported("查询为空或超出准备预算")
	}
	e.nodes++
	if e.nodes > qm.MaxNodes {
		return sqlUnsupported("查询超出准备预算")
	}
	p := query
	if len(p.ColumnsPart) == 0 {
		return sqlUnsupported("查询缺少输出投影")
	}
	if (p.DistinctPart || len(p.GroupByPart) > 0) && !e.mysql {
		return sqlUnsupported("去重或分组尚不能等价下推")
	}
	if p.LimitPart != nil && (p.LimitPart.Count < 0 || p.LimitPart.Offset < 0) {
		return sqlUnsupported("逻辑窗口无效")
	}
	if err := e.checkSource(p.FromPart, depth+1); err != nil {
		return err
	}
	outputs := make(map[qm.FieldID]bool, len(p.ColumnsPart))
	for _, c := range p.ColumnsPart {
		if c.Output == 0 || outputs[c.Output] || c.Alias == "" {
			return sqlUnsupported("投影输出身份或名称无效")
		}
		outputs[c.Output] = true
		if err := e.checkExpr(c.Expr, depth+1, e.mysql); err != nil {
			return err
		}
	}
	for i, list := range [][]*qm.Sqlizer{p.WherePart, p.GroupByPart, p.HavingPart} {
		for _, expr := range list {
			if err := e.checkExpr(expr, depth+1, i == 2 && e.mysql); err != nil {
				return err
			}
		}
	}
	for _, key := range p.OrderByPart {
		if key.Nulls != qm.NullsFirst && key.Nulls != qm.NullsLast {
			return sqlUnsupported("排序规则无效")
		}
		if err := e.checkExpr(key.Expr, depth+1, e.mysql); err != nil {
			return err
		}
	}
	return nil
}
func (e *mysqlSQLRenderer) checkSource(source qm.Relation, depth int) error {
	if source == nil || reflect.ValueOf(source).IsNil() || depth > qm.MaxDepth {
		return sqlUnsupported("来源为空或超出准备预算")
	}
	e.nodes++
	if e.nodes > qm.MaxNodes {
		return sqlUnsupported("来源超出准备预算")
	}
	switch r := source.(type) {
	case *qm.Scan:
		_, _, err := e.scanMappings(r)
		return err
	case *qm.Join:
		if r.Kind != qm.JoinInner && r.Kind != qm.JoinLeft && r.Kind != qm.JoinRight && r.Kind != qm.JoinCross {
			return sqlUnsupported("连接类型不受支持")
		}
		if err := e.checkSource(r.Left, depth+1); err != nil {
			return err
		}
		if err := e.checkSource(r.Right, depth+1); err != nil {
			return err
		}
		if r.Kind == qm.JoinCross {
			if r.On != nil {
				return sqlUnsupported("交叉连接不接受条件")
			}
			return nil
		}
		return e.checkExpr(r.On, depth+1, false)
	case *qm.Subquery:
		return e.checkSelect(r.Query, depth+1)
	default:
		return sqlUnsupported("来源类型不受支持")
	}
}
func (e *mysqlSQLRenderer) checkExpr(expr *qm.Sqlizer, depth int, aggregate bool) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	e.nodes++
	if expr == nil || depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return sqlUnsupported("表达式超出准备预算")
	}
	if (expr.Domain != "" && expr.Domain != qm.DomainVega) || len(expr.Requirements) != 0 {
		return sqlUnsupported("表达式语义不受支持")
	}
	arity := len(expr.Args)
	switch expr.Kind {
	case qm.ExprField:
		if expr.Field == 0 || arity != 0 {
			return sqlUnsupported("字段节点无效")
		}
	case qm.ExprLiteral:
		if expr.Value == nil || arity != 0 {
			return sqlUnsupported("常量节点无效")
		}
		if e.mysql && expr.Type.Kind == qm.TypeDecimal {
			value, ok := expr.Value.Data.(qm.DecimalValue)
			if !ok {
				return sqlUnsupported("decimal 值无效")
			}
			if _, err := mysqlDecimal(string(value), expr.Type); err != nil {
				return err
			}
		} else {
			if _, err := sqlLiteral(expr.Value); err != nil {
				return err
			}
		}
		if value, ok := expr.Value.Data.(qm.StringValue); ok {
			e.parameterBytes += len(value)
			if e.parameterBytes > qm.MaxSQLBytes {
				return sqlUnsupported("参数字节数超出预算")
			}
		}
	case qm.ExprUnary:
		if arity != 1 || expr.Op != qm.OperatorNot {
			return sqlUnsupported("一元运算不受支持")
		}
	case qm.ExprBinary:
		if arity != 2 {
			return sqlUnsupported("二元表达式无效")
		}
		switch expr.Op {
		case qm.OperatorAnd, qm.OperatorOr, qm.OperatorEqual, qm.OperatorNotEqual, qm.OperatorLess, qm.OperatorLessEqual, qm.OperatorGreater, qm.OperatorGreaterEqual:
		default:
			return sqlUnsupported("二元运算不受支持")
		}
		for _, arg := range expr.Args {
			if arg == nil || !e.scalarType(arg.Type) {
				return sqlUnsupported("比较类型不受支持")
			}
		}
	case qm.ExprIsNull:
		if arity != 1 {
			return sqlUnsupported("NULL 判断无效")
		}
	case qm.ExprCast:
		if arity != 1 || expr.Args[0] == nil || !e.scalarType(expr.Type) {
			return sqlUnsupported("类型转换无效")
		}
		if !e.canCast(expr.Args[0].Type, expr.Type) {
			return sqlUnsupported("类型转换不受支持")
		}

	case qm.ExprIn, qm.ExprBetween:
		if (expr.Kind == qm.ExprIn && (arity < 2 || arity > qm.MaxINValues+1)) || (expr.Kind == qm.ExprBetween && arity != 3) {
			return sqlUnsupported("集合或范围无效")
		}
		for _, arg := range expr.Args {
			if arg == nil || !e.scalarType(arg.Type) {
				return sqlUnsupported("集合或范围类型不受支持")
			}
		}
	case qm.ExprCall:
		if !aggregate {
			return sqlUnsupported("标量函数不受支持")
		}
		if expr.Function == "COUNT_STAR" {
			if arity != 0 || expr.Distinct {
				return sqlUnsupported("COUNT 参数无效")
			}
			break
		}
		if arity != 1 || expr.Args[0] == nil || !e.scalarType(expr.Args[0].Type) {
			return sqlUnsupported("聚合参数无效")
		}
		if expr.Function != "COUNT" && expr.Function != "MIN" && expr.Function != "MAX" {
			return sqlUnsupported("聚合函数不受支持")
		}
		if expr.Distinct && expr.Function != "COUNT" {
			return sqlUnsupported("聚合去重不受支持")
		}
	default:
		return sqlUnsupported("表达式不受支持")
	}
	for _, arg := range expr.Args {
		if err := e.checkExpr(arg, depth+1, aggregate && expr.Kind != qm.ExprCall); err != nil {
			return err
		}
	}
	return nil
}

// selectQuery 按 SELECT 子句顺序生成 SQL，只有显式子查询才递归生成 SELECT。
func (e *mysqlSQLRenderer) selectQuery(query *qm.SelectBuilder, depth int, carryOrder bool) (result sqlRenderResult, resultErr error) {
	defer func() {
		if len(result.text) > qm.MaxSQLBytes {
			result = sqlRenderResult{}
			resultErr = sqlUnsupported("目标 SQL 超出生成预算")
		}
	}()
	if err := e.ctx.Err(); err != nil {
		return result, err
	}
	e.nodes++
	if query == nil || depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return result, sqlUnsupported("查询超出生成预算")
	}
	p := query
	from, fromArgs, scope, err := e.sourceSQL(p.FromPart, depth+1)
	if err != nil {
		return result, err
	}
	var columns []string
	for _, column := range p.ColumnsPart {
		var value string
		var args []any
		if p.DistinctPart || len(p.GroupByPart) > 0 {
			value, args, err = e.comparisonExpr(column.Expr, scope, depth+1)
		} else {
			value, args, err = e.expr(column.Expr, scope, depth+1)
		}
		if err != nil {
			return result, err
		}
		columns = append(columns, value+" AS "+sqlField(column.Output))
		result.args = append(result.args, args...)
	}
	// 仅 API 外层读取需要携带私有排序列；普通查询直接生成 ORDER BY。
	var directOrder []string
	var orderArgs []any
	for _, key := range p.OrderByPart {
		value, args, err := e.comparisonExpr(key.Expr, scope, depth+1)
		if err != nil {
			return result, err
		}
		direction := qm.SortAscending
		if key.Descending {
			direction = qm.SortDescending
		}
		if carryOrder {
			e.sequence++
			name := fmt.Sprintf("_sort%d", e.sequence)
			columns = append(columns, value+" AS `"+name+"`")
			result.args = append(result.args, args...)
			result.order = append(result.order, sqlSortColumn{name: name, direction: direction, nulls: key.Nulls})
		} else {
			nulls := "ASC"
			if key.Nulls == qm.NullsFirst {
				nulls = "DESC"
			}
			directOrder = append(directOrder, "("+value+" IS NULL) "+nulls)
			orderArgs = append(orderArgs, args...)
			value, args, err = e.comparisonExpr(key.Expr, scope, depth+1)
			if err != nil {
				return result, err
			}
			directOrder = append(directOrder, value+" "+strings.ToUpper(string(direction)))
			orderArgs = append(orderArgs, args...)
		}
	}
	prefix := "SELECT "
	if p.DistinctPart {
		prefix += "DISTINCT "
	}
	result.text = prefix + strings.Join(columns, ", ") + " FROM " + from
	result.args = append(result.args, fromArgs...)
	for _, clause := range []struct {
		name        string
		expressions []*qm.Sqlizer
		separator   string
		compare     bool
	}{{" WHERE ", p.WherePart, " AND ", false}, {" GROUP BY ", p.GroupByPart, ", ", true}, {" HAVING ", p.HavingPart, " AND ", false}} {
		if len(clause.expressions) == 0 {
			continue
		}
		var values []string
		for _, expr := range clause.expressions {
			var value string
			var args []any
			if clause.compare {
				value, args, err = e.comparisonExpr(expr, scope, depth+1)
			} else {
				value, args, err = e.expr(expr, scope, depth+1)
			}
			if err != nil {
				return result, err
			}
			values = append(values, value)
			result.args = append(result.args, args...)
		}
		result.text += clause.name + strings.Join(values, clause.separator)
	}
	if len(directOrder) > 0 {
		result.text += " ORDER BY " + strings.Join(directOrder, ", ")
		result.args = append(result.args, orderArgs...)
	} else {
		result.text += sqlOrderBy(result.order, "")
	}
	if p.LimitPart != nil {
		result.text += " LIMIT ? OFFSET ?"
		result.args = append(result.args, p.LimitPart.Count, p.LimitPart.Offset)
	}
	return result, nil
}
func (e *mysqlSQLRenderer) sourceSQL(source qm.Relation, depth int) (string, []any, map[qm.FieldID]string, error) {
	if err := e.ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	e.nodes++
	if source == nil || reflect.ValueOf(source).IsNil() || depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return "", nil, nil, sqlUnsupported("来源超出生成预算")
	}
	switch r := source.(type) {
	case *qm.Scan:
		table, fields, err := e.scanMappings(r)
		if err != nil {
			return "", nil, nil, err
		}
		name, err := sqlIdentifier(table)
		if err != nil {
			return "", nil, nil, err
		}
		alias := fmt.Sprintf("t%d", r.Instance)
		scope := map[qm.FieldID]string{}
		for id, parts := range fields {
			column, err := sqlIdentifier(parts)
			if err != nil {
				return "", nil, nil, err
			}
			scope[id] = alias + "." + column
		}
		return name + " AS " + alias, nil, scope, nil
	case *qm.Join:
		left, a, scope, err := e.sourceSQL(r.Left, depth+1)
		if err != nil {
			return "", nil, nil, err
		}
		right, c, other, err := e.sourceSQL(r.Right, depth+1)
		if err != nil {
			return "", nil, nil, err
		}
		for id, name := range other {
			if scope[id] != "" {
				return "", nil, nil, sqlUnsupported("来源字段身份重复")
			}
			scope[id] = name
		}
		kind := map[qm.JoinKind]string{qm.JoinInner: "INNER JOIN", qm.JoinLeft: "LEFT JOIN", qm.JoinRight: "RIGHT JOIN", qm.JoinCross: "CROSS JOIN"}[r.Kind]
		if kind == "" {
			return "", nil, nil, sqlUnsupported("连接类型不受支持")
		}
		text := "(" + left + " " + kind + " " + right
		args := append(a, c...)
		if r.Kind != qm.JoinCross {
			on, values, err := e.expr(r.On, scope, depth+1)
			if err != nil {
				return "", nil, nil, err
			}
			text += " ON " + on
			args = append(args, values...)
		}
		return text + ")", args, scope, nil
	case *qm.Subquery:
		inner, err := e.selectQuery(r.Query, depth+1, false)
		if err != nil {
			return "", nil, nil, err
		}
		alias := fmt.Sprintf("t%d", r.Instance)
		scope := map[qm.FieldID]string{}
		for _, mapping := range r.Columns {
			if scope[mapping.Output] != "" {
				return "", nil, nil, sqlUnsupported("子查询输出映射重复")
			}
			scope[mapping.Output] = alias + "." + sqlField(mapping.Input)
		}
		if len(scope) != len(r.OutputSchema) {
			return "", nil, nil, sqlUnsupported("子查询输出映射不完整")
		}
		return "(" + inner.text + ") AS " + alias, inner.args, scope, nil
	default:
		return "", nil, nil, sqlUnsupported("来源类型不受支持")
	}
}

// scanMappings 校验来源实例和字段映射；单源限制仅作用于本次连接器调用。
func (e *mysqlSQLRenderer) scanMappings(scan *qm.Scan) (qm.PhysicalIdentifier, map[qm.FieldID]qm.PhysicalIdentifier, error) {
	if scan.Resource.CatalogID == "" || (e.catalogID != "" && e.catalogID != scan.Resource.CatalogID) {
		return nil, nil, sqlUnsupported("当前连接器调用不支持跨目录查询")
	}
	e.catalogID = scan.Resource.CatalogID
	valid := func(parts qm.PhysicalIdentifier) bool {
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if part == "" {
				return false
			}
		}
		return true
	}
	if scan.Instance == 0 || !valid(scan.Identifier) {
		return nil, nil, sqlUnsupported("来源物理名称或实例无效")
	}
	fields := make(map[qm.FieldID]qm.PhysicalIdentifier, len(scan.Fields))
	for _, field := range scan.Fields {
		if field.ID == 0 || fields[field.ID] != nil || !valid(field.Identifier) || field.NativeType.ConnectorType != e.connector {
			return nil, nil, sqlUnsupported("来源字段无效或与 Connector 不一致")
		}
		fields[field.ID] = field.Identifier
	}
	return scan.Identifier, fields, nil
}

func (e *mysqlSQLRenderer) expr(expr *qm.Sqlizer, scope map[qm.FieldID]string, depth int) (string, []any, error) {
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
	case qm.ExprCall:
		return e.aggregateCall(expr, scope, depth+1)
	case qm.ExprField:
		if scope[expr.Field] == "" {
			return "", nil, sqlUnsupported("字段不在当前输入作用域")
		}
		return scope[expr.Field], nil, nil
	case qm.ExprLiteral:
		if expr.Value != nil {
			if data, ok := expr.Value.Data.(qm.StringValue); ok {
				e.parameterBytes += len(data)
				if e.parameterBytes > qm.MaxSQLBytes {
					return "", nil, sqlUnsupported("参数字节数超出生成预算")
				}
			}
		}
		if e.mysql && expr.Value != nil && expr.Type.Kind == qm.TypeDecimal {
			data, ok := expr.Value.Data.(qm.DecimalValue)
			if !ok {
				return "", nil, sqlUnsupported("decimal 绑定值类型无效")
			}
			value, err := mysqlDecimal(string(data), expr.Type)
			if err != nil {
				return "", nil, err
			}
			return fmt.Sprintf("CAST(? AS DECIMAL(%d,%d))", expr.Type.Precision, expr.Type.Scale), []any{value}, nil
		}
		value, err := sqlLiteral(expr.Value)
		return "?", []any{value}, err
	case qm.ExprUnary:
		if expr.Op != qm.OperatorNot || len(expr.Args) != 1 {
			return "", nil, sqlUnsupported("一元运算尚不支持")
		}
		value, args, err := e.expr(expr.Args[0], scope, depth+1)
		return "(NOT " + value + ")", args, err
	case qm.ExprBinary:
		if len(expr.Args) != 2 {
			return "", nil, sqlUnsupported("二元表达式结构无效")
		}
		switch expr.Op {
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
		return "(" + left + " " + string(expr.Op) + " " + right + ")", append(a, b...), nil
	case qm.ExprCast:
		if len(expr.Args) != 1 || expr.Args[0] == nil || !e.canCast(expr.Args[0].Type, expr.Type) {
			return "", nil, sqlUnsupported("类型转换尚不能等价下推")
		}
		value, args, err := e.expr(expr.Args[0], scope, depth+1)
		if err != nil {
			return "", nil, err
		}
		if e.mysql && mysqlLosslessDecimalCast(expr.Args[0].Type, expr.Type) {
			return fmt.Sprintf("CAST(%s AS DECIMAL(%d,%d))", value, expr.Type.Precision, expr.Type.Scale), args, nil
		}
		return value, args, nil
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

func (e *mysqlSQLRenderer) comparisonExpr(expr *qm.Sqlizer, scope map[qm.FieldID]string, depth int) (string, []any, error) {
	value, args, err := e.expr(expr, scope, depth)
	if err != nil {
		return "", nil, err
	}
	if !e.scalarType(expr.Type) {
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

func sqlOrderBy(order []sqlSortColumn, alias string) string {
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

func (e *mysqlSQLRenderer) aggregateCall(expr *qm.Sqlizer, scope map[qm.FieldID]string, depth int) (string, []any, error) {
	if err := e.ctx.Err(); err != nil {
		return "", nil, err
	}
	e.nodes++
	if depth > qm.MaxDepth || e.nodes > qm.MaxNodes {
		return "", nil, sqlUnsupported("聚合表达式超出生成预算")
	}
	if expr == nil || expr.Kind != qm.ExprCall || len(expr.Requirements) != 0 || expr.Domain != qm.DomainVega {
		return "", nil, sqlUnsupported("聚合表达式无效")
	}
	if expr.Function == "COUNT_STAR" && len(expr.Args) == 0 && !expr.Distinct {
		return "COUNT(*)", nil, nil
	}
	if len(expr.Args) != 1 {
		return "", nil, sqlUnsupported("聚合参数无效")
	}
	switch expr.Function {
	case "COUNT", "MIN", "MAX":
	default:
		return "", nil, sqlUnsupported("聚合函数尚不能等价下推")
	}
	if expr.Distinct && expr.Function != "COUNT" {
		return "", nil, sqlUnsupported("聚合去重组合不受支持")
	}
	// 二进制字符串同时用于分组、COUNT DISTINCT 及 MIN/MAX，遵循相同字节序。
	value, args, err := e.comparisonExpr(expr.Args[0], scope, depth+1)
	if err != nil {
		return "", nil, err
	}
	if expr.Distinct {
		value = "DISTINCT " + value
	}
	return string(expr.Function) + "(" + value + ")", args, nil
}

// mysqlDecimal 仅做无损规范化。不能表示的有效位或小数位直接拒绝，不经过 float64。
func mysqlDecimal(text string, typ qm.LogicalType) (string, error) {
	if !mysqlDecimalType(typ) || len(text) == 0 || len(text) > qm.MaxSQLBytes {
		return "", sqlUnsupported("decimal 类型或值无效")
	}
	negative := strings.HasPrefix(text, "-")
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	integer, fraction, hasPoint := strings.Cut(text, ".")
	if integer == "" || (hasPoint && fraction == "") {
		return "", sqlUnsupported("decimal 表示无效")
	}
	for _, part := range []string{integer, fraction} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return "", sqlUnsupported("decimal 表示无效")
			}
		}
	}
	integer = strings.TrimLeft(integer, "0")
	if len(integer) > typ.Precision-typ.Scale {
		return "", sqlUnsupported("decimal 整数部分超出精确值域")
	}
	if len(fraction) > typ.Scale {
		if strings.Trim(fraction[typ.Scale:], "0") != "" {
			return "", sqlUnsupported("decimal 小数部分不能无损表示")
		}
		fraction = fraction[:typ.Scale]
	}
	fraction += strings.Repeat("0", typ.Scale-len(fraction))
	if integer == "" {
		integer = "0"
	}
	if integer == "0" && strings.Trim(fraction, "0") == "" {
		negative = false
	}
	result := integer
	if typ.Scale > 0 {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return result, nil
}

func mysqlDecimalType(typ qm.LogicalType) bool {
	return typ.Kind == qm.TypeDecimal && typ.Precision >= 1 && typ.Precision <= 38 && typ.Scale >= 0 && typ.Scale <= typ.Precision && typ.Scale <= 30
}

// 只开放 Binder 插入的无损拓宽；显式 CAST 的错误/舍入约束另行兑现。
func mysqlLosslessDecimalCast(from, to qm.LogicalType) bool {
	if !mysqlDecimalType(to) {
		return false
	}
	if from.Kind == qm.TypeInteger && from.BitWidth == 64 {
		digits := 19
		if from.Unsigned {
			digits = 20
		}
		return to.Precision-to.Scale >= digits
	}
	return mysqlDecimalType(from) && to.Scale >= from.Scale && to.Precision-to.Scale >= from.Precision-from.Scale
}

func (e *mysqlSQLRenderer) canCast(from, to qm.LogicalType) bool {
	if !e.scalarType(to) {
		return false
	}
	stringCast := (from.Kind == qm.TypeString || from.Kind == qm.TypeText) && (to.Kind == qm.TypeString || to.Kind == qm.TypeText)
	return from.Kind == qm.TypeNull || stringCast || (e.mysql && mysqlLosslessDecimalCast(from, to))
}
func (e *mysqlSQLRenderer) scalarType(typ qm.LogicalType) bool {
	return sqlScalarType(typ) || (e.mysql && (mysqlDecimalType(typ) || (typ.Kind == qm.TypeFloat && typ.BitWidth == 64)))
}

func (s *sqlResultStream) Schema() qm.Schema {
	return cloneSQLSchema(s.schema)
}

func (s *sqlResultStream) Completion() qm.SQLReadCompletion {
	completion := s.completion
	if completion.NextOffset != nil {
		completion.NextOffset = new(*completion.NextOffset)
	}
	return completion
}

func (s *sqlResultStream) Next(ctx context.Context) (qm.Batch, error) {
	if s.failure != nil {
		return qm.Batch{}, s.failure
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if s.done {
		return qm.Batch{}, io.EOF
	}
	if s.closed {
		return qm.Batch{}, errors.New("SQL 结果流已关闭")
	}
	batch := qm.Batch{}
	for len(batch.Rows) < sqlBatchRows {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		if s.limit != nil && s.read == *s.limit {
			more := s.rows.Next()
			if err := s.rows.Err(); err != nil {
				return s.fail(mariadbQueryError(err))
			}
			if err := s.finish(more); err != nil {
				return s.fail(err)
			}
			break
		}
		if !s.rows.Next() {
			if err := s.rows.Err(); err != nil {
				return s.fail(mariadbQueryError(err))
			}
			if err := s.finish(false); err != nil {
				return s.fail(err)
			}
			break
		}
		values := make([]any, len(s.schema))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := s.rows.Scan(dest...); err != nil {
			return s.fail(fmt.Errorf("SQL 结果读取失败: %w", err))
		}
		row := make(qm.Row, len(values))
		for i, raw := range values {
			cell, err := decodeSQLCell(raw, s.schema[i])
			if err != nil {
				return s.fail(err)
			}
			row[i] = cell
		}
		s.read++
		batch.Rows = append(batch.Rows, row)
	}
	if len(batch.Rows) == 0 {
		return qm.Batch{}, io.EOF
	}
	return batch, nil
}

func (s *sqlResultStream) finish(more bool) error {
	if err := s.rows.Close(); err != nil {
		return mariadbQueryError(err)
	}
	s.done = true
	s.completion = qm.SQLReadCompletion{Kind: qm.CompletionRelationEnd, RowsRead: s.read}
	if more {
		s.completion.Kind = qm.CompletionWindowEnd
		s.completion.NextOffset = new(s.offset + s.read)
	}
	return nil
}

func (s *sqlResultStream) fail(err error) (qm.Batch, error) {
	s.failure = err
	s.completion = qm.SQLReadCompletion{}
	_ = s.Close()
	return qm.Batch{}, err
}

func (s *sqlResultStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.rows != nil {
		return mariadbQueryError(s.rows.Close())
	}
	return nil
}

func decodeSQLCell(raw any, column qm.OutputColumn) (qm.Value, error) {
	if raw == nil {
		if !column.Nullable && column.Type.Kind != qm.TypeNull {
			return qm.Value{}, errors.New("源结果出现非空列 NULL")
		}
		return qm.Value{Type: column.Type, Data: qm.NullValue{}}, nil
	}
	if column.Type.Kind == qm.TypeFloat {
		var value float64
		var err error
		switch v := raw.(type) {
		case float64:
			value = v
		case []byte:
			value, err = strconv.ParseFloat(string(v), 64)
		case string:
			value, err = strconv.ParseFloat(v, 64)
		default:
			err = errors.New("源结果不是浮点数")
		}
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return qm.Value{}, errors.New("源结果浮点数不是有限值")
		}
		return qm.Value{Type: column.Type, Data: qm.FloatValue(value)}, nil
	}
	var text string
	switch v := raw.(type) {
	case []byte:
		text = string(v)
	case string:
		text = v
	case int64:
		text = strconv.FormatInt(v, 10)
	case uint64:
		text = strconv.FormatUint(v, 10)
	case bool:
		if v {
			text = "1"
		} else {
			text = "0"
		}
	default:
		return qm.Value{}, errors.New("源结果表示与解码类型不一致")
	}
	value := qm.Value{Type: column.Type}
	switch column.Type.Kind {
	case qm.TypeInteger:
		if column.Type.Unsigned {
			if _, err := strconv.ParseUint(text, 10, 64); err != nil {
				return qm.Value{}, errors.New("源结果整数超出精确值域")
			}
		} else if _, err := strconv.ParseInt(text, 10, 64); err != nil {
			return qm.Value{}, errors.New("源结果整数超出精确值域")
		}
		value.Data = qm.IntegerValue(text)
	case qm.TypeDecimal:
		normalized, err := mysqlDecimal(text, column.Type)
		if err != nil {
			return qm.Value{}, errors.New("源结果 decimal 超出精确类型值域")
		}
		value.Data = qm.DecimalValue(normalized)
	case qm.TypeBoolean:
		if text != "0" && text != "1" {
			return qm.Value{}, errors.New("源结果不是布尔值")
		}
		value.Data = qm.BooleanValue(text == "1")
	case qm.TypeString, qm.TypeText:
		if _, ok := raw.(string); !ok {
			if _, ok := raw.([]byte); !ok {
				return qm.Value{}, errors.New("源结果不是字符串")
			}
		}
		if !utf8.ValidString(text) {
			return qm.Value{}, errors.New("源结果字符串不是有效 UTF-8")
		}
		value.Data = qm.StringValue(text)
	default:
		return qm.Value{}, errors.New("源结果类型尚无精确解码支持")
	}
	return value, nil
}
