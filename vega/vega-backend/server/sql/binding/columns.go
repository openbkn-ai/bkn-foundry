// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package binding 按已授权资源的公开 Schema 绑定 SQL 字段，不访问资源服务或源数据库。
package binding

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

type publicColumn struct {
	name     string
	physical string
}

type relationColumns struct {
	qualifier string
	columns   []publicColumn
	table     *sb.Table
}

type columnOrigin struct {
	qualifier string
	table     *sb.Table
}

type outputColumn struct {
	name string
	expr sb.SqlNode
}

type columnScope struct {
	sources      []relationColumns
	outputs      []outputColumn
	preferOutput bool
	binder       *columnBinder
}

type columnBinder struct {
	resources map[string]*interfaces.Resource
	origins   map[*sb.Column]columnOrigin
	aliases   map[string]bool
	nextAlias int
}

// BindColumns 校验并绑定 SQL 字段引用，原地改写查询树。
// 按 SchemaDefinition.Name 精确匹配字段和别名，检查作用域、引用歧义、UNION 列数及分组/排序位置。
// 绑定时展开投影星号和输出别名，按需将 Table 公开字段映射为 OriginalName，并补充来源限定。
// 调用方须先完成权限、资源和来源检查；含 View 的查询须在展开固定过滤之前调用一次。
// 校验失败时查询树可能已被部分改写，调用方应丢弃该查询树。
func BindColumns(query *sb.SelectBuilder, resources map[string]*interfaces.Resource) error {
	binder := columnBinder{
		resources: resources,
		origins:   make(map[*sb.Column]columnOrigin),
		aliases:   make(map[string]bool),
	}
	binder.reserveAliases(query)
	_, err := binder.selectQuery(query)
	return err
}

func (binder *columnBinder) relation(node sb.SqlNode) ([]relationColumns, error) {
	switch source := node.(type) {
	case *sb.Table:
		resource := binder.resources[source.ResourceID]
		if resource == nil {
			return nil, interfaces.NewConditionBuildError("SQL resource metadata is unavailable")
		}
		alias := source.Alias
		if alias == "" {
			alias = source.SourceName
		}
		columns := make([]publicColumn, 0, len(resource.SchemaDefinition))
		seen := make(map[string]bool)
		for _, field := range resource.SchemaDefinition {
			if field == nil || field.Name == "" {
				continue
			}
			if seen[field.Name] {
				return nil, interfaces.NewConditionBuildError("SQL resource has ambiguous public columns")
			}
			seen[field.Name] = true
			physical := field.Name
			if resource.Category == interfaces.ResourceCategoryTable && field.OriginalName != "" {
				physical = field.OriginalName
			}
			columns = append(columns, publicColumn{name: field.Name, physical: physical})
		}
		return []relationColumns{{qualifier: alias, columns: columns, table: source}}, nil
	case *sb.Subquery:
		outputs, err := binder.selectQuery(source.Query)
		return []relationColumns{outputRelation(source.Alias, outputs)}, err
	case *sb.Union:
		left, err := binder.selectQuery(source.Left)
		if err != nil {
			return nil, err
		}
		right, err := binder.selectQuery(source.Right)
		if err != nil {
			return nil, err
		}
		if len(left) != len(right) {
			return nil, interfaces.NewConditionBuildError("UNION branches must have the same number of columns")
		}
		return []relationColumns{outputRelation(source.Alias, left)}, nil
	case *sb.JoinedTable:
		relations, err := binder.relation(source.From)
		if err != nil {
			return nil, err
		}
		return binder.joins(relations, source.Joins)
	case nil:
		return nil, nil
	default:
		return nil, interfaces.NewConditionBuildError("unsupported SQL source")
	}
}

func outputRelation(qualifier string, outputs []outputColumn) relationColumns {
	relation := relationColumns{qualifier: qualifier, columns: make([]publicColumn, 0, len(outputs))}
	for _, output := range outputs {
		relation.columns = append(relation.columns, publicColumn{name: output.name, physical: output.name})
	}
	return relation
}

// joins 按顺序绑定 ON，括号中的 JOIN 组拥有独立的来源范围，不能引用后续 JOIN。
func (binder *columnBinder) joins(relations []relationColumns, joins []sb.Join) ([]relationColumns, error) {
	if err := uniqueRelations(relations); err != nil {
		return nil, err
	}
	for i := range joins {
		joined, err := binder.relation(joins[i].Source)
		if err != nil {
			return nil, err
		}
		relations = append(relations, joined...)
		if err := uniqueRelations(relations); err != nil {
			return nil, err
		}
		joins[i].On, err = bindExpression(joins[i].On, columnScope{sources: relations, binder: binder})
		if err != nil {
			return nil, err
		}
	}
	return relations, nil
}

