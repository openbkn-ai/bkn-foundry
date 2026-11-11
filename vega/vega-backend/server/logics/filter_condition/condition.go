// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var (
	// ErrMissingOperation 表示非空过滤条件缺少算子。
	ErrMissingOperation = errors.New("missing operation")
	// ErrUnsupportedOperation 表示过滤条件使用了不支持的算子。
	ErrUnsupportedOperation = errors.New("unsupported operation")
)

// ValidateFilterable 在转换或模型调用前检查显式引用字段的过滤开关。
// 全文通配字段不展开校验，字段缺失和条件格式仍由已有构造器处理。
func ValidateFilterable(cfg *interfaces.FilterCondCfg, fields map[string]*interfaces.Property) error {
	if cfg == nil {
		return nil
	}
	if err := validateFilterableFields(cfg, fields); err != nil {
		return err
	}
	for _, child := range cfg.SubConds {
		if err := ValidateFilterable(child, fields); err != nil {
			return err
		}
	}
	return nil
}

func validateFilterableFields(cfg *interfaces.FilterCondCfg, fields map[string]*interfaces.Property) error {
	// 全文通配字段沿用 OpenSearch 的字段解析，显式字段仍检查过滤开关。
	switch cfg.Operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		if names, ok := cfg.RemainCfg["fields"].([]any); ok {
			if len(names) == 1 && names[0] == interfaces.AllField {
				break
			}
			for _, item := range names {
				name, _ := item.(string)
				if err := checkFilterableField(name, cfg.Operation, fields); err != nil {
					return err
				}
			}
		} else if cfg.Field != interfaces.AllField {
			if err := checkFilterableField(cfg.Field, cfg.Operation, fields); err != nil {
				return err
			}
		}
	default:
		if err := checkFilterableField(cfg.Field, cfg.Operation, fields); err != nil {
			return err
		}
	}
	if cfg.ValueFrom == interfaces.ValueFrom_Field {
		if name, ok := cfg.Value.(string); ok {
			return checkFilterableField(name, cfg.Operation, fields)
		}
	}
	return nil
}

func checkFilterableField(name, operation string, fields map[string]*interfaces.Property) error {
	field := FilterFieldForOperation(name, operation, fields)
	if field == nil {
		return nil
	}
	if !field.EffectiveFilterable() {
		if field.Name != "" {
			name = field.Name
		}
		return interfaces.NewConditionBuildError("field %q is not filterable", name)
	}
	return nil
}

// FilterFieldForOperation 定位过滤校验的实际字段；未提供物理投影时沿用原字段开关。
// 这里只选择查询 Schema 已有的字段，不推断或改写任何过滤能力。
func FilterFieldForOperation(name, operation string, fields map[string]*interfaces.Property) *interfaces.Property {
	field, exists := fields[name]
	if !exists {
		return nil
	}
	featureType, defaultName := "", ""
	switch operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		if field.Type == interfaces.DataType_String {
			featureType, defaultName = interfaces.PropertyFeatureType_Fulltext, interfaces.LocalIndexFulltextSubfieldName
		}
	case OperationEqual, OperationEqual2, OperationNotEqual, OperationNotEqual2,
		OperationIn, OperationNotIn, OperationLike, OperationNotLike, OperationContain, OperationNotContain,
		OperationPrefix, OperationNotPrefix, OperationRegex:
		if field.Type == interfaces.DataType_Text {
			featureType, defaultName = interfaces.PropertyFeatureType_Keyword, interfaces.LocalIndexKeywordSubfieldName
		}
	case OperationKnnVector:
		if field.Type == interfaces.DataType_String || field.Type == interfaces.DataType_Text {
			featureType = interfaces.PropertyFeatureType_Vector
		}
	}
	for _, feature := range field.Features {
		if featureType == "" || feature.FeatureType != featureType {
			continue
		}
		physicalName := featureQueryFieldName(name, feature.FeatureName, defaultName)
		if featureType == interfaces.PropertyFeatureType_Vector {
			physicalName = feature.RefProperty
			if physicalName == "" {
				physicalName = name + interfaces.LocalIndexVectorFieldSuffix
			}
		}
		if target, exists := fields[physicalName]; exists {
			return target
		}
		break
	}
	return field
}

func featureQueryFieldName(parent, configured, defaultName string) string {
	name := strings.TrimSpace(configured)
	if name == "" {
		name = defaultName
	}
	if strings.HasPrefix(name, parent+".") {
		return name
	}
	return parent + "." + name
}

// wildcardFields 保持原有逻辑字段及生成向量范围，不重复展开新加入的物理多字段。
func wildcardFields(fields map[string]*interfaces.Property) []*interfaces.Property {
	physicalChildren := make(map[string]bool)
	for name, field := range fields {
		for _, feature := range field.Features {
			if field.Type == interfaces.DataType_String && feature.FeatureType == interfaces.PropertyFeatureType_Fulltext {
				physicalChildren[featureQueryFieldName(name, feature.FeatureName, interfaces.LocalIndexFulltextSubfieldName)] = true
			}
			if field.Type == interfaces.DataType_Text && feature.FeatureType == interfaces.PropertyFeatureType_Keyword {
				physicalChildren[featureQueryFieldName(name, feature.FeatureName, interfaces.LocalIndexKeywordSubfieldName)] = true
			}
		}
	}
	result := make([]*interfaces.Property, 0, len(fields))
	for name, field := range fields {
		if !physicalChildren[name] {
			result = append(result, field)
		}
	}
	return result
}

