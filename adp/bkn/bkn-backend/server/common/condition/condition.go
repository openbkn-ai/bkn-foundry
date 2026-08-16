// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	dtype "bkn-backend/interfaces/data_type"
)

const MaxSubCondition = 100

// SQL string escaping.
var Special = strings.NewReplacer(`\`, `\\\\`, `'`, `\'`, `%`, `\%`, `_`, `\_`)

//go:generate mockgen -source ../condition/condition.go -destination ../condition/mock/mock_condition.go
type Condition interface {
	Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error)
	Convert2SQL(ctx context.Context) (string, error) // Convert the condition to an SQL WHERE clause.
}

// Append filter conditions to the query section of the DSL request.
func NewCondition(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*ViewField) (cond Condition, err error) {
	if cfg == nil {
		return nil, nil
	}
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

func NewCondWithOpr(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*ViewField) (cond Condition, err error) {

	// Validate all operators except multi_match.
	if cfg.Operation != OperationMultiMatch {
		// Check permissions for fields other than *.
		if cfg.Field != AllField {
			field, ok := fieldsMap[cfg.Field]
			if !ok {
				return nil, fmt.Errorf("condition config field name '%s' must in view original fields", cfg.Field)
			}

			// Binary fields do not support filtering.
			if field.Type == dtype.DATATYPE_BINARY {
				return nil, fmt.Errorf("condition config field '%s' is binary type, do not support filtering", cfg.Field)
			}

			cfg.NameField = field
		}
	}

	switch cfg.Operation {
	case OperationEq:
		cond, err = NewEqCond(ctx, cfg, fieldsMap)
	case OperationNotEq:
		cond, err = NewNotEqCond(ctx, cfg, fieldsMap)
	case OperationIn:
		cond, err = NewInCond(ctx, cfg, fieldsMap)
	case OperationNotIn:
		cond, err = NewNotInCond(ctx, cfg, fieldsMap)
	case OperationLike:
		cond, err = NewLikeCond(ctx, cfg, fieldsMap)
	case OperationNotLike:
		cond, err = NewNotLikeCond(ctx, cfg, fieldsMap)
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

	default:
		return nil, fmt.Errorf("not support condition's operation: %s", cfg.Operation)
	}
	if err != nil {
		return nil, err
	}

	return cond, nil
}

func getFilterFieldName(name string, fieldsMap map[string]*ViewField, isFullTextQuery bool) string {
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
	// Add the .keyword suffix to text fields for exact queries.
	if !isFullTextQuery && fieldInfo.Type == dtype.DATATYPE_TEXT {
		name = wrapKeyWordFieldName(name)
	}

	return name
}

// Convert to keyword.
func wrapKeyWordFieldName(fields ...string) string {
	for _, field := range fields {
		if field == "" {
			logger.Warn("missing metric name")
			return ""
		}
	}

	return strings.Join(fields, ".") + "." + dtype.KEYWORD_SUFFIX
}

// ConvertCondCfgToFilterCondition converts CondCfg to dataset filter condition format
// Reference: ontology-query's RewriteCondition pattern
func ConvertCondCfgToFilterCondition(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*ViewField,
	vectorizer func(ctx context.Context, word string) ([]*VectorResp, error)) (map[string]any, error) {
	if cfg == nil {
		return nil, nil
	}

	switch cfg.Operation {
	case OperationEq:
		return convertEqCondToDatasetFilterCondition(cfg)
	case OperationNotEq:
		return convertNotEqCondToDatasetFilterCondition(cfg)
	case OperationIn:
		return convertInCondToDatasetFilterCondition(cfg)
	case OperationNotIn:
		return convertNotInCondToDatasetFilterCondition(cfg)
	case OperationLike:
		return convertLikeCondToDatasetFilterCondition(cfg)
	case OperationNotLike:
		return convertNotLikeCondToDatasetFilterCondition(cfg)
	case OperationMatch:
		return convertMatchCondToDatasetFilterCondition(cfg, fieldsMap)
	case OperationMatchPhrase:
		return convertMatchPhraseCondToDatasetFilterCondition(cfg, fieldsMap)
	case OperationRegex:
		return convertRegexCondToDatasetFilterCondition(cfg)
	case OperationKNN:
		return convertKnnCondToDatasetFilterCondition(ctx, cfg, fieldsMap, vectorizer)
	case OperationMultiMatch:
		return convertMultiMatchCondToDatasetFilterCondition(cfg, fieldsMap)
	case OperationAnd:
		return convertAndCondToDatasetFilterCondition(ctx, cfg, fieldsMap, vectorizer)
	case OperationOr:
		return convertOrCondToDatasetFilterCondition(ctx, cfg, fieldsMap, vectorizer)
	default:
		return nil, fmt.Errorf("not support condition's operation for dataset filter: %s", cfg.Operation)
	}
}