func uniqueRelations(relations []relationColumns) error {
	seen := make(map[string]bool)
	for _, relation := range relations {
		if relation.qualifier == "" {
			continue
		}
		if seen[relation.qualifier] {
			return interfaces.NewConditionBuildError("query contains duplicate source aliases")
		}
		seen[relation.qualifier] = true
	}
	return nil
}

func (binder *columnBinder) selectQuery(query *sb.SelectBuilder) ([]outputColumn, error) {
	if query == nil {
		return nil, interfaces.NewConditionBuildError("SQL query is required")
	}
	relations, err := binder.relation(query.FromPart)
	if err != nil {
		return nil, err
	}
	relations, err = binder.joins(relations, query.JoinsPart)
	if err != nil {
		return nil, err
	}
	scope := columnScope{sources: relations, binder: binder}
	columns, outputs, err := bindProjection(query.ColumnsPart, scope)
	if err != nil {
		return nil, err
	}
	// Parser 为 UNION 生成透明星号包装，保留该结构供方言生成器展开左结合链。
	_, union := query.FromPart.(*sb.Union)
	starWrapper := union && len(query.ColumnsPart) == 1
	if starWrapper {
		star, ok := query.ColumnsPart[0].(*sb.Star)
		starWrapper = ok && star.Qualifier == ""
	}
	if !starWrapper {
		query.ColumnsPart = columns
	} else {
		for i := range outputs {
			outputs[i].expr = &sb.Column{Name: outputs[i].name}
		}
	}
	if err := bindExpressions(query.WherePart, scope); err != nil {
		return nil, err
	}
	scope.outputs = outputs
	for _, group := range query.GroupByPart {
		if err := validateGroupPosition(group, uint64(len(outputs))); err != nil {
			return nil, err
		}
	}
	if err := bindExpressions(query.GroupByPart, scope); err != nil {
		return nil, err
	}
	scope.preferOutput = true
	if err := bindExpressions(query.HavingPart, scope); err != nil {
		return nil, err
	}
	for i := range query.OrderByPart {
		order := &query.OrderByPart[i]
		if order.Position != 0 {
			if order.Expr != nil || order.Position > uint64(len(outputs)) {
				return nil, interfaces.NewConditionBuildError("ORDER BY position is outside the query output")
			}
			continue
		}
		if column, ok := order.Expr.(*sb.Column); ok && column.Qualifier == "" {
			index, err := scope.outputIndex(column.Name)
			if err != nil {
				return nil, err
			}
			if index >= 0 && isIntegerLiteral(outputs[index].expr) {
				// 指向输出位置，避免常量被误作序号或加零后不再匹配 DISTINCT 投影。
				order.Expr, order.Position = nil, uint64(index+1)
				continue
			}
		}
		order.Expr, err = bindExpression(order.Expr, scope)
		if err != nil {
			return nil, err
		}
	}
	return outputs, nil
}

func bindProjection(projection []sb.SqlNode, scope columnScope) ([]sb.SqlNode, []outputColumn, error) {
	var columns []sb.SqlNode
	var outputs []outputColumn
	for _, node := range projection {
		if star, ok := node.(*sb.Star); ok {
			matched := false
			for _, relation := range scope.sources {
				if star.Qualifier != "" && star.Qualifier != relation.qualifier {
					continue
				}
				matched = true
				for _, field := range relation.columns {
					origin := columnOrigin{qualifier: relation.qualifier, table: relation.table}
					qualifier := relation.qualifier
					if len(scope.sources) > 1 && qualifier == "" {
						qualifier = scope.binder.originQualifier(origin)
					}
					column := &sb.Column{Qualifier: qualifier, Name: field.physical}
					scope.binder.origins[column] = origin
					var expression sb.SqlNode = column
					outputs = append(outputs, outputColumn{name: field.name, expr: expression})
					if field.name != field.physical {
						expression = &sb.Alias{Expr: expression, Name: field.name}
					}
					columns = append(columns, expression)
				}
			}
			if !matched {
				return nil, nil, unknownColumn()
			}
			continue
		}
		name := ""
		switch projection := node.(type) {
		case *sb.Alias:
			name = projection.Name
		case *sb.Column:
			name = projection.Name
		}
		bound, err := bindExpression(node, scope)
		if err != nil {
			return nil, nil, err
		}
		expression := bound
		if alias, ok := bound.(*sb.Alias); ok {
			expression = alias.Expr
		} else if column, ok := bound.(*sb.Column); ok && column.Name != name {
			bound = &sb.Alias{Expr: column, Name: name}
		}
		columns = append(columns, bound)
		outputs = append(outputs, outputColumn{name: name, expr: expression})
	}
	if len(columns) == 0 {
		return nil, nil, interfaces.NewConditionBuildError("query has no public output columns")
	}
	return columns, outputs, nil
}

