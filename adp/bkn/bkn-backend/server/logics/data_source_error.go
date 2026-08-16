// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
)

// UnsupportedObjectTypeDataSourceError returns a client-facing 400 for invalid object-type bindings.
func UnsupportedObjectTypeDataSourceError(ctx context.Context, otID, dsType string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
		WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx),
			"BknBackend.ObjectType.InvalidParameter.Detail.DataSourceTypeNotSupported",
			map[string]any{"objectType": otID, "type": dsType}))
}

// UnsupportedRelationBackingDataSourceError returns a client-facing 400 for invalid relation backing bindings.
func UnsupportedRelationBackingDataSourceError(ctx context.Context, rtID, dsType string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
		WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx),
			"BknBackend.RelationType.InvalidParameter.Detail.BackingDataSourceTypeInvalid",
			map[string]any{"expected": "resource", "actual": dsType}))
}
