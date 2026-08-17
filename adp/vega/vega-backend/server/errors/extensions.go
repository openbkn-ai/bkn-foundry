// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors Extensions (Issue #382 option B) error code
package errors

// Extensions error code (currently all are 400 Bad Request)
const (
	// 400 Bad Request
	VegaBackend_Extensions_InvalidFormat         = "VegaBackend.Extensions.InvalidFormat"
	VegaBackend_Extensions_QuotaExceeded         = "VegaBackend.Extensions.QuotaExceeded"
	VegaBackend_Extensions_PropertyQuotaExceeded = "VegaBackend.Extensions.PropertyQuotaExceeded"
	VegaBackend_Extensions_ReservedKey           = "VegaBackend.Extensions.ReservedKey"
	VegaBackend_Extensions_MismatchedQueryPairs  = "VegaBackend.Extensions.MismatchedQueryPairs"
	VegaBackend_Extensions_TooManyFilterPairs    = "VegaBackend.Extensions.TooManyFilterPairs"
)

// The ExtensionsErrCodeList must be rest.Register in init; otherwise, the process will be fatal (missing errorCode) when this code is returned.
var ExtensionsErrCodeList = []string{
	// 400 Bad Request
	VegaBackend_Extensions_InvalidFormat,
	VegaBackend_Extensions_QuotaExceeded,
	VegaBackend_Extensions_PropertyQuotaExceeded,
	VegaBackend_Extensions_ReservedKey,
	VegaBackend_Extensions_MismatchedQueryPairs,
	VegaBackend_Extensions_TooManyFilterPairs,
}
