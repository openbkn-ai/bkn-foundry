// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"errors"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	dtype "ontology-query/interfaces/data_type"
	"ontology-query/locale"
)

const MaxSubCondition = 100

func validationError(ctx context.Context, name string, templateData map[string]any) error {
	return errors.New(locale.ValidationDetail(ctx, name, templateData))
}

// SQL string escaping.
var Special = strings.NewReplacer(`\`, `\\\\`, `'`, `\'`, `%`, `\%`, `_`, `\_`)

//go:generate mockgen -source ../condition/condition.go -destination ../condition/mock/mock_condition.go
type Condition interface {
	Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error)
	Convert2SQL(ctx context.Context) (string, error) // Convert the condition to an SQL WHERE clause.

	// RewriteCond(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (*CondCfg, error)
}

// NewCondition appends filter conditions to the query section of a DSL request.
func NewCondition(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (cond Condition, err error) {
	if cfg == nil {
		return nil, nil
	}
	cfg = PromoteLegacyLeafWithSubConds(cfg)
	switch cfg.Operation {
	case OperationAnd:
		cond, err = newAndCond(ctx, cfg, fieldScope, fieldsMap)
	case OperationOr:
		cond, err = newOrCond(ctx, cfg, fieldScope, fieldsMap)
	default:
		cond, err = NewCondWithOpr(ctx, cfg, fieldScope, fieldsMap)
	}
	if err != nil {
		return nil, err
	}

	return cond, nil
}

func NewCondWithOpr(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (cond Condition, err error) {
	// Validate all operators except multi_match.
	if cfg.Operation != OperationMultiMatch {
		// Validate fields other than *.
		if cfg.Name != AllField {
			field, ok := fieldsMap[cfg.Name]
			if !ok {
				return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": cfg.Name})
			}

			// Binary fields do not support filtering.
			if field.Type == dtype.DATATYPE_BINARY {
				return nil, validationError(ctx, "BinaryFieldUnsupported", map[string]any{"field": cfg.Name})
			}

			cfg.NameField = field
		}
	}

	switch cfg.Operation {
	case OperationEq:
		cond, err = NewEqCond(ctx, cfg, fieldsMap)
	case OperationNotEq:
		cond, err = NewNotEqCond(ctx, cfg, fieldsMap)
	case OperationGt:
		cond, err = NewGtCond(ctx, cfg, fieldsMap)
	case OperationGte:
		cond, err = NewGteCond(ctx, cfg, fieldsMap)
	case OperationLt:
		cond, err = NewLtCond(ctx, cfg, fieldsMap)
	case OperationLte:
		cond, err = NewLteCond(ctx, cfg, fieldsMap)
	case OperationIn:
		cond, err = NewInCond(ctx, cfg, fieldsMap)
	case OperationNotIn:
		cond, err = NewNotInCond(ctx, cfg, fieldsMap)
	case OperationLike:
		cond, err = NewLikeCond(ctx, cfg, fieldsMap)
	case OperationNotLike:
		cond, err = NewNotLikeCond(ctx, cfg, fieldsMap)
	case OperationRange:
		cond, err = NewRangeCond(ctx, cfg, fieldsMap)
	case OperationOutRange:
		cond, err = NewOutRangeCond(ctx, cfg, fieldsMap)
	case OperationExist:
		cond, err = NewExistCond(cfg)
	case OperationNotExist:
		cond, err = NewNotExistCond(cfg)
	case OperationRegex:
		cond, err = NewRegexCond(ctx, cfg, fieldsMap)
	case OperationMatch:
		cond, err = NewMatchCond(ctx, cfg, fieldScope, fieldsMap)
	case OperationMatchPhrase:
		cond, err = NewMatchPhraseCond(ctx, cfg, fieldScope, fieldsMap)
	case OperationKNN:
		cond, err = NewKnnCond(ctx, cfg, fieldScope, fieldsMap)
	case OperationMultiMatch:
		cond, err = NewMultiMatchCond(ctx, cfg, fieldScope, fieldsMap)
	case OperationPrefix:
		cond, err = NewPrefixCond(ctx, cfg, fieldsMap)
	case OperationNotPrefix:
		cond, err = NewNotPrefixCond(ctx, cfg, fieldsMap)
	case OperationNull:
		cond, err = NewNullCond(ctx, cfg, fieldsMap)
	case OperationNotNull:
		cond, err = NewNotNullCond(ctx, cfg, fieldsMap)
	case OperationContain:
		cond, err = NewContainCond(ctx, cfg, fieldsMap)
	case OperationNotContain:
		cond, err = NewNotContainCond(ctx, cfg, fieldsMap)
	case OperationTrue:
		cond, err = NewTrueCond(ctx, cfg, fieldsMap)
	case OperationFalse:
		cond, err = NewFalseCond(ctx, cfg, fieldsMap)
	case OperationBefore:
		cond, err = NewBeforeCond(ctx, cfg, fieldsMap)
	case OperationCurrent:
		cond, err = NewCurrentCond(ctx, cfg, fieldsMap)
	case OperationBetween:
		cond, err = NewBetweenCond(ctx, cfg, fieldsMap)

	default:
		return nil, validationError(ctx, "UnsupportedConditionOperation", map[string]any{"operation": cfg.Operation})
	}
	if err != nil {
		return nil, err
	}

	return cond, nil
}

func getFilterFieldName(name string, fieldsMap map[string]*DataProperty, isFullTextQuery bool) string {
	// Full-text search permits the * field.
	if name == AllField {
		return name
	}

	// Convert __id to the OpenSearch built-in _id field.
	if name == MetaField_ID {
		return OS_MetaField_ID
	}

	// Add the _desensitize suffix for desensitized fields.
	desensitizeFieldName := name + DESENSITIZE_FIELD_SUFFIX

	fieldInfo, ok1 := fieldsMap[name]
	_, ok2 := fieldsMap[desensitizeFieldName]
	if ok1 && ok2 {
		// Desensitized field.
		name = desensitizeFieldName
	}

	// Text fields do not need the keyword suffix for full-text search.
	// Add .keyword for exact queries on text fields with a keyword index.
	if !isFullTextQuery && ok1 &&
		fieldInfo.Type == dtype.DATATYPE_TEXT &&
		fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.KeywordConfig.Enabled {
		name = wrapKeyWordFieldName(name)
	}

	return name
}

// wrapKeyWordFieldName converts a field path to its keyword field.
func wrapKeyWordFieldName(fields ...string) string {
	for _, field := range fields {
		if field == "" {
			logger.Warn("missing metric name")
			return ""
		}
	}

	return strings.Join(fields, ".") + "." + dtype.KEYWORD_SUFFIX
}

// RewriteCondition rewrites an ontology-property condition as a data-view condition.
func RewriteCondition(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty,
	vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (viewCfg *CondCfg, err error) {

	if cfg == nil {
		return nil, nil
	}
	cfg = PromoteLegacyLeafWithSubConds(cfg)
	switch cfg.Operation {
	case OperationAnd:
		viewCfg, err = rewriteAndCondition(ctx, cfg, fieldsMap, vectorizer)
	case OperationOr:
		viewCfg, err = rewriteOrCondition(ctx, cfg, fieldsMap, vectorizer)
	default:
		viewCfg, err = rewriteCondWithOpr(ctx, cfg, fieldsMap, vectorizer)
	}
	if err != nil {
		return nil, err
	}

	return viewCfg, nil
}

// PromoteLegacyLeafWithSubConds lifts malformed condition trees where a comparison
// leaf also carries sub_conditions (Studio multi-row before and-normalization)
// into an explicit and node so rewrite recurses into every leaf.
func PromoteLegacyLeafWithSubConds(cfg *CondCfg) *CondCfg {
	if cfg == nil {
		return nil
	}
	if cfg.Operation == OperationAnd || cfg.Operation == OperationOr {
		if len(cfg.SubConds) == 0 {
			return cfg
		}
		promoted := make([]*CondCfg, 0, len(cfg.SubConds))
		for _, sub := range cfg.SubConds {
			promoted = append(promoted, PromoteLegacyLeafWithSubConds(sub))
		}
		out := *cfg
		out.SubConds = promoted
		return &out
	}
	if len(cfg.SubConds) == 0 {
		return cfg
	}

	leaf := &CondCfg{
		ObjectTypeID: cfg.ObjectTypeID,
		Name:         cfg.Name,
		Operation:    cfg.Operation,
		ValueOptCfg:  cfg.ValueOptCfg,
		RemainCfg:    cfg.RemainCfg,
		NameField:    cfg.NameField,
	}
	subs := make([]*CondCfg, 0, 1+len(cfg.SubConds))
	subs = append(subs, leaf)
	for _, sub := range cfg.SubConds {
		subs = append(subs, PromoteLegacyLeafWithSubConds(sub))
	}
	return &CondCfg{
		ObjectTypeID: cfg.ObjectTypeID,
		Operation:    OperationAnd,
		SubConds:     subs,
	}
}

func rewriteCondWithOpr(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty,
	vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (viewCfg *CondCfg, err error) {

	// Validate all operators except multi_match.
	if cfg.Operation != OperationMultiMatch {
		// Validate fields other than *.
		if cfg.Name != AllField {
			field, ok := fieldsMap[cfg.Name]
			if !ok {
				return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": cfg.Name})
			}

			// Binary fields do not support filtering.
			if field.Type == dtype.DATATYPE_BINARY {
				return nil, validationError(ctx, "BinaryFieldUnsupported", map[string]any{"field": cfg.Name})
			}

			cfg.NameField = field
			if field.MappedField.Name == "" {
				return nil, validationError(ctx, "ViewMappedFieldRequired", map[string]any{"field": cfg.Name})
			}
		}
	}

	switch cfg.Operation {
	case OperationEq:
		viewCfg, err = rewriteEqCond(ctx, cfg)
	case OperationNotEq:
		viewCfg, err = rewriteNotEqCond(ctx, cfg)
	case OperationGt:
		viewCfg, err = rewriteGtCond(ctx, cfg)
	case OperationGte:
		viewCfg, err = rewriteGteCond(ctx, cfg)
	case OperationLt:
		viewCfg, err = rewriteLtCond(ctx, cfg)
	case OperationLte:
		viewCfg, err = rewriteLteCond(ctx, cfg)
	case OperationIn:
		viewCfg, err = rewriteInCond(ctx, cfg)
	case OperationNotIn:
		viewCfg, err = rewriteNotInCond(ctx, cfg)
	case OperationLike:
		viewCfg, err = rewriteLikeCond(ctx, cfg)
	case OperationNotLike:
		viewCfg, err = rewriteNotLikeCond(ctx, cfg)
	case OperationRange:
		viewCfg, err = rewriteRangeCond(ctx, cfg)
	case OperationOutRange:
		viewCfg, err = rewriteOutRangeCond(ctx, cfg)
	case OperationExist:
		viewCfg, err = rewriteExistCond(ctx, cfg)
	case OperationNotExist:
		viewCfg, err = rewriteNotExistCond(ctx, cfg)
	case OperationRegex:
		viewCfg, err = rewriteRegexCond(ctx, cfg)
	case OperationMatch:
		viewCfg, err = rewriteMatchCond(ctx, cfg)
	case OperationMatchPhrase:
		viewCfg, err = rewriteMatchPhraseCond(ctx, cfg)
	case OperationKNN:
		viewCfg, err = rewriteKnnCond(ctx, cfg, vectorizer)
	case OperationMultiMatch:
		viewCfg, err = rewriteMultiMatchCond(ctx, cfg, fieldsMap)
	case OperationPrefix:
		viewCfg, err = rewritePrefixCond(ctx, cfg)
	case OperationNotPrefix:
		viewCfg, err = rewriteNotPrefixCond(ctx, cfg)
	case OperationNull:
		viewCfg, err = rewriteNullCond(ctx, cfg)
	case OperationNotNull:
		viewCfg, err = rewriteNotNullCond(ctx, cfg)
	case OperationContain:
		viewCfg, err = rewriteContainCond(ctx, cfg)
	case OperationNotContain:
		viewCfg, err = rewriteNotContainCond(ctx, cfg)
	case OperationTrue:
		viewCfg, err = rewriteTrueCond(ctx, cfg)
	case OperationFalse:
		viewCfg, err = rewriteFalseCond(ctx, cfg)
	case OperationBefore:
		viewCfg, err = rewriteBeforeCond(ctx, cfg)
	case OperationCurrent:
		viewCfg, err = rewriteCurrentCond(ctx, cfg)
	case OperationBetween:
		viewCfg, err = rewriteBetweenCond(ctx, cfg)
	case OperationEmpty:
		viewCfg, err = rewriteEmptyCond(ctx, cfg)
	case OperationNotEmpty:
		viewCfg, err = rewriteNotEmptyCond(ctx, cfg)
	default:
		return nil, validationError(ctx, "UnsupportedConditionOperation", map[string]any{"operation": cfg.Operation})
	}
	if err != nil {
		return nil, err
	}

	return viewCfg, nil
}
