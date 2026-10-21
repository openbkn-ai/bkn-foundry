// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// derivedView 在调用者权限下展开源资源。固定条件留在边界内部，外部只能看到导出槽。
func (b *queryBinder) derivedView(reference *qm.Scan, view *interfaces.Resource) (bindingResult, error) {
	origin := qm.Origin{Kind: qm.OriginSavedView, ResourceID: view.ID, Path: "logic_definition"}
	if b.views[view.ID] {
		return bindingResult{}, bindFailure(origin, "视图依赖包含循环")
	}
	if len(b.views) >= interfaces.MaxRecursionDepth {
		return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "视图展开深度超限"}
	}
	if view.LogicType != interfaces.LogicType_Derived {
		return bindingResult{}, bindFailure(origin, "此逻辑视图类型尚未接入")
	}
	if err := b.enter(view, origin); err != nil {
		return bindingResult{}, err
	}
	defer b.leave(view)
	b.views[view.ID] = true
	defer delete(b.views, view.ID)
	definition, err := interfaces.DecodeDerivedLogicDefinition(view.LogicDefinition)
	if err != nil {
		return bindingResult{}, bindFailure(origin, "保存的视图定义无效")
	}
	sourceOrigin := origin
	sourceOrigin.Path += ".source_resource_id"
	input, err := b.source(definition.SourceResourceID, "", sourceOrigin)
	if err != nil {
		return bindingResult{}, err
	}
	source := b.resources[definition.SourceResourceID]
	if definition.FilterCondition != nil {
		conditionOrigin := origin
		conditionOrigin.Path += ".filter_condition"
		tree, err := FilterExpression(definition.FilterCondition, conditionOrigin)
		if err != nil {
			return bindingResult{}, interfaces.NewStoredConditionBuildError(err)
		}
		predicate, err := b.expr(tree, &input.scope, false)
		if err != nil {
			return bindingResult{}, interfaces.NewStoredConditionBuildError(err)
		}
		if predicate.Type.Kind != qm.TypeBoolean && predicate.Type.Kind != qm.TypeNull {
			return bindingResult{}, interfaces.NewStoredConditionBuildError(bindFailure(conditionOrigin, "固定条件不是布尔表达式"))
		}
		input.root = &qm.BoundFilter{BoundRelationBase: relationBase(input.schema, conditionOrigin), Input: input.root, Predicate: predicate}
	}
	if len(view.SchemaDefinition) == 0 {
		return bindingResult{}, bindFailure(origin, "视图没有导出字段")
	}
	if uint64(b.nextField)+uint64(len(view.SchemaDefinition)) > qm.MaxNodes {
		return bindingResult{}, &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "绑定输出槽数量超限"}
	}
	alias := reference.Alias
	if alias == "" {
		alias = view.ID
	}
	result := bindingResult{scope: bindingScope{aliases: map[string]bool{alias: true}}}
	mappings := make([]qm.ColumnMapping, 0, len(view.SchemaDefinition))
	names := make(map[string]bool)
	for i, output := range view.SchemaDefinition {
		at := origin
		at.Path = fmt.Sprintf("schema_definition[%d]", i)
		if output == nil || output.Name == "" || output.OriginalName == "" || names[output.Name] {
			return bindingResult{}, bindFailure(at, "视图导出字段缺失或重复")
		}
		names[output.Name] = true
		matched := -1
		for j, property := range source.SchemaDefinition {
			name := property.OriginalName
			if name == "" {
				name = property.Name
			}
			if name == output.OriginalName {
				if matched >= 0 {
					return bindingResult{}, bindFailure(at, "视图源字段映射存在歧义")
				}
				matched = j
			}
		}
		if matched < 0 {
			return bindingResult{}, bindFailure(at, "视图源字段已不存在")
		}
		property := source.SchemaDefinition[matched]
		if output.Type != property.Type || (output.OriginalType != "" && output.OriginalType != property.OriginalType) {
			return bindingResult{}, bindFailure(at, "视图源字段类型已变化")
		}
		if !validViewFeatures(output.Features, property.Features) {
			return bindingResult{}, bindFailure(at, "视图源字段特征已变化")
		}
		// input 只导出直接来源的公共列，不允许沿 lineage 穿透嵌套视图。
		col, err := lookupColumn(&input.scope, []string{property.Name}, at)
		if err != nil {
			return bindingResult{}, err
		}
		c := col.column
		old := c.ID
		c.ID = b.fieldID()
		c.Name = output.Name
		c.Lineage = []qm.FieldID{old}
		c.Origin = at
		result.schema = append(result.schema, c)
		result.scope.columns = append(result.scope.columns, scopeColumn{column: c, qualifier: alias, domain: col.domain})
		mappings = append(mappings, qm.ColumnMapping{Input: old, Output: c.ID})
	}
	result.root = &qm.BoundViewBoundary{BoundRelationBase: relationBase(result.schema, reference.Origin), Input: input.root,
		Resource: qm.ResourceReference{ID: view.ID, CatalogID: view.CatalogID, Category: view.Category, Version: strconv.FormatInt(view.UpdateTime, 10)}, Scope: b.scopeID(), Instance: reference.Instance, Columns: mappings, Authorization: qm.ViewAuthorization{Mode: qm.ViewAuthorizationInvoker}}
	return result, nil
}

func validViewFeatures(requested, source []interfaces.PropertyFeature) bool {
	used := make(map[int]bool)
	for _, feature := range requested {
		match := -1
		for i, candidate := range source {
			if candidate.FeatureType == feature.FeatureType && (feature.FeatureName == "" || feature.FeatureName == candidate.FeatureName) {
				if match >= 0 {
					return false
				}
				match = i
			}
		}
		if match < 0 || used[match] {
			return false
		}
		used[match] = true
		candidate := source[match]
		if feature.RefProperty != candidate.RefProperty || feature.IsDefault != candidate.IsDefault || feature.IsNative != candidate.IsNative || !reflect.DeepEqual(feature.Config, candidate.Config) {
			return false
		}
	}
	return true
}
