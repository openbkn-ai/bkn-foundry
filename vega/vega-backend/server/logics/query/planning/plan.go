// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// Plan 选择能够访问完整绑定查询的单个受控执行环境，不执行数据查询。
// 调用前必须完成可信 Binder 的字段、类型和授权检查；这里的树检查不替代绑定。
// 未指定来源时只访问源端；显式 auto/index 由结构化查询入口按既有路由规则传入。
// 同源只表示执行位置一致，整树算子及精度能力仍由 Connector Prepare 最终检查。
func Plan(ctx context.Context, query *qm.BoundQuery, identity qm.ExecutionIdentity,
	options qm.ExecutionOptions, resolver interfaces.SQLQueryTargetResolver) (*qm.ExecutionPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil {
		return nil, planError(qm.ErrorInvalidTree, qm.Origin{}, "缺少绑定查询")
	}
	tree := planSources{active: make(map[qm.BoundRelation]bool), instances: make(map[qm.RelationInstanceID]bool)}
	if err := tree.visit(ctx, query.Root, 1); err != nil {
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
	if options.QuerySource == "" {
		options.QuerySource = qm.QuerySourceSource
	}
	switch options.QuerySource {
	case qm.QuerySourceSource, qm.QuerySourceIndex, qm.QuerySourceAuto:
	default:
		return nil, planError(qm.ErrorPlanningUnsupported, qm.Origin{}, "不支持的查询来源")
	}
	if options.IgnoreLocalIndex && options.QuerySource == qm.QuerySourceIndex {
		return nil, planError(qm.ErrorPlanningUnsupported, qm.Origin{}, "查询来源与忽略本地索引选项冲突")
	}

	// 对候选执行环境取交集，不枚举候选组合，也不产生部分下推计划。
	var choices []qm.TargetBinding
	totalCandidates := 0
	for i, scan := range tree.scans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates, err := resolver.ResolveSQLQueryTargets(ctx, *scan, identity, options)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		totalCandidates += len(candidates)
		if totalCandidates > qm.MaxNodes {
			return nil, planError(qm.ErrorLimit, scan.Origin, "查询目标候选数量超限")
		}
		eligible := make([]qm.TargetBinding, 0, len(candidates))
		for _, candidate := range candidates {
			if err := validateTargetBinding(scan, candidate); err != nil {
				return nil, err
			}
			if options.QuerySource != qm.QuerySourceAuto && candidate.Source != options.QuerySource {
				continue
			}
			if options.IgnoreLocalIndex && candidate.Source == qm.QuerySourceIndex {
				continue
			}
			eligible = append(eligible, candidate)
		}
		if i == 0 {
			for _, candidate := range eligible {
				choices = append(choices, cloneTargetBinding(candidate))
			}
		} else {
			matched := choices[:0]
			for _, choice := range choices {
				for _, candidate := range eligible {
					if sameExecutionTarget(choice, candidate) {
						copy := cloneTargetBinding(candidate)
						choice.Relations = append(choice.Relations, copy.Relations...)
						choice.Fields = append(choice.Fields, copy.Fields...)
						matched = append(matched, choice)
						break
					}
				}
			}
			choices = matched
		}
		if len(choices) == 0 {
			return nil, planError(qm.ErrorPlanningUnsupported, scan.Origin, "完整查询没有共同可用的单源执行目标")
		}
	}
	selected := choices[0]
	// 指纹在合并后重新计算，不能借用单个 Scan 的目标制品指纹。
	selected.Fingerprint = ""
	encoded, err := json.Marshal(selected)
	if err != nil {
		return nil, planError(qm.ErrorInvalidTree, qm.Origin{}, "执行目标元数据无法编码")
	}
	fingerprint := sha256.Sum256(encoded)
	selected.Fingerprint = hex.EncodeToString(fingerprint[:])
	return &qm.ExecutionPlan{Query: query, TargetBinding: selected}, nil
}

func planError(kind qm.ErrorKind, origin qm.Origin, message string) error {
	return &qm.CompileError{Kind: kind, Origin: origin, Message: message}
}

type planSources struct {
	active    map[qm.BoundRelation]bool
	instances map[qm.RelationInstanceID]bool
	scans     []*qm.BoundScan
	nodes     int
}

