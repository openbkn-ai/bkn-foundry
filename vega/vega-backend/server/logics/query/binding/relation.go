// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/functions"
)

var resourceID = regexp.MustCompile(interfaces.RegexPattern_NonBuiltin_ID)

func relationBase(schema qm.Schema, origin qm.Origin) qm.BoundRelationBase {
	return qm.BoundRelationBase{OutputSchema: schema, Origin: origin}
}
func (b *queryBinder) relation(relation qm.Relation) (bindingResult, error) {
	if err := b.enter(relation, qm.Origin{}); err != nil {
		return bindingResult{}, err
	}
	defer b.leave(relation)
	switch r := relation.(type) {
	case *qm.Scan:
		return b.scan(r)
	case *qm.Join:
		return b.join(r)
	case *qm.Project:
		return b.projectRelation(r)
	case *qm.Aggregate:
		return b.aggregateRelation(r)
	case *qm.Filter:
		input, err := b.relation(r.Input)
		if err != nil {
			return bindingResult{}, err
		}
		predicate, err := b.expr(r.Predicate, &input.scope, false)
		if err != nil {
			return bindingResult{}, err
		}
		if predicate.Type.Kind != qm.TypeBoolean && predicate.Type.Kind != qm.TypeNull {
			return bindingResult{}, bindFailure(r.Origin, "过滤条件必须为布尔表达式")
		}
		input.root = &qm.BoundFilter{BoundRelationBase: relationBase(input.schema, r.Origin), Input: input.root, Predicate: predicate, Having: r.Having}
		input.project = nil
		input.sortInput = nil
		return input, nil
	case *qm.Distinct:
		input, err := b.relation(r.Input)
		if err != nil {
			return bindingResult{}, err
		}
		keys := make([]qm.FieldID, 0, len(input.schema))
		for _, c := range input.schema {
			if !comparableType(c.Type) {
				return bindingResult{}, bindFailure(r.Origin, "去重字段类型不支持")
			}
			keys = append(keys, c.ID)
		}
		input.root = &qm.BoundDistinct{BoundRelationBase: relationBase(input.schema, r.Origin), Input: input.root, Keys: keys}
		input.distinct = true
		return input, nil
	case *qm.Sort:
		return b.sortRelation(r)
	case *qm.Limit:
		if r.Offset < 0 || r.Count < 0 {
			return bindingResult{}, bindFailure(r.Origin, "LIMIT 不能为负")
		}
		input, err := b.relation(r.Input)
		if err != nil {
			return bindingResult{}, err
		}
		input.root = &qm.BoundLimit{BoundRelationBase: relationBase(input.schema, r.Origin), Input: input.root, Offset: r.Offset, Count: r.Count}
		input.project = nil
		input.sortInput = nil
		return input, nil
	case *qm.Subquery:
		if r.Scope == 0 || b.scopes[r.Scope] || r.Alias == "" || r.Instance == 0 || b.instances[r.Instance] {
			return bindingResult{}, bindFailure(r.Origin, "派生查询身份或别名非法")
		}
		b.scopes[r.Scope] = true
		b.instances[r.Instance] = true
		input, err := b.relation(r.Input)
		if err != nil {
			return bindingResult{}, err
		}
		input = b.trimHidden(input)
		if uint64(b.nextField)+uint64(len(input.schema)) > qm.MaxNodes {
			return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: r.Origin, Message: "绑定输出槽数量超限"}
		}
		result := bindingResult{scope: bindingScope{aliases: map[string]bool{r.Alias: true}}}
		mappings := make([]qm.ColumnMapping, 0, len(input.schema))
		for _, c := range input.schema {
			old := c.ID
			c.ID = b.fieldID()
			c.Lineage = []qm.FieldID{old}
			result.schema = append(result.schema, c)
			result.scope.columns = append(result.scope.columns, scopeColumn{column: c, qualifier: r.Alias, domain: qm.DomainVega})
			mappings = append(mappings, qm.ColumnMapping{Input: old, Output: c.ID})
		}
		result.root = &qm.BoundSubquery{BoundRelationBase: relationBase(result.schema, r.Origin), Input: input.root, Scope: b.scopeID(), Instance: b.instanceID(), Alias: r.Alias, Columns: mappings}
		return result, nil
	case *qm.ViewBoundary:
		return bindingResult{}, bindFailure(r.Origin, "视图展开与授权适配尚未接入")
	default:
		return bindingResult{}, bindFailure(qm.Origin{}, "未知的查询关系")
	}
}

