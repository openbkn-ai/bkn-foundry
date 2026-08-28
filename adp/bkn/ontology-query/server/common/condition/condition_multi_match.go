// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"ontology-query/common"
	dtype "ontology-query/interfaces/data_type"

	"github.com/bytedance/sonic"
)

type MultiMatchCond struct {
	mCfg              *CondCfg
	mFilterFieldNames []string
}

func NewMultiMatchCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	// Read the multi_match fields array from RemainCfg. Omit it or pass ["*"] to match all fields.
	var fields []string
	cfgFields, exist := cfg.RemainCfg["fields"]
	if exist {
		// fields must be an array when present.
		if !common.IsSlice(cfgFields) {
			return nil, validationError(ctx, "MultiMatchFieldsArray", nil)
		}
		// Every fields item must be a string.
		for _, cfgField := range cfgFields.([]any) {
			field, ok := cfgField.(string)
			if !ok {
				return nil, validationError(ctx, "MultiMatchFieldsStringArray", map[string]any{"value": cfgField})
			}

			if field == AllField {
				expanded, err := expandIndexFieldNamesForMultiMatchStar(ctx, fieldsMap)
				if err != nil {
					return nil, err
				}
				fields = append(fields, expanded...)
				continue
			}

			fieldInfo := fieldsMap[field]
			if fieldInfo == nil {
				return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": field})
			}
			name := getFilterFieldName(field, fieldsMap, true)

			if fieldInfo.Type == dtype.DATATYPE_TEXT {
				fields = append(fields, name)
				continue
			}
			if fieldInfo.Type == dtype.DATATYPE_STRING {
				fields = append(fields, name+"."+dtype.TEXT_SUFFIX)
				continue
			}
			return nil, validationError(ctx, "MultiMatchPropertyNotFullText", map[string]any{"field": field})
		}
	}

	// Validate optional match_type.
	matchType, exist := cfg.RemainCfg["match_type"]
	if exist && matchType != "" {
		mtype, ok := matchType.(string)
		if !ok {
			return nil, validationError(ctx, "MultiMatchTypeString", map[string]any{"value": matchType})
		}
		if !MatchTypeMap[mtype] {
			return nil, validationError(ctx, "MultiMatchTypeInvalid", map[string]any{"value": mtype})
		}
	}

	return &MultiMatchCond{
		mCfg:              cfg,
		mFilterFieldNames: fields,
	}, nil
}

func (cond *MultiMatchCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf("%q", vStr)
	}

	fields, err := sonic.Marshal(cond.mFilterFieldNames)
	if err != nil {
		return "", validationError(ctx, "MultiMatchFieldsMarshalFailed", nil)
	}

	// Default to best_fields.
	matchType := "best_fields"
	if mt, ok := cond.mCfg.RemainCfg["match_type"]; ok {
		if mtStr, ok := mt.(string); ok {
			matchType = mtStr
		} else {
			return "", validationError(ctx, "MultiMatchTypeString", map[string]any{"value": mt})
		}
	}

	dslStr := fmt.Sprintf(`
					{
						"multi_match": {
							"query": %v,
							"type": "%s"`, v, matchType)

	// When fields is omitted, query index.query.default_field, which defaults to *.
	if len(cond.mFilterFieldNames) > 0 {
		dslStr = fmt.Sprintf(`%s,
							"fields": %v
						}
					}`, dslStr, string(fields))
	} else {
		dslStr = fmt.Sprintf(`%s
						}
					}`, dslStr)
	}

	return dslStr, nil
}

