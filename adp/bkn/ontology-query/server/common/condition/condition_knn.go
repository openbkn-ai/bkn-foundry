// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"encoding/json"
	"fmt"
	dtype "ontology-query/interfaces/data_type"
)

type KnnCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
	mSubConds        []Condition
}

func NewKnnCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*DataProperty) (Condition, error) {

	// 校验名称是否存在
	name := getFilterFieldName(cfg.Name, fieldsMap, true)
	var field string
	// 如果指定*查询,报错，不支持，因为字段太多，向量耗时太长
	if name == AllField {
		return nil, fmt.Errorf(`the knn operation does not support the [*] query, please specify the field name explicitly`)
	} else {
		// 向量字段做knn查询时需要把向量字段换成 "_vector_"+property.Name
		// 字段是否做了knn
		fieldInfo := fieldsMap[name]
		if fieldInfo.IndexConfig != nil && fieldInfo.IndexConfig.VectorConfig.Enabled {
			// 配置了向量化的属性,可以做向量化查询,否则报错,不能进行向量化查询
			field = "_vector_" + name
		} else {
			return nil, fmt.Errorf(`the index of property [%s] is not configured for vectorization and cannot be used for [knn] filtering. Please check the index configuration of the object type and the current request`, name)
		}
	}

	subConds := []Condition{}
	for _, subCond := range cfg.SubConds {
		cond, err := NewCondition(ctx, subCond, fieldScope, fieldsMap)
		if err != nil {
			return nil, err
		}

		if cond != nil {
			subConds = append(subConds, cond)
		}
	}

	return &KnnCond{
		mCfg:             cfg,
		mFilterFieldName: field,
		mSubConds:        subConds,
	}, nil
}

func (cond *KnnCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	v := fmt.Sprintf("%v", cond.mCfg.Value)

	vector, err := vectorizer(ctx, cond.mCfg.NameField, v)
	if err != nil {
		return "", fmt.Errorf("condition [knn]: vectorizer [%s] failed, error: %s", v, err.Error())
	}
	res, err := json.Marshal(vector[0].Vector)
	if err != nil {
		return "", fmt.Errorf("condition [in] json marshal right value failed, %s", err.Error())
	}

	// sub condition
	subDSL := ""
	if len(cond.mSubConds) > 0 {
		subDSL = `
		,
		"filter": {
			"bool": {
				"must": [
					%s
				]
			}
		}
		`

		subCondStr := ""
		for i, subCond := range cond.mSubConds {
			dsl, err := subCond.Convert(ctx, vectorizer)
			if err != nil {
				return "", err
			}

			if i != len(cond.mSubConds)-1 {
				dsl += ","
			}

			subCondStr += dsl

		}
		subDSL = fmt.Sprintf(subDSL, subCondStr)
	}

	// limit_key 和 limit_value 未给时，填入默认值
	key := cond.mCfg.RemainCfg["limit_key"]
	value := cond.mCfg.RemainCfg["limit_value"]
	if key == nil || key == "" {
		key = KNN_LIMIT_KEY_DEFAULT
	}
	if value == nil {
		value = KNN_LIMIT_VALUE_DEFAULT
	}

	dslStr := fmt.Sprintf(`
					{
						"knn": {
							"%s":{
								"%s": %v,
								"vector": %v
								%s
							}
						}
					}`, cond.mFilterFieldName, key, value,
		string(res), subDSL)

	return dslStr, nil
}

func (cond *KnnCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

func rewriteKnnCond(ctx context.Context, cfg *CondCfg,
	vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (*CondCfg, error) {

	// 过滤条件中的属性字段换成映射的视图字段
	if cfg.NameField.Name == "" {
		return nil, fmt.Errorf("向量过滤[knn]操作符使用的过滤字段[%s]在对象类的属性中不存在", cfg.Name)
	}

	// knn 落到两种字段上：
	//   1. 属性本身就是 vector 类型（数据视图那条老路），向量就在映射字段上；
	//   2. 属性是普通标量，但绑定资源为它建了向量索引——向量写在构建任务生成的另一个
	//      字段上，字段名由 BKN 随 Schema 下发在 index_config.vector_config.vector_field。
	// 第二种是资源类对象类的常态：源列是 string，属性类型不可能是 vector，只看类型会
	// 把建好的向量索引全部判死。
	vectorField := ""
	switch {
	case cfg.NameField.Type == dtype.DATATYPE_VECTOR:
		vectorField = cfg.NameField.MappedField.Name
	case cfg.NameField.IndexConfig != nil && cfg.NameField.IndexConfig.VectorConfig.VectorField != "":
		vectorField = cfg.NameField.IndexConfig.VectorConfig.VectorField
	default:
		return nil, fmt.Errorf("condition [knn] left field is not a vector field and has no vector index: %s:%s", cfg.NameField.Name, cfg.NameField.Type)
	}

	// 向量化必须有模型；缺了就没法把查询词变成向量。
	if cfg.NameField.IndexConfig == nil || cfg.NameField.IndexConfig.VectorConfig.ModelID == "" {
		return nil, fmt.Errorf("condition [knn] left field field: %s need config a small model, current small model is empty", cfg.NameField.Name)
	}

	// value 是向量化后的内容
	v := fmt.Sprintf("%v", cfg.Value)

	vector, err := vectorizer(ctx, cfg.NameField, v)
	if err != nil {
		return nil, fmt.Errorf("condition [knn]: vectorizer [%s] failed, error: %s", v, err.Error())
	}

	return &CondCfg{
		Name:      vectorField,
		Operation: OperationKNNVector, // 操作符为 knn_vector
		ValueOptCfg: ValueOptCfg{
			Value: vector[0].Vector, // 值用向量化后的内容
		},
		RemainCfg: cfg.RemainCfg,
	}, nil
}
