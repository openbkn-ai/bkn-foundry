// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func BuildDslQuery(ctx context.Context, queryStr string, query *interfaces.ConceptsQuery) (map[string]any, error) {
	var dslMap map[string]any
	err := json.Unmarshal([]byte(queryStr), &dslMap)
	if err != nil {
		return map[string]any{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_InternalError_UnMarshalDataFailed).
			WithErrorDetails(fmt.Sprintf("failed to unMarshal dslStr to map, %s", err.Error()))
	}

	// 处理 sort
	sort := []map[string]any{}
	for _, sp := range query.Sort {
		// 不做排序字段参数校验了，如果排序字段不存在，opensearch会报错，由opensearch来报错
		sort = append(sort, map[string]any{
			sp.Field: sp.Direction,
		})
	}

	dsl := map[string]any{
		"size":         query.Limit,
		"sort":         sort,
		"track_scores": true,
	}
	dsl["query"] = dslMap

	return dsl, nil
}

// PropertyIndexCaps 描述 Vega 资源的某个字段在本地索引里实际具备的检索能力。
// 能力来源是资源 schema 上的字段 features（由 `openbkn vega dataset build` 写入），
// 不是对象类属性上手填的 index_config。
type PropertyIndexCaps struct {
	Keyword  bool
	Fulltext bool
	Vector   bool
	// EmbeddingModel 是该字段建向量索引时用的模型，取自字段特性的 embedding_model，
	// 缺省回落到资源级 default_embedding_model。资源上存的可能是模型名而不是 ID。
	EmbeddingModel string
	// VectorField 是构建任务为该字段生成的向量字段名。命名规则由 vega 构建侧决定
	// （见 worker/build_task_common.go 的 appendTaskEmbeddingVectorFields），这里
	// 跟着它走，避免下游各自拼一遍。
	VectorField string
}

// VectorFieldSuffix 与 vega 构建任务生成向量字段时使用的后缀保持一致。
const VectorFieldSuffix = "_vector"

// VegaResourceIndexCaps 派生资源各字段的索引能力，key 是资源字段名。
//
// 资源没有本地索引（index_name 为空）时返回 nil：features 只是「配置了要建什么」，
// 构建任务没跑完之前这些能力并不存在，此时 Vega 会回落到源库实时查。
func VegaResourceIndexCaps(res *interfaces.VegaResource) map[string]PropertyIndexCaps {
	if res == nil || res.LocalIndexName == "" {
		return nil
	}

	caps := make(map[string]PropertyIndexCaps, len(res.SchemaDefinition))
	for _, p := range res.SchemaDefinition {
		if p == nil {
			continue
		}
		for _, feature := range p.Features {
			// 特性可以挂在一个属性上而作用于另一个字段（ref_property）。归属必须跟
			// vega-backend 生成构建任务快照时的算法一致，否则能力会记到错误的字段上。
			field := p.Name
			if feature.RefProperty != "" {
				field = feature.RefProperty
			}
			propCaps := caps[field]
			switch feature.FeatureType {
			case interfaces.FieldFeatureType_Keyword:
				propCaps.Keyword = true
			case interfaces.FieldFeatureType_Fulltext:
				propCaps.Fulltext = true
			case interfaces.FieldFeatureType_Vector:
				propCaps.Vector = true
				propCaps.VectorField = field + VectorFieldSuffix
				propCaps.EmbeddingModel = stringConfigValue(feature.Config, "embedding_model")
				if propCaps.EmbeddingModel == "" && res.IndexConfig != nil {
					propCaps.EmbeddingModel = res.IndexConfig.DefaultEmbeddingModel
				}
			default:
				continue
			}
			caps[field] = propCaps
		}
	}
	return caps
}

// VegaResourceSchemaToFieldsMap maps vega Resource schema to view-like fields for display and validation.
func VegaResourceSchemaToFieldsMap(res *interfaces.VegaResource) map[string]*interfaces.ViewField {
	fields := make(map[string]*interfaces.ViewField)
	for _, p := range res.SchemaDefinition {
		if p == nil {
			continue
		}
		fields[p.Name] = &interfaces.ViewField{
			Name:         p.Name,
			Type:         p.Type,
			DisplayName:  p.DisplayName,
			OriginalName: p.OriginalName,
		}
	}
	return fields
}

// stringConfigValue 读取特性 config 里的字符串项，缺失或类型不符时返回空串。
func stringConfigValue(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
