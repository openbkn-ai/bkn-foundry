// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	dtype "ontology-query/interfaces/data_type"

	"github.com/bytedance/sonic"
)

type MatchCond struct {
	mCfg              *CondCfg
	mFilterFieldNames []string
}

func NewMatchCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	name := getFilterFieldName(cfg.Name, fieldsMap, true)
	var fields []string
	// When querying * against a view with a partial field scope, replace it with the view field list.
	if name == AllField {
		// * Run full-text search only on text fields and properties with a full-text index.
		for _, fieldInfo := range fieldsMap {
			fields = append(fields, fieldInfo.Name)
			// If it is a string type with a full-text index, the field name is fieldInfo.Name + "." + dtype.TEXT_SUFFIX.
			if fieldInfo.Type == dtype.DATATYPE_STRING &&
				fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.FulltextConfig.Enabled {
				fields = append(fields, fieldInfo.Name+"."+dtype.TEXT_SUFFIX)
			}
		}
	} else {
		// Whether the field has a full-text index.
		fieldInfo := fieldsMap[name]
		if fieldInfo.Type == dtype.DATATYPE_TEXT {
			// Append text fields directly.
			fields = append(fields, name)
		} else {
			if fieldInfo.Type == dtype.DATATYPE_STRING &&
				fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.FulltextConfig.Enabled {
				// match queries are allowed only for properties with a full-text index; otherwise return an error.
				// For string fields with full-text enabled, match filters on xxx.text.
				fields = append(fields, name+"."+dtype.TEXT_SUFFIX)
			} else {
				return nil, fmt.Errorf(`the index of property [%s] is not configured for full-text search and cannot be used for [match] filtering. Please check the index configuration of the object type and the current request`, name)
			}
		}
	}

	return &MatchCond{
		mCfg:              cfg,
		mFilterFieldNames: fields,
	}, nil
}

func (cond *MatchCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf("%q", vStr)
	}

	fields, err := sonic.Marshal(cond.mFilterFieldNames)
	if err != nil {
		return "", fmt.Errorf("condition [match] marshal fields error: %s", err.Error())
	}

	dslStr := fmt.Sprintf(`
					{
						"multi_match": {
							"query": %v,
							"type": "best_fields",
							"fields": %v
						}
					}`, v, string(fields))

	return dslStr, nil
}

func (cond *MatchCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

func rewriteMatchCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	fieldName := ""
	if cfg.Name == AllField {
		fieldName = AllField
	} else {
		if cfg.NameField.Name == "" {
			return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "match", "field": cfg.Name})
		}
		fieldName = cfg.NameField.MappedField.Name
	}

	return &CondCfg{
		Name:        fieldName,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
