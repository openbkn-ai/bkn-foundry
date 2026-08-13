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
)

// UnsupportedObjectTypeDataSourceError returns a client-facing 400 for invalid object-type bindings.
func UnsupportedObjectTypeDataSourceError(ctx context.Context, otID, dsType string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("object_type[%s] data_source.type must be resource, got %q", otID, dsType))
}

// UnsupportedRelationBackingDataSourceError returns a client-facing 400 for invalid relation backing bindings.
func UnsupportedRelationBackingDataSourceError(ctx context.Context, rtID, dsType string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("relation_type[%s] backing data_source.type must be resource, got %q", rtID, dsType))
}
