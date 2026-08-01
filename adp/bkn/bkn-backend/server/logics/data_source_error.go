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

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func isLegacyDataViewType(dsType string) bool {
	return dsType == "" || dsType == interfaces.DATA_SOURCE_TYPE_DATA_VIEW
}

// UnsupportedObjectTypeDataSourceError returns a client-facing 400 for invalid object-type bindings.
func UnsupportedObjectTypeDataSourceError(ctx context.Context, otID, dsType string) *rest.HTTPError {
	if isLegacyDataViewType(dsType) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf(
				"data_view 数据源已废弃，请将 object_type[%s] 重新绑定为 vega resource（data_source.type=resource）", otID))
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("unsupported data_source.type %q on object_type %s", dsType, otID))
}

// UnsupportedRelationBackingDataSourceError returns a client-facing 400 for invalid relation backing bindings.
func UnsupportedRelationBackingDataSourceError(ctx context.Context, rtID, dsType string) *rest.HTTPError {
	if isLegacyDataViewType(dsType) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf(
				"data_view 数据源已废弃，请将 relation_type[%s] 的 backing 重新绑定为 vega resource（data_source.type=resource）", rtID))
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("unsupported relation backing data_source.type %q on relation_type %s", dsType, rtID))
}
