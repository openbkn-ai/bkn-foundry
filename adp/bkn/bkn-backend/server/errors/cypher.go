// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

const (
	BknBackend_Cypher_InvalidParameter = "BknBackend.Cypher.InvalidParameter"
	BknBackend_Cypher_SyntaxError      = "BknBackend.Cypher.SyntaxError"
	BknBackend_Cypher_Unsupported      = "BknBackend.Cypher.Unsupported"
	BknBackend_Cypher_InvalidQuery     = "BknBackend.Cypher.InvalidQuery"
	BknBackend_Cypher_LimitExceeded    = "BknBackend.Cypher.LimitExceeded"
	BknBackend_Cypher_InternalError    = "BknBackend.Cypher.InternalError"
	BknBackend_Cypher_QueryFailed      = "BknBackend.Cypher.QueryFailed"
	// BknBackend_Cypher_Forbidden is the data source refusing a resource the
	// compiled statement reads. It is kept apart from QueryFailed so a missing
	// grant reads as one rather than as a transient failure.
	BknBackend_Cypher_Forbidden = "BknBackend.Cypher.Forbidden"
	// BknBackend_Cypher_PropertyForbidden is a property the query names that
	// the caller may not read in full.
	BknBackend_Cypher_PropertyForbidden = "BknBackend.Cypher.PropertyForbidden"
)

var CypherErrCodeList = []string{
	BknBackend_Cypher_InvalidParameter,
	BknBackend_Cypher_SyntaxError,
	BknBackend_Cypher_Unsupported,
	BknBackend_Cypher_InvalidQuery,
	BknBackend_Cypher_LimitExceeded,
	BknBackend_Cypher_InternalError,
	BknBackend_Cypher_QueryFailed,
	BknBackend_Cypher_Forbidden,
	BknBackend_Cypher_PropertyForbidden,
}