func (cond *MultiMatchCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

func rewriteMultiMatchCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (*CondCfg, error) {

	// Replace object-type properties with mapped data-view fields.
	var fields []string
	cfgFields, exist := cfg.RemainCfg["fields"]
	if exist {
		// fields must be an array when present.
		if !common.IsSlice(cfgFields) {
			return nil, validationError(ctx, "MultiMatchFieldsArray", nil)
		}
		// Every fields item must be a string.
		for _, cfgField := range cfgFields.([]any) {
			field, ok := cfgField.(string)
			if !ok {
				return nil, validationError(ctx, "MultiMatchFieldsStringArray", map[string]any{"value": cfgField})
			}

			if field == AllField {
				expanded, err := expandViewFieldNamesForMultiMatchStar(ctx, fieldsMap)
				if err != nil {
					return nil, err
				}
				fields = append(fields, expanded...)
				continue
			}

			fieldInfo, ok1 := fieldsMap[field]
			if !ok1 || fieldInfo == nil {
				return nil, validationError(ctx, "ConditionFieldNotFound", map[string]any{"field": field})
			}
			if fieldInfo.MappedField.Name == "" {
				return nil, validationError(ctx, "ViewMappedFieldRequired", map[string]any{"field": field})
			}

			if fieldInfo.Type == dtype.DATATYPE_TEXT {
				fields = append(fields, fieldInfo.MappedField.Name)
				continue
			}
			if fieldInfo.Type == dtype.DATATYPE_STRING {
				fields = append(fields, fieldInfo.MappedField.Name+"."+dtype.TEXT_SUFFIX)
				continue
			}
			return nil, validationError(ctx, "MultiMatchViewPropertyNotFullText", map[string]any{"field": field})
		}
	}

	// Validate optional match_type.
	matchType, exist := cfg.RemainCfg["match_type"]
	if exist && matchType != "" {
		mtype, ok := matchType.(string)
		if !ok {
			return nil, validationError(ctx, "MultiMatchTypeString", map[string]any{"value": matchType})
		}
		if !MatchTypeMap[mtype] {
			return nil, validationError(ctx, "MultiMatchTypeInvalid", map[string]any{"value": mtype})
		}
	}

	return &CondCfg{
		RemainCfg: map[string]any{
			"fields":     fields,
			"match_type": matchType,
		},
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}

// expandIndexFieldNamesForMultiMatchStar resolves ["*"] into OpenSearch field names for text and
// fulltext-enabled string properties (uses .text subfield for the latter).
func expandIndexFieldNamesForMultiMatchStar(ctx context.Context, fieldsMap map[string]*DataProperty) ([]string, error) {
	var out []string
	for _, fieldInfo := range fieldsMap {
		if fieldInfo == nil {
			continue
		}
		if fieldInfo.Type == dtype.DATATYPE_TEXT {
			out = append(out, getFilterFieldName(fieldInfo.Name, fieldsMap, true))
			continue
		}
		if fieldInfo.Type == dtype.DATATYPE_STRING {
			base := getFilterFieldName(fieldInfo.Name, fieldsMap, true)
			out = append(out, base+"."+dtype.TEXT_SUFFIX)
		}
	}
	if len(out) == 0 {
		return nil, validationError(ctx, "MultiMatchFullTextPropertyRequired", nil)
	}
	return out, nil
}

// expandViewFieldNamesForMultiMatchStar resolves ["*"] into view column names (MappedField.Name)
// for the same property kinds as the index path, skipping properties without mapped_field.
func expandViewFieldNamesForMultiMatchStar(ctx context.Context, fieldsMap map[string]*DataProperty) ([]string, error) {
	var out []string
	for _, fieldInfo := range fieldsMap {
		if fieldInfo == nil || fieldInfo.MappedField.Name == "" {
			continue
		}
		if fieldInfo.Type == dtype.DATATYPE_TEXT {
			out = append(out, fieldInfo.MappedField.Name)
			continue
		}
		if fieldInfo.Type == dtype.DATATYPE_STRING {
			out = append(out, fieldInfo.MappedField.Name+"."+dtype.TEXT_SUFFIX)
		}
	}
	if len(out) == 0 {
		return nil, validationError(ctx, "MultiMatchMappedFieldRequired", nil)
	}
	return out, nil
}