func validateGroupPosition(node sb.SqlNode, width uint64) error {
	literal, ok := node.(*sb.Literal)
	if !ok {
		return nil
	}
	number, ok := literal.Value.(json.Number)
	if !ok || strings.ContainsAny(string(number), ".eE") {
		return nil
	}
	position, err := strconv.ParseUint(string(number), 10, 64)
	if err != nil || position == 0 || position > width {
		return interfaces.NewConditionBuildError("GROUP BY position is outside the query output")
	}
	return nil
}

func unknownColumn() error {
	return interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, nil)
}

func (scope columnScope) column(column *sb.Column) (sb.SqlNode, error) {
	if column.Qualifier == "" && scope.preferOutput {
		output, found, err := scope.output(column.Name)
		if found || err != nil {
			return output, err
		}
	}
	var matched *publicColumn
	qualifier := column.Qualifier
	var origin columnOrigin
	for _, relation := range scope.sources {
		if column.Qualifier != "" && column.Qualifier != relation.qualifier {
			continue
		}
		for i := range relation.columns {
			field := &relation.columns[i]
			if field.name != column.Name {
				continue
			}
			if matched != nil {
				return nil, interfaces.NewConditionBuildError("query references an ambiguous column")
			}
			matched = field
			origin = columnOrigin{qualifier: relation.qualifier, table: relation.table}
			if qualifier == "" && len(scope.sources) > 1 {
				qualifier = relation.qualifier
			}
		}
	}
	if matched != nil {
		if qualifier == "" && (len(scope.sources) > 1 || scope.preferOutput) {
			qualifier = scope.binder.originQualifier(origin)
		}
		bound := &sb.Column{Qualifier: qualifier, Name: matched.physical}
		scope.binder.origins[bound] = origin
		return bound, nil
	}
	if column.Qualifier == "" && !scope.preferOutput {
		output, found, err := scope.output(column.Name)
		if found || err != nil {
			return output, err
		}
	}
	return nil, unknownColumn()
}

func (scope columnScope) outputIndex(name string) (int, error) {
	index := -1
	for i, output := range scope.outputs {
		if output.name != name {
			continue
		}
		if index >= 0 {
			return -1, interfaces.NewConditionBuildError("query references an ambiguous output alias")
		}
		index = i
	}
	return index, nil
}

func isIntegerLiteral(node sb.SqlNode) bool {
	literal, ok := node.(*sb.Literal)
	if !ok {
		return false
	}
	number, ok := literal.Value.(json.Number)
	return ok && !strings.ContainsAny(string(number), ".eE")
}

func (scope columnScope) output(name string) (sb.SqlNode, bool, error) {
	index, err := scope.outputIndex(name)
	if index < 0 || err != nil {
		return nil, false, err
	}
	matched := scope.outputs[index].expr
	if isIntegerLiteral(matched) {
		// 别名嵌入表达式或用于 GROUP BY 时保持常量含义，不能变成位置序号。
		return &sb.Arithmetic{Left: matched, Op: sb.Add, Right: &sb.Literal{Value: json.Number("0")}}, true, nil
	}
	collision := false
	for _, output := range scope.outputs {
		if output.name != name && strings.EqualFold(output.name, name) {
			collision = true
			break
		}
	}
	if column, ok := matched.(*sb.Column); !ok || column.Name != name || collision {
		if err := scope.binder.qualifyOutput(matched); err != nil {
			return nil, true, err
		}
	}
	return matched, true, nil
}

func (binder *columnBinder) reserveAliases(node sb.SqlNode) {
	switch source := node.(type) {
	case *sb.Table:
		binder.aliases[strings.ToLower(source.Alias)] = true
		binder.aliases[strings.ToLower(source.SourceName)] = true
	case *sb.Subquery:
		binder.aliases[strings.ToLower(source.Alias)] = true
		binder.reserveAliases(source.Query)
	case *sb.Union:
		binder.aliases[strings.ToLower(source.Alias)] = true
		binder.reserveAliases(source.Left)
		binder.reserveAliases(source.Right)
	case *sb.JoinedTable:
		binder.reserveAliases(source.From)
		for _, join := range source.Joins {
			binder.reserveAliases(join.Source)
		}
	case *sb.SelectBuilder:
		if source != nil {
			binder.reserveAliases(source.FromPart)
			for _, join := range source.JoinsPart {
				binder.reserveAliases(join.Source)
			}
		}
	}
}