func (b *queryBinder) scan(r *qm.Scan) (bindingResult, error) {
	if !resourceID.MatchString(r.ResourceID) || r.Instance == 0 || b.instances[r.Instance] {
		return bindingResult{}, bindFailure(r.Origin, "资源引用或来源实例非法")
	}
	b.instances[r.Instance] = true
	return b.source(r.ResourceID, r.Alias, r.Origin)
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
		resource, err = b.resolver.ResolveSQLQueryResource(b.ctx, r.ResourceID, b.identity)
		if err != nil {
			return bindingResult{}, err
		}
		if err := b.ctx.Err(); err != nil {
			return bindingResult{}, err
		}
		if resource == nil || resource.ID != r.ResourceID || resource.CatalogID == "" {
			return bindingResult{}, bindFailure(r.Origin, "资源元数据不完整")
		}
		if !resource.Enabled || resource.Status != interfaces.ResourceStatusActive {
			return bindingResult{}, bindFailure(r.Origin, "资源当前不可查询")
		}
		if !interfaces.IsPhysicalResourceCategory(resource.Category) && resource.Category != interfaces.ResourceCategoryLogicalView {
			return bindingResult{}, bindFailure(r.Origin, "逻辑资源展开尚未接入")
		}
		snapshot := any(resource.SchemaDefinition)
		kind := qm.DependencyResource
		if resource.Category == interfaces.ResourceCategoryLogicalView {
			kind = qm.DependencyView
			snapshot = struct {
				Schema     []*interfaces.Property
				LogicType  string
				Definition any
			}{resource.SchemaDefinition, resource.LogicType, resource.LogicDefinition}
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return bindingResult{}, bindFailure(r.Origin, "资源字段元数据无效")
		}
		digest := sha256.Sum256(encoded)
		b.dependencies = append(b.dependencies, qm.Dependency{Kind: kind, ID: r.ResourceID, Version: strconv.FormatInt(resource.UpdateTime, 10), SchemaFingerprint: hex.EncodeToString(digest[:]), Authorization: []qm.AuthorizationBasis{basis}})
		b.resources[r.ResourceID] = resource
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
	fields := make([]qm.BoundSourceField, 0, len(resource.SchemaDefinition))
	names := make(map[string]bool)
	if uint64(b.nextField)+uint64(len(resource.SchemaDefinition)) > qm.MaxNodes {
		return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: r.Origin, Message: "资源字段数量超限"}
	}
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name == "" || names[property.Name] {
			return bindingResult{}, bindFailure(r.Origin, "资源字段名称缺失或重复")
		}
		names[property.Name] = true
		typ, native, err := propertyType(property, r.Origin)
		if err != nil {
			return bindingResult{}, err
		}
		c := qm.OutputColumn{ID: b.fieldID(), Name: property.Name, Type: typ, Nullable: true, Origin: r.Origin, Encoding: outputEncoding(typ)}
		fields = append(fields, qm.BoundSourceField{ID: c.ID, LogicalName: c.Name, NativeType: native})
		result.schema = append(result.schema, c)
		result.scope.columns = append(result.scope.columns, scopeColumn{column: c, qualifier: alias, domain: native.Domain})
	}
	result.root = &qm.BoundScan{BoundRelationBase: relationBase(result.schema, r.Origin), Resource: qm.ResourceReference{ID: r.ResourceID, CatalogID: resource.CatalogID, Category: resource.Category, Version: strconv.FormatInt(resource.UpdateTime, 10)}, Instance: r.Instance, Fields: fields}
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
	var on *qm.BoundExpr
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
	return bindingResult{root: &qm.BoundJoin{BoundRelationBase: relationBase(schema, r.Origin), Left: left.root, Right: right.root, Kind: r.Kind, On: on}, schema: schema, scope: scope}, nil
}