// ResolveFulltextWildcardFields 将发布范围绑定到执行条件，不改写原始通配表达。
// 固定条件与请求条件各自构造后，由调用方仅对需要发布限制的条件树调用。
func ResolveFulltextWildcardFields(condition interfaces.FilterCondition, names []string) error {
	var children []interfaces.FilterCondition
	var err error
	switch actual := condition.(type) {
	case *MatchCond:
		actual.Fields, err = resolveFulltextWildcardFields(actual.Cfg, actual.Fields, names)
	case *MatchPhraseCond:
		actual.Fields, err = resolveFulltextWildcardFields(actual.Cfg, actual.Fields, names)
	case *MultiMatchCond:
		actual.Fields, err = resolveFulltextWildcardFields(actual.Cfg, actual.Fields, names)
	case *AndCond:
		children = actual.SubConds
	case *OrCond:
		children = actual.SubConds
	case *KnnVectorCond:
		children = actual.SubConds
	}
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := ResolveFulltextWildcardFields(child, names); err != nil {
			return err
		}
	}
	return nil
}

func resolveFulltextWildcardFields(cfg *interfaces.FilterCondCfg, fields []*interfaces.Property, names []string) ([]*interfaces.Property, error) {
	if raw, present := cfg.RemainCfg["fields"]; present {
		list, ok := raw.([]any)
		if !ok || len(list) != 1 || list[0] != interfaces.AllField {
			return fields, nil
		}
	} else if cfg.Field != interfaces.AllField {
		return fields, nil
	}
	if len(names) == 0 {
		return fields, interfaces.NewConditionBuildError("fulltext wildcard has no published fields")
	}
	available := make(map[string]*interfaces.Property, len(fields))
	for _, field := range fields {
		available[field.Name] = field
	}
	resolved := make([]*interfaces.Property, 0, len(names))
	for _, name := range names {
		field, exists := available[name]
		if !exists {
			return fields, interfaces.NewConditionBuildError("fulltext wildcard field %q is not available in query schema", name)
		}
		resolved = append(resolved, field)
	}
	return resolved, nil
}

// Concatenate the filter conditions to the query section of the dsl request
func NewFilterCondition(ctx context.Context, cfg *interfaces.FilterCondCfg,
	fieldsMap map[string]*interfaces.Property) (interfaces.FilterCondition, error) {

	if cfg == nil {
		return nil, nil //nolint:nilnil // Nil result represents an expected absence condition.
	}

	// Determine whether the filter is an empty object {}
	if cfg.Field == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil, nil //nolint:nilnil // Nil result represents an expected absence condition.
	}

	if err := validateFilterableFields(cfg, fieldsMap); err != nil {
		return nil, err
	}

	condFactory, exists := OperationMap[cfg.Operation]
	if !exists {
		return nil, fmt.Errorf("unsupported operation: %s", cfg.Operation)
	}

	if err := NormalizeValueFrom(cfg); err != nil {
		return nil, err
	}

	// 先沿用算子的结构校验，再用解析后的副本构造时间条件，保留既有缺值和数组错误语义。
	cond, err := condFactory.New(ctx, cfg, fieldsMap)
	if err != nil {
		return nil, err
	}
	runtimeCfg, err := normalizeTemporalCondition(cfg, fieldsMap)
	if err != nil {
		return nil, err
	}
	if runtimeCfg != cfg {
		return condFactory.New(ctx, runtimeCfg, fieldsMap)
	}
	return cond, nil
}

// NormalizeValueFrom applies the default value source and rejects sources that
// the condition converters cannot execute. It also walks nested conditions.
func NormalizeValueFrom(cfg *interfaces.FilterCondCfg) error {
	if cfg == nil {
		return nil
	}
	if cfg.Field == "" && cfg.Operation == "" && len(cfg.SubConds) == 0 && cfg.ValueFrom == "" && cfg.Value == nil {
		return nil
	}
	if cfg.Operation == "" {
		return ErrMissingOperation
	}
	factory, ok := OperationMap[cfg.Operation]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperation, cfg.Operation)
	}
	for _, child := range cfg.SubConds {
		if err := NormalizeValueFrom(child); err != nil {
			return err
		}
	}
	if cfg.ValueFrom != "" && cfg.ValueFrom != interfaces.ValueFrom_Const && cfg.ValueFrom != interfaces.ValueFrom_Field {
		return fmt.Errorf("operation %q does not support value_from %q", cfg.Operation, cfg.ValueFrom)
	}
	if !factory.NeedValue() {
		return nil
	}
	if cfg.ValueFrom == "" {
		cfg.ValueFrom = interfaces.ValueFrom_Const
	}
	if cfg.ValueFrom != interfaces.ValueFrom_Const &&
		(factory.NeedConstValue() || cfg.ValueFrom != interfaces.ValueFrom_Field) {
		return fmt.Errorf("operation %q does not support value_from %q", cfg.Operation, cfg.ValueFrom)
	}
	return nil
}

func IsSlice(i any) bool {
	kind := reflect.ValueOf(i).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

func IsSameType(arr []any) bool {
	if len(arr) == 0 {
		return true
	}

	firstType := reflect.TypeOf(arr[0])
	for _, v := range arr {
		if reflect.TypeOf(v) != firstType {
			return false
		}
	}

	return true
}
