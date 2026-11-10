// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// CypherQueryReq is one read-only Cypher statement for bkn-backend's internal
// cypher-queries endpoint.
type CypherQueryReq struct {
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters,omitempty"`
	// Explain compiles, plans and authorizes the statement without reading data.
	Explain bool `json:"explain,omitempty"`
}

// CypherQueryResult is bkn-backend's reply as it came: the status code and the
// raw body, success or error, so a caller can pass an error through unchanged.
type CypherQueryResult struct {
	StatusCode int
	Body       []byte
}

// BknBackendClient runs Cypher statements in bkn-backend.
//
//go:generate mockgen -source=bkn_backend.go -destination=../mocks/bkn_backend.go -package=mocks
type BknBackendClient interface {
	// RunCypher runs the statement against one knowledge network as the account
	// authenticated on ctx. It never falls back to another identity: without an
	// authenticated account it returns an error and sends nothing.
	RunCypher(ctx context.Context, knID string, req *CypherQueryReq) (*CypherQueryResult, error)
}
