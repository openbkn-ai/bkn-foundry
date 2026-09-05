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
)

var CypherErrCodeList = []string{
	BknBackend_Cypher_InvalidParameter,
	BknBackend_Cypher_SyntaxError,
	BknBackend_Cypher_Unsupported,
	BknBackend_Cypher_InvalidQuery,
	BknBackend_Cypher_LimitExceeded,
	BknBackend_Cypher_InternalError,
	BknBackend_Cypher_QueryFailed,
}
