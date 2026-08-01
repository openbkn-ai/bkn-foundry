// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

func isLegacyDataViewType(dsType string) bool {
	return dsType == "" || dsType == interfaces.DATA_SOURCE_TYPE_DATA_VIEW
}

// MissingObjectTypeDataSourceError returns a client-facing 400 when an object type has no data source binding.
func MissingObjectTypeDataSourceError(ctx context.Context, otID string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("对象类[%s]未绑定数据源", otID))
}

// UnsupportedObjectTypeDataSourceError returns a client-facing 400 for invalid object-type bindings.
func UnsupportedObjectTypeDataSourceError(ctx context.Context, otID, dsType string) *rest.HTTPError {
	if isLegacyDataViewType(dsType) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf(
				"data_view 数据源已废弃，请将 object_type[%s] 重新绑定为 vega resource（data_source.type=resource）", otID))
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("unsupported data_source.type %q on object_type %s", dsType, otID))
}

// UnsupportedRelationBackingDataSourceError returns a client-facing 400 for invalid relation backing bindings.
func UnsupportedRelationBackingDataSourceError(ctx context.Context, dsType string) *rest.HTTPError {
	if isLegacyDataViewType(dsType) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails("data_view 数据源已废弃，请将间接关系 backing 重新绑定为 vega resource（data_source.type=resource）")
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("unsupported relation backing data_source.type %q", dsType))
}