func (s *planSources) visit(ctx context.Context, relation qm.BoundRelation, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// BoundRelation 是封闭的指针类型集合；含 typed nil 的接口也必须拒绝。
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
	var input qm.BoundRelation
	switch r := relation.(type) {
	case *qm.BoundScan:
		if r.Resource.ID == "" || r.Resource.CatalogID == "" || r.Instance == 0 || s.instances[r.Instance] {
			return planError(qm.ErrorInvalidTree, r.Origin, "来源身份缺失或重复")
		}
		s.instances[r.Instance] = true
		s.scans = append(s.scans, r)
		return nil
	case *qm.BoundJoin:
		if err := s.visit(ctx, r.Left, depth+1); err != nil {
			return err
		}
		return s.visit(ctx, r.Right, depth+1)
	case *qm.BoundFilter:
		input = r.Input
	case *qm.BoundAggregate:
		input = r.Input
	case *qm.BoundProject:
		input = r.Input
	case *qm.BoundDistinct:
		input = r.Input
	case *qm.BoundSort:
		input = r.Input
	case *qm.BoundLimit:
		input = r.Input
	case *qm.BoundSubquery:
		input = r.Input
	case *qm.BoundViewBoundary:
		input = r.Input
	case *qm.BoundSearch:
		input = r.Input
	default:
		return planError(qm.ErrorInvalidTree, qm.Origin{}, "未知的绑定关系")
	}
	return s.visit(ctx, input, depth+1)
}

func validateTargetBinding(scan *qm.BoundScan, candidate qm.TargetBinding) error {
	invalid := func() error {
		return planError(qm.ErrorInvalidTree, scan.Origin, "查询目标映射不完整或与来源不一致")
	}
	target := candidate.Target
	if target.CatalogID != scan.Resource.CatalogID || target.ConnectorType == "" || target.PhysicalTargetKey == "" ||
		target.ConnectionProfile == "" || target.EffectiveSourceIdentity == "" || target.AuthorizationNamespace == "" ||
		target.SchemaRevision == "" || target.ConfigRevision == "" {
		return invalid()
	}
	if candidate.Source != qm.QuerySourceSource && candidate.Source != qm.QuerySourceIndex {
		return invalid()
	}
	if len(candidate.Relations) != 1 {
		return invalid()
	}
	relation := candidate.Relations[0]
	if relation.Instance != scan.Instance || relation.ResourceID != scan.Resource.ID || !validPhysicalIdentifier(relation.Identifier) {
		return invalid()
	}
	fields := make(map[qm.FieldID]bool, len(scan.Fields))
	for _, field := range scan.Fields {
		if field.ID == 0 || fields[field.ID] {
			return invalid()
		}
		fields[field.ID] = true
	}
	if len(candidate.Fields) != len(fields) {
		return invalid()
	}
	for _, field := range candidate.Fields {
		if field.Address.Relation != scan.Instance || !fields[field.Address.Field] || !validPhysicalIdentifier(field.Identifier) {
			return invalid()
		}
		if field.MissingValue != "" && field.MissingValue != qm.MissingAsNull && field.MissingValue != qm.MissingDistinct {
			return invalid()
		}
		delete(fields, field.Address.Field)
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

func sameExecutionTarget(a, b qm.TargetBinding) bool {
	if a.Target != b.Target || a.Source != b.Source || a.Freshness.Revision != b.Freshness.Revision {
		return false
	}
	if a.Freshness.AsOf == nil || b.Freshness.AsOf == nil {
		return a.Freshness.AsOf == b.Freshness.AsOf
	}
	return a.Freshness.AsOf.Equal(*b.Freshness.AsOf)
}

func cloneTargetBinding(binding qm.TargetBinding) qm.TargetBinding {
	binding.Relations = slices.Clone(binding.Relations)
	for i := range binding.Relations {
		binding.Relations[i].Identifier = slices.Clone(binding.Relations[i].Identifier)
	}
	binding.Fields = slices.Clone(binding.Fields)
	for i := range binding.Fields {
		field := &binding.Fields[i]
		field.Identifier = slices.Clone(field.Identifier)
		field.NativeType.Precision = cloneInt(field.NativeType.Precision)
		field.NativeType.Scale = cloneInt(field.NativeType.Scale)
		field.NativeType.TimePrecision = cloneInt(field.NativeType.TimePrecision)
	}
	if binding.Freshness.AsOf != nil {
		value := *binding.Freshness.AsOf
		binding.Freshness.AsOf = &value
	}
	return binding
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
