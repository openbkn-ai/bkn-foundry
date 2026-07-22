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
