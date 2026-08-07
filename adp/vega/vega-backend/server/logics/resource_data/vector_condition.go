// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"fmt"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

// resolveVectorConditions 把向量检索条件里的查询文本就地换成向量，并把字段换成
// 索引里实际存向量的那个字段。
//
// 调用方只说「在 stadium_name 上做向量检索，查询词是这句话」。向量落在哪个字段、
// 用哪个模型算，都是本地索引的实现细节：字段是构建任务生成的，模型是建索引时定的。
// 这跟全文检索的处理方式一致——那边同样由 vega 自己把字段解析到分词子字段
// （见 opensearch_condition.go 的 fulltextFieldName），调用方不需要知道。
//
// 条件里已经是向量（值不是字符串）时原样放行：数据视图那条老路自己算好了向量。
func (rds *resourceDataService) resolveVectorConditions(ctx context.Context,
	resource *interfaces.Resource, cfg *interfaces.FilterCondCfg) error {

	if cfg == nil {
		return nil
	}

	for _, sub := range cfg.SubConds {
		if err := rds.resolveVectorConditions(ctx, resource, sub); err != nil {
			return err
		}
	}

	if cfg.Operation != filter_condition.OperationKnnVector {
		return nil
	}
	text, ok := cfg.Value.(string)
	if !ok {
		return nil
	}

	field, err := vectorFieldFor(resource, cfg.Name)
	if err != nil {
		return err
	}
	model, err := rds.embeddingModelForIndex(ctx, resource, cfg.Name)
	if err != nil {
		return err
	}

	vectors, err := rds.mfs.GetVector(ctx, model, []string{text})
	if err != nil {
		return fmt.Errorf("condition [knn_vector] vectorize %q with model %q failed: %w", text, model, err)
	}
	if len(vectors) == 0 || vectors[0] == nil {
		return fmt.Errorf("condition [knn_vector] model %q returned no vector for %q", model, text)
	}

	cfg.Name = field
	cfg.Value = vectors[0].Vector
	return nil
}

// vectorFieldFor 找出某个源字段对应的物理向量字段。
//
// 字段本身就是向量类型时（数据视图那条路）直接用它。否则要求源字段上声明了向量
// 特性，向量在构建任务生成的字段上。找不到就报错——这里不做降级：把向量检索悄悄
// 换成别的算子，调用方拿到的是一个语义完全不同却看起来成功的结果。
func vectorFieldFor(resource *interfaces.Resource, name string) (string, error) {
	if resource == nil {
		return "", fmt.Errorf("condition [knn_vector] left field '%s' has no resource context", name)
	}
	if resource.LocalIndexName == "" {
		return "", fmt.Errorf("condition [knn_vector] resource '%s' has no local index; build one before vector search", resource.Name)
	}

	for _, prop := range resource.SchemaDefinition {
		if prop == nil {
			continue
		}
		if prop.Name == name && prop.Type == interfaces.DataType_Vector {
			return name, nil
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			source := prop.Name
			if feature.RefProperty != "" {
				source = feature.RefProperty
			}
			if source == name {
				return interfaces.LocalIndexVectorFieldName(source), nil
			}
		}
	}

	return "", fmt.Errorf("condition [knn_vector] field '%s' has no vector index on resource '%s'", name, resource.Name)
}

// embeddingModelForIndex 取建这个索引时用的 embedding 模型 id。
//
// 权威来源是产出该索引的构建任务快照，不是资源上现在写着什么：资源的特性配置改了
// 但没重建索引时，两者会不一致，用错模型算出来的向量与索引里的不可比。快照里存的
// 已经是解析过的模型 id，与构建时写入向量用的完全一致。
func (rds *resourceDataService) embeddingModelForIndex(ctx context.Context,
	resource *interfaces.Resource, field string) (string, error) {

	taskID := interfaces.BuildTaskIDFromIndexName(resource.LocalIndexName)
	if taskID == "" {
		return "", fmt.Errorf("condition [knn_vector] cannot tell which build task produced index '%s'", resource.LocalIndexName)
	}

	task, err := rds.bta.GetByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("condition [knn_vector] load build task %s failed: %w", taskID, err)
	}
	if task == nil || task.IndexConfig == nil {
		return "", fmt.Errorf("condition [knn_vector] build task %s has no index config", taskID)
	}
	feature, ok := task.IndexConfig.Features[field]
	if !ok || feature.Vector == nil || feature.Vector.ModelID == "" {
		return "", fmt.Errorf("condition [knn_vector] field '%s' was not vectorized by build task %s", field, taskID)
	}
	return feature.Vector.ModelID, nil
}

