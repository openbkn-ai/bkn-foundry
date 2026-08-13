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

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

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
}

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
