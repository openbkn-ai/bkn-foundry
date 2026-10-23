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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
)

// MissingObjectTypeDataSourceError returns a client-facing 400 when an object type has no data source binding.
func MissingObjectTypeDataSourceError(ctx context.Context, otID string) *httperrors.HTTPError {
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.DataSourceRequired", map[string]any{"objectTypeID": otID})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails(localizedText)
}

// UnsupportedObjectTypeDataSourceError returns a client-facing 400 for invalid object-type bindings.
func UnsupportedObjectTypeDataSourceError(ctx context.Context, otID, dsType string) *httperrors.HTTPError {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("object_type[%s] data_source.type must be resource, got %q", otID, dsType))
}

// UnsupportedRelationBackingDataSourceError returns a client-facing 400 for invalid relation backing bindings.
func UnsupportedRelationBackingDataSourceError(ctx context.Context, dsType string) *httperrors.HTTPError {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter).
		WithErrorDetails(fmt.Sprintf("relation backing data_source.type must be resource, got %q", dsType))
}
