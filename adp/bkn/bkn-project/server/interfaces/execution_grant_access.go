// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"time"
)

// ExecutionGrantRequest asks bkn-safe for a credential one background run acts with for the
// person who started it, once that person's own sign-in has lapsed: on one knowledge network,
// through one Agent, for at most TTL.
type ExecutionGrantRequest struct {
	ID          string
	ProjectID   string
	RunID       string
	DelegatorID string
	KnID        string
	AgentID     string
	TTL         time.Duration
}

// ExecutionGrant is an issued credential. It is held in memory only: never stored or logged.
type ExecutionGrant struct {
	Credential string
	ExpiresAt  time.Time
}

//go:generate mockgen -source ../interfaces/execution_grant_access.go -destination ../interfaces/mock/mock_execution_grant_access.go
type ExecutionGrantAccess interface {
	// Issue asks bkn-safe for a credential; bkn-safe refuses a delegator who may not view the
	// network now.
	Issue(ctx context.Context, req ExecutionGrantRequest) (*ExecutionGrant, error)
	// Revoke ends a grant at once; revoking one not issued, or twice, is fine.
	Revoke(ctx context.Context, id string, projectID string, runID string) error
}
