// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package planning

import (
	"context"
	"reflect"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type planSources struct {
	active    map[any]bool
	instances map[qm.RelationInstanceID]bool
	scans     []*qm.Scan
	nodes     int
}

// Plan 选择能够访问完整绑定查询的单个受控执行环境，不执行数据查询。
// 调用前必须完成可信 Binder 的字段、类型和授权检查；这里的树检查不替代绑定。
// 来源路由由服务层确定，Planner 将可信物理标识符填入来源节点。
// 同源只表示执行位置一致，整树算子及精度能力仍由 Connector Prepare 最终检查。
func Plan(ctx context.Context, query *qm.SelectBuilder, identity qm.ExecutionIdentity,
	resolver interfaces.SQLQuerySourceResolver) (*qm.SelectBuilder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil {
		return nil, planError(qm.ErrorInvalidTree, qm.Origin{}, "缺少绑定查询")
	}
	tree := planSources{active: make(map[any]bool), instances: make(map[qm.RelationInstanceID]bool)}
	if err := tree.visitQuery(ctx, query, 1); err != nil {
		return nil, err
	}
	if len(tree.scans) == 0 {
		return nil, planError(qm.ErrorInvalidTree, qm.Origin{}, "查询缺少来源")
	}
	catalog := tree.scans[0].Resource.CatalogID
	for _, scan := range tree.scans {
		if scan.Resource.CatalogID != catalog {
			return nil, planError(qm.ErrorPlanningUnsupported, scan.Origin, "当前不支持跨目录查询")
		}
	}
	if resolver == nil {
		return nil, planError(qm.ErrorInvalidTree, qm.Origin{}, "缺少查询目标解析器")
	}
	resolvedScans := make(map[*qm.Scan]*qm.Scan, len(tree.scans))
	for _, scan := range tree.scans {
		source := *scan
		source.Fields = slices.Clone(scan.Fields)
		resolved, err := resolver.ResolveSQLQuerySource(ctx, source, identity)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateSource(scan, resolved); err != nil {
			return nil, err
		}
		result := *scan
		result.Identifier = slices.Clone(resolved.Identifier)
		result.Fields = slices.Clone(scan.Fields)
		for i := range result.Fields {
			result.Fields[i].Identifier = slices.Clone(resolved.Fields[i].Identifier)
		}
		resolvedScans[scan] = &result
	}
	return resolveQuery(query, resolvedScans), nil
}

func planError(kind qm.ErrorKind, origin qm.Origin, message string) error {
	return &qm.CompileError{Kind: kind, Origin: origin, Message: message}
}

func (s *planSources) visit(ctx context.Context, relation qm.Relation, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// FROM 节点是封闭的指针类型集合；含 typed nil 的接口也必须拒绝。
	if relation == nil || reflect.ValueOf(relation).IsNil() {
		return planError(qm.ErrorInvalidTree, qm.Origin{}, "查询关系为空")
	}
	s.nodes++
	if s.nodes > qm.MaxNodes || depth > qm.MaxDepth {
		return planError(qm.ErrorLimit, qm.Origin{}, "绑定查询复杂度超限")
	}
	if s.active[relation] {
		return planError(qm.ErrorInvalidTree, qm.Origin{}, "绑定查询包含环")
	}
	s.active[relation] = true
	defer delete(s.active, relation)
	switch r := relation.(type) {
	case *qm.Scan:
		if r.Resource.ID == "" || r.Resource.CatalogID == "" || r.Instance == 0 || s.instances[r.Instance] {
			return planError(qm.ErrorInvalidTree, r.Origin, "来源身份缺失或重复")
		}
		s.instances[r.Instance] = true
		s.scans = append(s.scans, r)
		return nil
	case *qm.Join:
		if err := s.visit(ctx, r.Left, depth+1); err != nil {
			return err
		}
		return s.visit(ctx, r.Right, depth+1)
	case *qm.Subquery:
		return s.visitQuery(ctx, r.Query, depth+1)
	default:
		return planError(qm.ErrorInvalidTree, qm.Origin{}, "未知的 FROM 节点")
	}
}
func (s *planSources) visitQuery(ctx context.Context, query *qm.SelectBuilder, depth int) error {
	if query == nil || s.active[query] || depth > qm.MaxDepth {
		return planError(qm.ErrorInvalidTree, qm.Origin{}, "查询为空、包含环或超出深度")
	}
	s.active[query] = true
	defer delete(s.active, query)
	return s.visit(ctx, query.FromPart, depth+1)
}

// validateSource 不允许解析物理名称时改变绑定阶段已经确定的来源及字段身份。
func validateSource(scan *qm.Scan, candidate qm.Scan) error {
	invalid := func() error {
		return planError(qm.ErrorInvalidTree, scan.Origin, "物理来源不完整或与绑定来源不一致")
	}
	if candidate.Resource != scan.Resource || candidate.Instance != scan.Instance ||
		!validPhysicalIdentifier(candidate.Identifier) || len(candidate.Fields) != len(scan.Fields) {
		return invalid()
	}
	fields := make(map[qm.FieldID]bool, len(scan.Fields))
	for i, field := range candidate.Fields {
		original := scan.Fields[i]
		if field.ID == 0 || fields[field.ID] || field.ID != original.ID || field.LogicalName != original.LogicalName ||
			!reflect.DeepEqual(field.NativeType, original.NativeType) || !validPhysicalIdentifier(field.Identifier) {
			return invalid()
		}
		fields[field.ID] = true
	}
	return nil
}

func validPhysicalIdentifier(identifier qm.PhysicalIdentifier) bool {
	if len(identifier) == 0 {
		return false
	}
	for _, part := range identifier {
		if part == "" {
			return false
		}
	}
	return true
}

// resolveQuery 复制来源路径，保留输入 Builder 和已绑定表达式的只读约定。
func resolveQuery(query *qm.SelectBuilder, scans map[*qm.Scan]*qm.Scan) *qm.SelectBuilder {
	result := query.Clone()
	result.FromPart = resolveSource(query.FromPart, scans)
	return result
}

func resolveSource(source qm.Relation, scans map[*qm.Scan]*qm.Scan) qm.Relation {
	switch r := source.(type) {
	case *qm.Scan:
		return scans[r]
	case *qm.Join:
		result := *r
		result.Left = resolveSource(r.Left, scans)
		result.Right = resolveSource(r.Right, scans)
		return &result
	case *qm.Subquery:
		result := *r
		result.Query = resolveQuery(r.Query, scans)
		return &result
	default:
		return source // 来源类型及环已在解析前完成校验。
	}
}
