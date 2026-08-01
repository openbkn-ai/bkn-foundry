// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-comm-go/rest"

	"bkn-backend/interfaces"
)

const (
	LegacyDataViewBindingIssue   = "legacy_data_view_unsupported"
	ResourceBindingNotFoundIssue = "resource_not_found"
)

// ResolveDataSourceType returns the explicit data_source.type, or empty when unset.
func ResolveDataSourceType(ds *interfaces.ResourceInfo) string {
	if ds == nil {
		return ""
	}
	return ds.Type
}

// IsLegacyDataViewBinding reports bindings that previously routed to mdl-uniquery / mdl-data-model.
func IsLegacyDataViewBinding(dsType string) bool {
	return dsType == "" || dsType == interfaces.DATA_SOURCE_TYPE_DATA_VIEW
}

// LegacyDataViewBindingError returns a client-facing 400 for deprecated data_view bindings.
func LegacyDataViewBindingError(ctx context.Context, errorCode string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, errorCode)
}

// EnrichDataSourceBindingStatus marks legacy data_view bindings as unavailable in schema responses.
func EnrichDataSourceBindingStatus(ds *interfaces.ResourceInfo) {
	if ds == nil {
		return
	}
	if !IsLegacyDataViewBinding(ResolveDataSourceType(ds)) {
		return
	}
	unavailable := false
	ds.BindingAvailable = &unavailable
	ds.BindingIssue = LegacyDataViewBindingIssue
}

// MarkResourceBindingAvailable marks a vega resource binding as healthy in schema responses.
func MarkResourceBindingAvailable(ds *interfaces.ResourceInfo) {
	if ds == nil {
		return
	}
	available := true
	ds.BindingAvailable = &available
	ds.BindingIssue = ""
}

// MarkResourceBindingUnavailable marks a missing or unreachable vega resource binding.
func MarkResourceBindingUnavailable(ds *interfaces.ResourceInfo) {
	if ds == nil {
		return
	}
	unavailable := false
	ds.BindingAvailable = &unavailable
	ds.BindingIssue = ResourceBindingNotFoundIssue
}