func (b *queryBinder) projectRelation(r *qm.Project) (bindingResult, error) {
	input, err := b.relation(r.Input)
	if err != nil {
		return bindingResult{}, err
	}
	if len(r.Columns) == 0 {
		return bindingResult{}, bindFailure(r.Origin, "缺少投影")
	}
	project := &qm.BoundProject{Input: input.root}
	result := bindingResult{project: project, sortInput: &input.scope}
	names := make(map[string]bool)
	appendColumn := func(expr *qm.BoundExpr, name string, origin qm.Origin) error {
		if b.nextField >= qm.MaxNodes {
			return &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "绑定输出槽数量超限"}
		}
		if names[name] {
			return bindFailure(origin, "查询输出名称重复")
		}
		names[name] = true
		c := qm.OutputColumn{ID: b.fieldID(), Name: name, Type: expr.Type, Nullable: expr.Nullable, Lineage: expressionFields(expr), Origin: origin, Encoding: outputEncoding(expr.Type)}
		result.schema = append(result.schema, c)
		result.scope.columns = append(result.scope.columns, scopeColumn{column: c, domain: expr.Domain})
		project.Columns = append(project.Columns, qm.BoundNamedExpr{Output: c.ID, Expr: expr, Origin: origin})
		return nil
	}
	for i, item := range r.Columns {
		if item.Expr == nil {
			return bindingResult{}, bindFailure(item.Origin, "缺少投影表达式")
		}
		if item.Expr.Kind == qm.ExprWildcard {
			if item.Alias != "" || len(item.Expr.Name) > 1 {
				return bindingResult{}, bindFailure(item.Origin, "通配符结构非法")
			}
			matched := false
			for _, col := range input.scope.columns {
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
		expr, err := b.expr(item.Expr, &input.scope, false)
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
	project.BoundRelationBase = relationBase(result.schema, r.Origin)
	result.root = project
	return result, nil
}

func (b *queryBinder) aggregateRelation(r *qm.Aggregate) (bindingResult, error) {
	input, err := b.relation(r.Input)
	if err != nil {
		return bindingResult{}, err
	}
	aggregate := &qm.BoundAggregate{Input: input.root}
	result := bindingResult{scope: bindingScope{aliases: input.scope.aliases, groupInput: &input.scope}}
	if uint64(b.nextField)+uint64(len(r.GroupKeys))+uint64(len(r.Calls)) > qm.MaxNodes {
		return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: r.Origin, Message: "绑定输出槽数量超限"}
	}
	for _, key := range r.GroupKeys {
		expr, err := b.expr(key, &input.scope, false)
		if err != nil {
			return bindingResult{}, err
		}
		if !comparableType(expr.Type) {
			return bindingResult{}, bindFailure(key.Origin, "分组字段类型不支持")
		}
		c := qm.OutputColumn{ID: b.fieldID(), Type: expr.Type, Nullable: expr.Nullable, Lineage: expressionFields(expr), Origin: key.Origin, Encoding: outputEncoding(expr.Type)}
		col := scopeColumn{column: c, domain: expr.Domain}
		if key.Kind == qm.ExprName {
			c.Name = key.Name[len(key.Name)-1]
			col.column.Name = c.Name
			if len(key.Name) == 2 {
				col.qualifier = key.Name[0]
			} else {
				source, err := lookupColumn(&input.scope, key.Name, key.Origin)
				if err != nil {
					return bindingResult{}, err
				}
				col.qualifier = source.qualifier
			}
			duplicate := false
			for _, existing := range result.scope.columns {
				if existing.column.Name == col.column.Name && existing.qualifier == col.qualifier {
					duplicate = true
					break
				}
			}
			if !duplicate {
				result.scope.columns = append(result.scope.columns, col)
			}
		}
		result.scope.groups = append(result.scope.groups, groupBinding{source: key, column: col})
		result.schema = append(result.schema, c)
		aggregate.GroupKeys = append(aggregate.GroupKeys, qm.BoundNamedExpr{Output: c.ID, Expr: expr, Origin: key.Origin})
	}
	for _, call := range r.Calls {
		if call == nil || call.Kind != qm.ExprCall {
			return bindingResult{}, bindFailure(r.Origin, "聚合槽必须为聚合调用")
		}
		signature, ok := functions.Lookup(singleName(call.Name))
		if !ok || signature.Evaluation != qm.EvaluationAggregate {
			return bindingResult{}, bindFailure(call.Origin, "聚合槽签名无效")
		}
		expr, err := b.expr(call, &input.scope, true)
		if err != nil {
			return bindingResult{}, err
		}
		c := qm.OutputColumn{ID: b.fieldID(), Type: expr.Type, Nullable: expr.Nullable, Lineage: expressionFields(expr), Origin: call.Origin, Encoding: outputEncoding(expr.Type)}
		result.schema = append(result.schema, c)
		result.scope.aggregates = append(result.scope.aggregates, columnExpr(scopeColumn{column: c, domain: expr.Domain}, call.Origin))
		aggregate.Calls = append(aggregate.Calls, qm.BoundNamedExpr{Output: c.ID, Expr: expr, Origin: call.Origin})
	}
	aggregate.BoundRelationBase = relationBase(result.schema, r.Origin)
	result.root = aggregate
	return result, nil
}

func (b *queryBinder) sortRelation(r *qm.Sort) (bindingResult, error) {
	input, err := b.relation(r.Input)
	if err != nil {
		return bindingResult{}, err
	}
	sortScope := input.scope
	sortScope.fallback = input.sortInput
	keys := make([]qm.BoundSortKey, 0, len(r.Keys))
	visible := make(map[qm.FieldID]bool)
	for _, c := range input.schema {
		visible[c.ID] = true
	}
	for _, key := range r.Keys {
		var expr *qm.BoundExpr
		if key.Expr != nil && key.Expr.Kind == qm.ExprLiteral && key.Expr.Literal != nil && key.Expr.Literal.Kind == qm.LiteralInteger {
			position, err := strconv.Atoi(key.Expr.Literal.Text)
			if err != nil || position < 1 || position > len(input.schema) {
				return bindingResult{}, bindFailure(key.Origin, "排序位置超出投影范围")
			}
			expr = columnExpr(scopeColumn{column: input.schema[position-1], domain: qm.DomainVega}, key.Origin)
		} else {
			expr, err = b.expr(key.Expr, &sortScope, false)
			if err != nil {
				return bindingResult{}, err
			}
		}
		if !comparableType(expr.Type) {
			return bindingResult{}, bindFailure(key.Origin, "排序类型不支持")
		}
		// 非 DISTINCT 查询允许未投影排序键，作为隐藏投影穿过 Sort/Limit。
		needsHidden := false
		for _, field := range expressionFields(expr) {
			if !visible[field] {
				needsHidden = true
			}
		}
		if needsHidden {
			matched := false
			if input.project != nil {
				for _, column := range input.project.Columns {
					if sameBoundExpr(column.Expr, expr) {
						for _, c := range input.schema {
							if c.ID == column.Output {
								expr = columnExpr(scopeColumn{column: c, domain: expr.Domain}, key.Origin)
								matched = true
								break
							}
						}
						break
					}
				}
			}
			if !matched {
				if input.distinct || input.project == nil {
					return bindingResult{}, bindFailure(key.Origin, "去重后的排序表达式必须由输出字段确定")
				}
				if b.nextField >= qm.MaxNodes {
					return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: key.Origin, Message: "绑定输出槽数量超限"}
				}
				hidden := qm.OutputColumn{ID: b.fieldID(), Type: expr.Type, Nullable: expr.Nullable, Hidden: true, Lineage: expressionFields(expr), Origin: key.Origin, Encoding: outputEncoding(expr.Type)}
				input.schema = append(input.schema, hidden)
				input.project.OutputSchema = input.schema
				input.project.Columns = append(input.project.Columns, qm.BoundNamedExpr{Output: hidden.ID, Expr: expr, Origin: key.Origin})
				visible[hidden.ID] = true
				expr = columnExpr(scopeColumn{column: hidden, domain: expr.Domain}, key.Origin)
			}
		}
		bound := qm.BoundSortKey{Expr: expr, Direction: qm.SortAscending, Nulls: qm.NullsFirst, Origin: key.Origin}
		if key.Descending {
			bound.Direction = qm.SortDescending
			bound.Nulls = qm.NullsLast
		}
		keys = append(keys, bound)
	}
	input.root = &qm.BoundSort{BoundRelationBase: relationBase(input.schema, r.Origin), Input: input.root, Keys: keys}
	input.project = nil
	input.sortInput = nil
	return input, nil
}

func (b *queryBinder) trimHidden(input bindingResult) bindingResult {
	hidden := false
	for _, c := range input.schema {
		hidden = hidden || c.Hidden
	}
	if !hidden {
		return input
	}
	project := &qm.BoundProject{Input: input.root}
	var schema qm.Schema
	for _, c := range input.schema {
		if c.Hidden {
			continue
		}
		expr := columnExpr(scopeColumn{column: c, domain: qm.DomainVega}, c.Origin)
		c.Lineage = []qm.FieldID{c.ID}
		c.ID = b.fieldID()
		schema = append(schema, c)
		project.Columns = append(project.Columns, qm.BoundNamedExpr{Output: c.ID, Expr: expr, Origin: c.Origin})
	}
	project.BoundRelationBase = relationBase(schema, qm.Origin{})
	input.root = project
	input.schema = schema
	return input
}
