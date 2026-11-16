// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
)

// ErrActionPermitRejected means the permit is absent, stale, consumed, or does
// not match the current plan. Other errors indicate governance is unavailable.
var ErrActionPermitRejected = errors.New("action governance permit rejected")

// ActionGovernanceClaim is assembled from the authenticated caller and the
// current published action/target resolved by ontology-query. Request and
// permit IDs are references supplied by the client, never authority by themselves.
type ActionGovernanceClaim struct {
	KNID               string
	ActionTypeID       string
	DefinitionDigest   string
	Initiator          AccountInfo
	RequestID          string
	PermitID           string
	PlanDigest         string
	TargetIdentity     map[string]any
	DynamicParams      map[string]any
	RelationTarget     *ActionRelationTarget
	ObjectInput        map[string]any
	ActionTypeSnapshot map[string]any
}

// ActionGovernanceGate must compare the current trusted plan with the saved
// governance plan and atomically claim its permit. Returning nil is an
// authorization to create exactly one execution record.
type ActionGovernanceGate interface {
	ClaimActionPermit(context.Context, ActionGovernanceClaim) error
}
