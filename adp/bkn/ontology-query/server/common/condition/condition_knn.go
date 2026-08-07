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

	if cfg.NameField.Name == "" {
		return nil, fmt.Errorf("向量过滤[knn]操作符使用的过滤字段[%s]在对象类的属性中不存在", cfg.Name)
	}

	// 属性本身就是 vector 类型：向量就存在这个字段上，用哪个模型也由对象类自己声明
	// （index_config.vector_config.model_id）。这是对象类的 Schema，不是底层索引的
	// 实现细节，所以在这里算好向量传下去——这类资源可能根本没有本地构建索引
	// （例如直接对接的向量索引资源），下游没有别的依据可用。
	if cfg.NameField.Type == dtype.DATATYPE_VECTOR {
		if cfg.NameField.IndexConfig == nil || cfg.NameField.IndexConfig.VectorConfig.ModelID == "" {
			return nil, fmt.Errorf("condition [knn] left field field: %s need config a small model, current small model is empty", cfg.NameField.Name)
		}

		vector, err := vectorizer(ctx, cfg.NameField, fmt.Sprintf("%v", cfg.Value))
		if err != nil {
			return nil, fmt.Errorf("condition [knn]: vectorizer [%v] failed, error: %s", cfg.Value, err.Error())
		}

		return &CondCfg{
			Name:      cfg.NameField.MappedField.Name,
			Operation: OperationKNNVector,
			ValueOptCfg: ValueOptCfg{
				Value: vector[0].Vector,
			},
			RemainCfg: cfg.RemainCfg,
		}, nil
	}

	// 标量属性：向量落在构建任务生成的字段上，字段名与模型都是本地索引的实现细节，
	// 交给 vega 自己解析——它知道索引是哪个构建任务建的、当时用的哪个模型。这里只把
	// 逻辑属性名换成资源字段名，查询词原样下传（与全文检索的分工一致）。
	return &CondCfg{
		Name:      cfg.NameField.MappedField.Name,
		Operation: OperationKNNVector,
		ValueOptCfg: ValueOptCfg{
			Value: cfg.Value,
		},
		RemainCfg: cfg.RemainCfg,
	}, nil
}
