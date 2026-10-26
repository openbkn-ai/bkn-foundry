// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/functions"
)

var resourceID = regexp.MustCompile(interfaces.RegexPattern_NonBuiltin_ID)

// relation 只解析 FROM 中的表、JOIN 和显式子查询。
func (b *queryBinder) relation(source qm.Relation) (bindingResult, error) {
	if err := b.enter(source, qm.Origin{}); err != nil {
		return bindingResult{}, err
	}
	defer b.leave(source)
	switch r := source.(type) {
	case *qm.Scan:
		return b.source(r.ResourceID, r.Alias, r.Origin)
	case *qm.Join:
		return b.join(r)
	case *qm.Subquery:
		if r.Alias == "" {
			return bindingResult{}, bindFailure(r.Origin, "派生查询必须有别名")
		}
		input, err := b.selectQuery(r.Query)
		if err != nil {
			return bindingResult{}, err
		}
		result := bindingResult{scope: bindingScope{aliases: map[string]bool{r.Alias: true}}}
		derived := &qm.Subquery{Query: input.query, Alias: r.Alias, Instance: b.instanceID(), Origin: r.Origin}
		for _, c := range input.schema {
			old := c.ID
			c.ID = b.fieldID()
			c.Lineage = []qm.FieldID{old}
			result.schema = append(result.schema, c)
			result.scope.columns = append(result.scope.columns, scopeColumn{column: c, qualifier: r.Alias, domain: qm.DomainVega})
			derived.Columns = append(derived.Columns, qm.ColumnMapping{Input: old, Output: c.ID})
		}
		derived.OutputSchema = result.schema
		result.source = derived
		return result, nil
	default:
		return bindingResult{}, bindFailure(qm.Origin{}, "不支持的 FROM 节点")
	}
}

func (b *queryBinder) source(id, alias string, origin qm.Origin) (bindingResult, error) {
	if !resourceID.MatchString(id) {
		return bindingResult{}, bindFailure(origin, "资源引用非法")
	}
	r := &qm.Scan{ResourceID: id, Alias: alias, Origin: origin}
	resource, found := b.resources[r.ResourceID]
	if !found {
		basis, err := b.authorizer.AuthorizeSQLQueryResource(b.ctx, r.ResourceID, b.identity, interfaces.OPERATION_TYPE_QUERY_DATA)
		if err != nil {
			return bindingResult{}, err
		}
		if err := b.ctx.Err(); err != nil {
			return bindingResult{}, err
		}
		if basis.ResourceID != r.ResourceID || basis.Operation != interfaces.OPERATION_TYPE_QUERY_DATA {
			return bindingResult{}, bindFailure(r.Origin, "授权依据与查询资源不一致")
		}
		var connectorType string
		resource, connectorType, err = b.resolver.ResolveSQLQueryResource(b.ctx, r.ResourceID, b.identity)
		if err != nil {
			return bindingResult{}, err
		}
		if err := b.ctx.Err(); err != nil {
			return bindingResult{}, err
		}
		if resource == nil || resource.ID != r.ResourceID || resource.CatalogID == "" {
			return bindingResult{}, bindFailure(r.Origin, "资源元数据不完整")
		}
		if !resource.Enabled || (resource.Status != interfaces.ResourceStatusActive && resource.Status != interfaces.ResourceStatusDeprecated) {
			return bindingResult{}, bindFailure(r.Origin, "资源当前不可查询")
		}
		if !interfaces.IsPhysicalResourceCategory(resource.Category) && resource.Category != interfaces.ResourceCategoryLogicalView {
			return bindingResult{}, bindFailure(r.Origin, "逻辑资源展开尚未接入")
		}
		b.resources[r.ResourceID] = resource
		b.connectors[r.ResourceID] = connectorType
	}
	r.Instance = b.instanceID()
	if resource.Category == interfaces.ResourceCategoryLogicalView {
		return b.derivedView(r, resource)
	}
	alias = r.Alias
	if alias == "" {
		alias = r.ResourceID
	}
	result := bindingResult{scope: bindingScope{aliases: map[string]bool{alias: true}}}
	fields := make([]qm.SourceField, 0, len(resource.SchemaDefinition))
	names := make(map[string]bool)
	if uint64(b.nextField)+uint64(len(resource.SchemaDefinition)) > qm.MaxNodes {
		return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: r.Origin, Message: "资源字段数量超限"}
	}
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name == "" || names[property.Name] {
			return bindingResult{}, bindFailure(r.Origin, "资源字段名称缺失或重复")
		}
		names[property.Name] = true
		typ, native, err := sourcePropertyType(property, b.connectors[r.ResourceID], r.Origin)
		if err != nil {
			return bindingResult{}, err
		}
		c := qm.OutputColumn{ID: b.fieldID(), Name: property.Name, Type: typ, Nullable: true, Origin: r.Origin, Encoding: outputEncoding(typ)}
		c.Encoding.PublicType = property.Type
		fields = append(fields, qm.SourceField{ID: c.ID, LogicalName: c.Name, NativeType: native})
		result.schema = append(result.schema, c)
		result.scope.columns = append(result.scope.columns, scopeColumn{column: c, qualifier: alias, domain: native.Domain})
	}
	result.source = &qm.Scan{OutputSchema: result.schema, Resource: qm.ResourceReference{ID: r.ResourceID, CatalogID: resource.CatalogID, Category: resource.Category, Version: strconv.FormatInt(resource.UpdateTime, 10)}, Instance: r.Instance, Fields: fields}
	return result, nil
}