// qualifyOutput 确定别名展开后的来源列，避免源列名被源数据库再次解释为另一个输出别名。
func (binder *columnBinder) qualifyOutput(node sb.SqlNode) error {
	if column, ok := node.(*sb.Column); ok {
		origin, found := binder.origins[column]
		if column.Qualifier != "" || !found {
			return nil
		}
		column.Qualifier = binder.originQualifier(origin)
		return nil
	}
	fields, err := expressionFields(node)
	if err != nil {
		return err
	}
	for _, field := range fields {
		if err := binder.qualifyOutput(*field); err != nil {
			return err
		}
	}
	return nil
}

func (binder *columnBinder) originQualifier(origin columnOrigin) string {
	if origin.table == nil {
		return origin.qualifier
	}
	if origin.table.Alias != "" {
		return origin.table.Alias
	}
	if origin.qualifier != "" {
		return origin.qualifier
	}
	for {
		binder.nextAlias++
		alias := "_source_" + strconv.Itoa(binder.nextAlias)
		if !binder.aliases[alias] {
			binder.aliases[alias] = true
			origin.table.Alias = alias
			return alias
		}
	}
}

func bindExpressions(nodes []sb.SqlNode, scope columnScope) error {
	for i := range nodes {
		bound, err := bindExpression(nodes[i], scope)
		if err != nil {
			return err
		}
		nodes[i] = bound
	}
	return nil
}

// bindExpression 仅处理字段作用域；类型、函数和聚合是否合法继续由既有编译器与源数据库检查。
func bindExpression(node sb.SqlNode, scope columnScope) (sb.SqlNode, error) {
	if column, ok := node.(*sb.Column); ok {
		return scope.column(column)
	}
	if function, ok := node.(*sb.Function); ok {
		switch function.Name {
		case "COUNT", "SUM", "AVG", "MIN", "MAX":
			// 聚合内部引用输入行字段，不能把同名输出别名再次替换成整个聚合。
			scope.outputs = nil
			scope.preferOutput = false
		}
	}
	fields, err := expressionFields(node)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		bound, err := bindExpression(*field, scope)
		if err != nil {
			return nil, err
		}
		*field = bound
	}
	return node, nil
}

func expressionFields(node sb.SqlNode) ([]*sb.SqlNode, error) {
	var fields []*sb.SqlNode
	switch expression := node.(type) {
	case nil, *sb.Literal, *sb.Param, *sb.Column:
		return nil, nil
	case *sb.Alias:
		fields = []*sb.SqlNode{&expression.Expr}
	case *sb.Eq:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.NotEq:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.Gt:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.GtOrEq:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.Lt:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.LtOrEq:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.And:
		for i := range *expression {
			fields = append(fields, &(*expression)[i])
		}
	case *sb.Or:
		for i := range *expression {
			fields = append(fields, &(*expression)[i])
		}
	case *sb.Not:
		fields = []*sb.SqlNode{&expression.Expr}
	case *sb.IsNull:
		fields = []*sb.SqlNode{&expression.Expr}
	case *sb.In:
		fields = append(fields, &expression.Expr)
		for i := range expression.Values {
			fields = append(fields, &expression.Values[i])
		}
	case *sb.Between:
		fields = []*sb.SqlNode{&expression.Expr, &expression.Lower, &expression.Upper}
	case *sb.Like:
		fields = []*sb.SqlNode{&expression.Expr, &expression.Pattern}
	case *sb.Regex:
		fields = []*sb.SqlNode{&expression.Expr, &expression.Pattern}
	case *sb.Contains:
		fields = []*sb.SqlNode{&expression.Expr, &expression.Value}
	case *sb.Arithmetic:
		fields = []*sb.SqlNode{&expression.Left, &expression.Right}
	case *sb.Unary:
		fields = []*sb.SqlNode{&expression.Expr}
	case *sb.Function:
		if expression.Name == "COUNT" && len(expression.Args) == 1 {
			if star, ok := expression.Args[0].(*sb.Star); ok && star.Qualifier == "" {
				return nil, nil
			}
		}
		for i := range expression.Args {
			fields = append(fields, &expression.Args[i])
		}
	case *sb.Case:
		fields = []*sb.SqlNode{&expression.Operand, &expression.Else}
		for i := range expression.WhenParts {
			fields = append(fields, &expression.WhenParts[i].Condition, &expression.WhenParts[i].Result)
		}
	case *sb.Cast:
		fields = []*sb.SqlNode{&expression.Expr}
	case *sb.Interval:
		fields = []*sb.SqlNode{&expression.Value}
	default:
		return nil, interfaces.NewConditionBuildError("unsupported SQL expression")
	}
	return fields, nil
}
