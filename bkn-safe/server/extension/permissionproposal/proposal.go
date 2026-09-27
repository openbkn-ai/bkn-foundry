// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permissionproposal is the paid-extension seam for permission-request
// proposals whose approval materializes a policy rather than a Casbin grant.
package permissionproposal

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"
)

var (
	ErrUnavailable = errors.New("permission proposal handler is unavailable")
	// ErrStale means the policy changed after the applicant took its preview.
	// Approval must become an auditable terminal invalidation, never overwrite
	// a newer configuration.
	ErrStale = errors.New("permission proposal is stale")
)

type Proposal struct {
	Kind    string
	Payload []byte
}

// ApprovalMode describes what happens after a reviewer agrees to a proposal.
// Most proposals can be applied atomically. A business-need proposal may be
// approved without changing a policy; an administrator configures that policy
// separately through the ordinary permission-configuration workflow.
type ApprovalMode string

const (
	ApprovalModeApply       ApprovalMode = "apply"
	ApprovalModeApproveOnly ApprovalMode = "approve_only"
)

type Request struct {
	ID, RequesterID, ReviewerID string
	ResourceType, ResourceID    string
	Reason                      string
	Proposal                    Proposal
}

// Handler validates a durable proposal and materializes it in the same
// database transaction as the approval decision. Implementations must never
// trust a subject supplied by a browser: RequesterID is the sole recipient.
type Handler interface {
	Validate(context.Context, Request) error
	Apply(context.Context, *gorm.DB, Request) error
	Preview(context.Context, string, string, string) (any, error)
	ApprovalMode() ApprovalMode
}

var registry = struct {
	sync.RWMutex
	handlers map[string]Handler
}{handlers: map[string]Handler{}}

func Register(kind string, handler Handler) {
	if kind == "" || handler == nil {
		panic("permissionproposal: invalid registration")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.handlers[kind]; exists {
		panic("permissionproposal: handler already registered")
	}
	registry.handlers[kind] = handler
}

func HandlerFor(kind string) (Handler, error) {
	registry.RLock()
	handler := registry.handlers[kind]
	registry.RUnlock()
	if handler == nil {
		return nil, ErrUnavailable
	}
	return handler, nil
}
