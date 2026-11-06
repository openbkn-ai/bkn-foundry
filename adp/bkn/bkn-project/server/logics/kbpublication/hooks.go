// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Hooks is what publishing the document items of a draft adds to a release.
type Hooks struct {
	// Drafts reads the draft a review inspects; nil reads it from the store of each project.
	Drafts interfaces.DraftAccess
	// Using is what publishes a release with the Git store locked for it; nil is the service.
	Using func(interfaces.GitStore) Publisher
}

var _ projecthooks.Publications = Hooks{}

func target(t projecthooks.ReleaseTarget) Target {
	return Target{ProjectID: t.ProjectID, LineageID: t.LineageID, Branch: t.Branch, BranchCommit: t.BranchCommit, NetworkID: t.NetworkID, DeclaredProjectID: t.DeclaredProjectID, Publisher: t.Publisher}
}

// Review checks the knowledge base the document items of the draft are built from.
func (h Hooks) Review(ctx context.Context, gs interfaces.GitStore, token string, draft *interfaces.Draft, t projecthooks.ReleaseTarget) (interfaces.ReleaseCheck, func(string) string, bool) {
	if !HasDocuments(draft) {
		return interfaces.ReleaseCheck{}, nil, false
	}
	inspector := NewService(gs)
	if h.Drafts != nil {
		inspector.Drafts = h.Drafts
	}
	inspected, inspectErr := inspector.Inspect(ctx, token, target(t))
	reason := ""
	if inspected == nil && inspectErr == nil {
		inspectErr = ErrInvalidSnapshot
	}
	if inspectErr != nil {
		reason = "knowledge_base_unavailable"
		if public, ok := PublicError(ctx, inspectErr).(*httperrors.HTTPError); ok {
			switch public.ErrorCode {
			case perrors.BknProject_Release_SourcePermission:
				reason = "knowledge_base_permission"
			case perrors.BknProject_Release_KnowledgeBaseNotReady:
				reason = "knowledge_base_not_ready"
			}
		}
	}
	check := interfaces.ReleaseCheck{Code: "knowledge_base", Passed: inspectErr == nil && inspected != nil, Reason: reason}
	if check.Passed {
		originals := 0
		for _, file := range inspected.Selection.Files {
			if file.IncludeOriginal {
				originals++
			}
		}
		check.Details = map[string]int{"original_documents": originals}
	}
	return check, func(signature string) string { return ReviewSignature(signature, inspected) }, true
}

// Prepare readies publishing the knowledge base of the release, if it has one.
func (h Hooks) Prepare(ctx context.Context, gs interfaces.GitStore, token string, t projecthooks.ReleaseTarget) (projecthooks.Publication, error) {
	var service Publisher = NewService(gs)
	if h.Using != nil {
		service = h.Using(gs)
	}
	return prepare(ctx, service, token, t)
}

// Publisher is the part of the service a release uses.
type Publisher interface {
	Prepare(context.Context, string, Target) (*interfaces.KBPublication, error)
	Mount(context.Context, string, *interfaces.KBPublication) error
	Activate(context.Context, *interfaces.KBPublication) error
}

func prepare(ctx context.Context, service Publisher, token string, t projecthooks.ReleaseTarget) (projecthooks.Publication, error) {
	record, err := service.Prepare(ctx, token, target(t))
	if err != nil {
		return nil, PublicError(ctx, err)
	}
	if record == nil {
		return nil, nil
	}
	return publication{service, record}, nil
}

type publication struct {
	service Publisher
	record  *interfaces.KBPublication
}

func (p publication) ProjectID() string { return p.record.ProjectID }
func (p publication) Mount(ctx context.Context, token string) error {
	if err := p.service.Mount(ctx, token, p.record); err != nil {
		return PublicError(ctx, err)
	}
	return nil
}
func (p publication) Activate(ctx context.Context) error { return p.service.Activate(ctx, p.record) }
