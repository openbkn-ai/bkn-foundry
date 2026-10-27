// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
)

// ErrKnowledgeBaseUnavailable means this deployment has no knowledge base service to ask:
// knowledge bases are an Enterprise capability served by kb-api, and KB_API_URL is unset.
var ErrKnowledgeBaseUnavailable = errors.New("knowledge base service is not configured")

// KnowledgeBaseBrief is the identity of one Enterprise knowledge base, as kb-api reports it to
// in-cluster callers. It carries nothing about the knowledge base's content.
type KnowledgeBaseBrief struct {
	ID   string
	Name string
}

// KnowledgeBaseAccess reads knowledge base identities from kb-api's in-cluster API, the same
// endpoints bkn-safe uses to list knowledge_base authorization resources.
type KnowledgeBaseAccess interface {
	// GetKnowledgeBase returns nil, nil when the knowledge base does not exist.
	GetKnowledgeBase(ctx context.Context, kbID string) (*KnowledgeBaseBrief, error)
	// FindKnowledgeBasesByName returns the knowledge bases whose name is exactly name.
	FindKnowledgeBasesByName(ctx context.Context, name string) ([]*KnowledgeBaseBrief, error)
}
