// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// BuildResourceDataQuery 不经过 SQL 文本或 Query Tree，不读取资源、权限和数据源。
// API 分页保留在原请求，由查询会话处理；output_fields 裁剪在完整聚合及排序关系之后进行。
func BuildResourceDataQuery(params *interfaces.ResourceDataQueryParams, id string) (*qm.SelectBuilder, error) {
	origin := qm.Origin{Kind: qm.OriginDTO}
	fail := func(p, message string) (*qm.SelectBuilder, error) {
		return nil, invalid(path(origin, p), message)
	}
	if params == nil || !resourceID.MatchString(id) {
		return fail("resource_id", "结构化查询或资源 ID 无效")
	}
	if params.FilterCondCfg != nil || params.FixedFilterCondCfg != nil || params.ActualFilterCond != nil || params.FixedActualFilterCond != nil || len(params.SearchAfter) > 0 || params.CursorEncoded != "" || params.QueryType != "" {
		return fail("", "不接受内部编译条件或执行状态")
	}
	if params.Paging.Cursor != "" {
		return fail("paging.cursor", "续页必须通过查询会话恢复")
	}
	if len(params.OutputFields)+len(params.GroupBy)+len(params.Sort) > qm.MaxNodes {
		return fail("", "结构化查询字段数量超限")
	}
	paging := params.Paging.Normalized()
	if (paging.Mode != interfaces.PagingModeSingle && paging.Mode != interfaces.PagingModeCursor) || paging.Offset < 0 || paging.Limit < 1 || paging.Limit > interfaces.MaxPageLimit || params.Timeout < 0 {
		return fail("paging", "分页或超时参数无效")
	}
	if paging.Mode == interfaces.PagingModeCursor && (paging.Offset != 0 || (paging.KeepAliveSec != 0 && (paging.KeepAliveSec < interfaces.MinCursorKeepAliveSec || paging.KeepAliveSec > interfaces.MaxCursorKeepAliveSec))) {
		return fail("paging", "cursor 分页参数无效")
	}
	if paging.Mode == interfaces.PagingModeSingle && paging.KeepAliveSec != 0 {
		return fail("paging.keep_alive_sec", "single 分页不接受 keep_alive_sec")
	}
	if params.BinaryMode != nil && *params.BinaryMode != interfaces.BinaryModeMetadata && *params.BinaryMode != interfaces.BinaryModeContent {
		return fail("binary_mode", "binary_mode 无效")
	}
	parts := &qm.SelectBuilder{FromPart: &qm.Scan{ResourceID: id, Origin: path(origin, "resource_id")}}
	if params.FilterCondition != nil {
		predicate, err := ConvertFilterCondition(params.FilterCondition, path(origin, "filter_condition"))
		if err != nil {
			return nil, err
		}
		parts.WherePart = []*qm.Sqlizer{predicate}
	}
	var complete []qm.NamedExpr
	aggregate := params.Aggregation
	implicitCount := aggregate == nil && params.Having != nil && params.Having.Field == "count(*)"
	if implicitCount {
		aggregate = &interfaces.Aggregation{Aggr: "count", Property: "*"}
	}
	if aggregate != nil || len(params.GroupBy) > 0 {
		var aggregateExpr *qm.Sqlizer
		names := make(map[string]bool)
		for i, item := range params.GroupBy {
			at := path(origin, fmt.Sprintf("group_by[%d]", i))
			if item == nil || item.Property == "" || names[item.Property] {
				return fail(at.Path, "分组字段缺失或重复")
			}
			if item.CalendarInterval != "" {
				return fail(at.Path, "日历分桶尚未接入 IR")
			}
			names[item.Property] = true
			e := field(item.Property, at)
			parts.GroupByPart = append(parts.GroupByPart, e)
			complete = append(complete, qm.NamedExpr{Expr: field(item.Property, at), Alias: item.Property, Origin: at})
		}
		alias := ""
		if aggregate != nil {
			alias = aggregate.Alias
			if alias == "" {
				alias = "__value"
			}
			if names[alias] {
				return fail("aggregation.alias", "聚合别名与分组输出冲突")
			}
			name := strings.ToUpper(aggregate.Aggr)
			distinct := name == "COUNT_DISTINCT"
			if distinct {
				name = "COUNT"
			}
			switch name {
			case "COUNT", "SUM", "AVG", "MIN", "MAX":
			default:
				return fail("aggregation.aggr", "聚合函数尚未支持")
			}
			property := aggregate.Property
			if property == "" && name == "COUNT" {
				property = "*"
			}
			var arg *qm.Sqlizer
			if property == "*" {
				if name != "COUNT" || distinct {
					return fail("aggregation.property", "此聚合不接受通配符")
				}
				arg = &qm.Sqlizer{Kind: qm.ExprWildcard, Origin: path(origin, "aggregation.property")}
			} else if property != "" {
				arg = field(property, path(origin, "aggregation.property"))
			} else {
				return fail("aggregation.property", "缺少聚合字段")
			}
			aggregateExpr = &qm.Sqlizer{Kind: qm.ExprCall, Name: []string{name}, Args: []*qm.Sqlizer{arg}, Distinct: distinct, Origin: path(origin, "aggregation")}
			complete = append(complete, qm.NamedExpr{Expr: aggregateExpr, Alias: alias, Origin: path(origin, "aggregation.alias")})
		}
		if params.Having != nil {
			switch params.Having.Operation {
			case "==", "!=", ">", ">=", "<", "<=", "in", "not_in", "range", "out_range":
			default:
				return fail("having.operation", "HAVING 算子未开放")
			}
			validHavingField := params.Having.Field == "__value" || params.Having.Field == alias || (implicitCount && params.Having.Field == "count(*)")
			if aggregate == nil || !validHavingField {
				return fail("having.field", "HAVING 必须引用实际聚合输出")
			}
			raw := map[string]any{"field": params.Having.Field, "operation": params.Having.Operation, "value": params.Having.Value}
			predicate, err := ConvertFilterCondition(raw, path(origin, "having"))
			if err != nil {
				return nil, err
			}
			// HAVING 比较实际聚合槽，不能按同名源字段再次绑定。
			rewriteHaving(predicate, params.Having.Field, aggregateExpr)
			parts.HavingPart = []*qm.Sqlizer{predicate}
		}
		parts.ColumnsPart = complete
	} else if params.Having != nil {
		return fail("having", "HAVING 缺少聚合查询")
	}
	// 明细先保留完整来源作用域，供未输出字段的排序使用；响应列最后裁剪。
	if len(complete) == 0 {
		parts.ColumnsPart = []qm.NamedExpr{{Expr: &qm.Sqlizer{Kind: qm.ExprWildcard, Origin: path(origin, "output_fields")}, Origin: path(origin, "output_fields")}}
	}
	if len(params.Sort) > 0 {

		for i, item := range params.Sort {
			if item == nil || item.Field == "" {
				return fail("sort", "排序字段无效")
			}
			direction := strings.ToLower(item.Direction)
			if direction == "" {
				direction = "asc"
			}
			if direction != "asc" && direction != "desc" {
				return fail("sort", "排序方向无效")
			}
			at := path(origin, fmt.Sprintf("sort[%d]", i))
			parts.OrderByPart = append(parts.OrderByPart, qm.SortKey{Expr: field(item.Field, at), Descending: direction == "desc", Origin: at})
		}
	}
	if len(params.OutputFields) > 0 {
		var columns []qm.NamedExpr
		names := make(map[string]bool)
		for i, name := range params.OutputFields {
			if name == "" || names[name] {
				return fail("output_fields", "输出字段为空或重复")
			}
			names[name] = true
			at := path(origin, fmt.Sprintf("output_fields[%d]", i))
			columns = append(columns, qm.NamedExpr{Expr: field(name, at), Alias: name, Origin: at})
		}
		if len(complete) > 0 {
			inner := parts
			parts = &qm.SelectBuilder{FromPart: &qm.Subquery{Query: inner, Alias: "_data", Origin: origin}, ColumnsPart: columns, OrderByPart: parts.OrderByPart}
			innerParts := inner.Clone()
			innerParts.OrderByPart = nil
			parts.FromPart.(*qm.Subquery).Query = innerParts
		} else {
			parts.ColumnsPart = columns
		}
	}
	return parts, nil
}

func rewriteHaving(expr *qm.Sqlizer, name string, aggregate *qm.Sqlizer) {
	if expr.Kind == qm.ExprName && len(expr.Name) == 1 && expr.Name[0] == name {
		origin := expr.Origin
		*expr = *aggregate
		expr.Origin = origin
		return
	}
	for _, arg := range expr.Args {
		rewriteHaving(arg, name, aggregate)
	}
}