func (b *queryBinder) join(r *qm.Join) (bindingResult, error) {
	left, err := b.relation(r.Left)
	if err != nil {
		return bindingResult{}, err
	}
	right, err := b.relation(r.Right)
	if err != nil {
		return bindingResult{}, err
	}
	scope := bindingScope{aliases: make(map[string]bool)}
	for alias := range left.scope.aliases {
		scope.aliases[alias] = true
	}
	for alias := range right.scope.aliases {
		if scope.aliases[alias] {
			return bindingResult{}, bindFailure(r.Origin, "来源别名重复")
		}
		scope.aliases[alias] = true
	}
	scope.columns = append(scope.columns, left.scope.columns...)
	scope.columns = append(scope.columns, right.scope.columns...)
	var on *qm.Sqlizer
	switch r.Kind {
	case qm.JoinInner, qm.JoinLeft, qm.JoinRight:
		on, err = b.expr(r.On, &scope, false)
		if err != nil {
			return bindingResult{}, err
		}
		if on.Type.Kind != qm.TypeBoolean && on.Type.Kind != qm.TypeNull {
			return bindingResult{}, bindFailure(r.Origin, "JOIN 条件必须为布尔表达式")
		}
	case qm.JoinCross:
		if r.On != nil {
			return bindingResult{}, bindFailure(r.Origin, "CROSS JOIN 不接受 ON")
		}
	default:
		return bindingResult{}, bindFailure(r.Origin, "不支持的 JOIN 类型")
	}
	schema := make(qm.Schema, 0, len(scope.columns))
	for i := range scope.columns {
		if (r.Kind == qm.JoinLeft && i >= len(left.scope.columns)) || (r.Kind == qm.JoinRight && i < len(left.scope.columns)) {
			scope.columns[i].column.Nullable = true
		}
		schema = append(schema, scope.columns[i].column)
	}
	return bindingResult{source: &qm.Join{OutputSchema: schema, Left: left.source, Right: right.source, Kind: r.Kind, On: on}, schema: schema, scope: scope}, nil
}

