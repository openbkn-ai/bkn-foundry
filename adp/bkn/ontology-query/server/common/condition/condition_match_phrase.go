// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
)

type MatchPhraseCond struct {
	mCfg              *CondCfg
	mFilterFieldNames []string
}

func NewMatchPhraseCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	name := getFilterFieldName(cfg.Name, fieldsMap, true)
	var fields []string
	// When querying * against a view with a partial field scope, replace it with the view field list.
	if name == AllField {
		// fields = make([]string, 0, len(fieldsMap))
		// for fieldName := range fieldsMap {
		// 	fields = append(fields, fieldName)
		// }
		// * 只针对配了全文索引的属性
		for _, fieldInfo := range fieldsMap {
			if fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.FulltextConfig.Enabled {
				// 配置了全文索引的属性,可以做match查询,否则报错,不能进行match查询
				fields = append(fields, name)
			}
		}
	} else {
		// 字段是否做了全文索引
		fieldInfo := fieldsMap[name]
		if fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.FulltextConfig.Enabled {
			// 配置了全文索引的属性,可以做match查询,否则报错,不能进行match查询
			fields = append(fields, name)
		} else {
			return nil, fmt.Errorf(`the index of property [%s] is not configured for full-text search and cannot be used for [match_phrase] filtering. Please check the index configuration of the object type and the current request`, name)
		}
	}

	return &MatchPhraseCond{
		mCfg:              cfg,
		mFilterFieldNames: fields,
	}, nil
}

func (cond *MatchPhraseCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf("%q", vStr)
	}

	fields, err := sonic.Marshal(cond.mFilterFieldNames)
	if err != nil {
		return "", fmt.Errorf("condition [match_phrase] marshal fields error: %s", err.Error())
	}

	dslStr := fmt.Sprintf(`
					{
						"multi_match": {
							"query": %v,
							"type": "phrase",
							"fields": %v
						}
					}`, v, string(fields))

	return dslStr, nil
}

func (cond *MatchPhraseCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

func rewriteMatchPhraseCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// 过滤条件中的属性字段换成映射的视图字段
	fieldName := ""
	if cfg.Name == AllField {
		fieldName = AllField
	} else {
		if cfg.NameField.Name == "" {
			return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "match_phrase", "field": cfg.Name})
		}
		fieldName = cfg.NameField.MappedField.Name
	}

	return &CondCfg{
		Name:        fieldName,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
