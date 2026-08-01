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

const legacyDataViewMessage = "data_view data source is no longer supported; rebind the concept to a vega resource (data_source.type=resource) or rely on a built index"

// ResolveDataSourceType returns the explicit data_source.type, or empty when unset.
func ResolveDataSourceType(ds *interfaces.ResourceInfo) string {
	if ds == nil {
		return ""
	}
	return ds.Type
}

// IsLegacyDataViewBinding reports bindings that previously routed to mdl-uniquery.
func IsLegacyDataViewBinding(dsType string) bool {
	return dsType == "" || dsType == interfaces.DATA_SOURCE_TYPE_DATA_VIEW
}

// LegacyDataViewBindingError returns a client-facing 400 for deprecated data_view bindings.
func LegacyDataViewBindingError(ctx context.Context, conceptKind, conceptID string) *rest.HTTPError {
	detail := legacyDataViewMessage
	if conceptID != "" {
		detail = fmt.Sprintf("%s: %s=%s", legacyDataViewMessage, conceptKind, conceptID)
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest,
		oerrors.OntologyQuery_UnsupportedLegacyDataSourceBinding).
		WithErrorDetails(detail)
}