// selectQuery 按 SQL 子句次序绑定，在同一种 Builder 中补齐表达式及输出元数据。
func (b *queryBinder) selectQuery(query *qm.SelectBuilder) (bindingResult, error) {
	if err := b.enter(query, qm.Origin{}); err != nil {
		return bindingResult{}, err
	}
	defer b.leave(query)
	parts := query.Clone()
	input, err := b.relation(parts.FromPart)
	if err != nil {
		return bindingResult{}, err
	}
	parts.FromPart = input.source
	for i, raw := range parts.WherePart {
		predicate, err := b.expr(raw, &input.scope, false)
		if err != nil {
			return bindingResult{}, err
		}
		if predicate.Type.Kind != qm.TypeBoolean && predicate.Type.Kind != qm.TypeNull {
			return bindingResult{}, bindFailure(raw.Origin, "WHERE 必须为布尔表达式")
		}
		parts.WherePart[i] = predicate
	}
	// 只识别查询块是否聚合；保留函数节点，不建立聚合槽或另一套表达式。
	hasAggregate := false
	inspected := 0
	active := map[*qm.Sqlizer]bool{}
	var inspect func(*qm.Sqlizer, int) error
	inspect = func(e *qm.Sqlizer, depth int) error {
		if e == nil {
			return bindFailure(qm.Origin{}, "缺少表达式")
		}
		if active[e] {
			return bindFailure(e.Origin, "表达式包含环")
		}
		inspected++
		if depth > qm.MaxDepth || inspected > qm.MaxNodes {
			return &qm.CompileError{Kind: qm.ErrorLimit, Origin: e.Origin, Message: "表达式超出绑定预算"}
		}
		active[e] = true
		defer delete(active, e)
		if e.Kind == qm.ExprCall {
			signature, ok := functions.Lookup(singleName(e.Name))
			hasAggregate = hasAggregate || (ok && signature.Evaluation == qm.EvaluationAggregate)
		}
		for _, arg := range e.Args {
			if err := inspect(arg, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, column := range parts.ColumnsPart {
		if err := inspect(column.Expr, 1); err != nil {
			return bindingResult{}, err
		}
	}
	for _, expr := range parts.HavingPart {
		if err := inspect(expr, 1); err != nil {
			return bindingResult{}, err
		}
	}
	for _, key := range parts.OrderByPart {
		if err := inspect(key.Expr, 1); err != nil {
			return bindingResult{}, err
		}
	}
	valueScope := input.scope
	if len(parts.GroupByPart) > 0 || hasAggregate || len(parts.HavingPart) > 0 {
		valueScope = bindingScope{aliases: input.scope.aliases, groupInput: &input.scope}
		for i, key := range parts.GroupByPart {
			expr, err := b.expr(key, &input.scope, false)
			if err != nil {
				return bindingResult{}, err
			}
			if !comparableType(expr.Type) {
				return bindingResult{}, bindFailure(key.Origin, "分组类型不支持")
			}
			c := qm.OutputColumn{ID: b.fieldID(), Type: expr.Type, Nullable: expr.Nullable, Encoding: expressionEncoding(expr, &input.scope)}
			column := scopeColumn{column: c, expression: expr, domain: expr.Domain}
			if key.Kind == qm.ExprName {
				column.column.Name = key.Name[len(key.Name)-1]
				original, err := lookupColumn(&input.scope, key.Name, key.Origin)
				if err != nil {
					return bindingResult{}, err
				}
				column.qualifier = original.qualifier
				duplicate := false
				for _, existing := range valueScope.columns {
					if existing.column.Name == column.column.Name && existing.qualifier == column.qualifier {
						duplicate = true
						break
					}
				}
				if !duplicate {
					valueScope.columns = append(valueScope.columns, column)
				}
			}
			valueScope.groups = append(valueScope.groups, groupBinding{source: key, column: column})
			parts.GroupByPart[i] = expr
		}

	}
	result := bindingResult{scope: bindingScope{aliases: map[string]bool{}}}
	var columns []qm.NamedExpr
	names := map[string]bool{}
	appendColumn := func(expr *qm.Sqlizer, name string, origin qm.Origin) error {
		if names[name] {
			return bindFailure(origin, "查询输出名称重复")
		}
		names[name] = true
		if b.nextField >= qm.MaxNodes {
			return &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "绑定输出槽数量超限"}
		}
		c := qm.OutputColumn{ID: b.fieldID(), Name: name, Type: expr.Type, Nullable: expr.Nullable, Lineage: expr.FieldIDs(), Origin: origin, Encoding: expressionEncoding(expr, &input.scope)}
		result.schema = append(result.schema, c)
		result.scope.columns = append(result.scope.columns, scopeColumn{column: c, expression: expr, domain: expr.Domain})
		columns = append(columns, qm.NamedExpr{Expr: expr, Alias: name, Output: c.ID, Encoding: c.Encoding, Origin: origin})
		return nil
	}
	for i, item := range parts.ColumnsPart {
		if item.Expr == nil {
			return bindingResult{}, bindFailure(item.Origin, "缺少投影表达式")
		}
		if item.Expr.Kind == qm.ExprWildcard {
			if item.Alias != "" || len(item.Expr.Name) > 1 {
				return bindingResult{}, bindFailure(item.Origin, "通配符结构非法")
			}
			matched := false
			for _, col := range valueScope.columns {
				if len(item.Expr.Name) == 1 && item.Expr.Name[0] != col.qualifier {
					continue
				}
				matched = true
				if err := appendColumn(columnExpr(col, item.Origin), col.column.Name, item.Origin); err != nil {
					return bindingResult{}, err
				}
			}
			if !matched {
				if item.Origin.Kind == qm.OriginDTO {
					return bindingResult{}, interfaces.NewOutputFieldError(interfaces.OutputFieldNoneQueryable, "")
				}
				return bindingResult{}, bindFailure(item.Origin, "通配符没有可见字段")
			}
			continue
		}
		expr, err := b.expr(item.Expr, &valueScope, true)
		if err != nil {
			if item.Origin.Kind == qm.OriginDTO && strings.HasPrefix(item.Origin.Path, "output_fields[") && item.Expr.Kind == qm.ExprName && len(item.Expr.Name) == 1 {
				return bindingResult{}, interfaces.NewOutputFieldError(interfaces.OutputFieldUnknown, item.Expr.Name[0])
			}
			return bindingResult{}, err
		}
		name := item.Alias
		if name == "" {
			if item.Expr.Kind == qm.ExprName {
				name = item.Expr.Name[len(item.Expr.Name)-1]
			} else {
				name = fmt.Sprintf("expr_%d", i+1)
			}
		}
		if err := appendColumn(expr, name, item.Origin); err != nil {
			return bindingResult{}, err
		}
	}
	if len(columns) == 0 {
		return bindingResult{}, bindFailure(qm.Origin{}, "缺少投影")
	}
	parts.ColumnsPart = columns
	aliasScope := result.scope
	aliasScope.fallback = &valueScope
	for i, raw := range parts.HavingPart {
		expr, err := b.expr(raw, &aliasScope, true)
		if err != nil {
			return bindingResult{}, err
		}
		if expr.Type.Kind != qm.TypeBoolean && expr.Type.Kind != qm.TypeNull {
			return bindingResult{}, bindFailure(raw.Origin, "HAVING 必须为布尔表达式")
		}
		parts.HavingPart[i] = expr
	}
	for i, key := range parts.OrderByPart {
		var expr *qm.Sqlizer
		if key.Expr != nil && key.Expr.Kind == qm.ExprLiteral && key.Expr.Literal != nil && key.Expr.Literal.Kind == qm.LiteralInteger {
			position, err := strconv.Atoi(key.Expr.Literal.Text)
			if err != nil || position < 1 || position > len(columns) {
				return bindingResult{}, bindFailure(key.Origin, "排序位置超出投影范围")
			}
			expr = columns[position-1].Expr
		} else {
			expr, err = b.expr(key.Expr, &aliasScope, true)
			if err != nil {
				return bindingResult{}, err
			}
		}
		if !comparableType(expr.Type) {
			return bindingResult{}, bindFailure(key.Origin, "排序类型不支持")
		}
		if parts.DistinctPart {
			exact := false
			visible := map[qm.FieldID]bool{}
			for _, c := range columns {
				exact = exact || sameBoundExpr(c.Expr, expr)
				if c.Expr.Kind == qm.ExprField {
					visible[c.Expr.Field] = true
				}
			}
			if !exact {
				for _, id := range expr.FieldIDs() {
					if !visible[id] {
						return bindingResult{}, bindFailure(key.Origin, "去重后的排序表达式必须由输出字段确定")
					}
				}
			}
		}
		key.Expr = expr
		key.Nulls = qm.NullsFirst
		if key.Descending {
			key.Nulls = qm.NullsLast
		}
		parts.OrderByPart[i] = key
	}
	if parts.LimitPart != nil && (parts.LimitPart.Count < 0 || parts.LimitPart.Offset < 0) {
		return bindingResult{}, bindFailure(qm.Origin{}, "LIMIT 不能为负")
	}
	result.query = parts
	return result, nil
}
