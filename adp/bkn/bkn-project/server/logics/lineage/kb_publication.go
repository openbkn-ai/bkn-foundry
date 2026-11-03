// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
)

type knowledgePublication interface {
	Prepare(context.Context, string, kbpublication.Target) (*interfaces.KBPublication, error)
	Mount(context.Context, string, *interfaces.KBPublication) error
	Activate(context.Context, *interfaces.KBPublication) error
}

func (s *lineageService) knowledgePublisher(gs interfaces.GitStore) knowledgePublication {
	if s.newKnowledgePublication != nil {
		return s.newKnowledgePublication(gs)
	}
	return newKnowledgePublisher(gs)
}

// The publisher is built with the already locked Git store for each release.
func newKnowledgePublisher(gs interfaces.GitStore) knowledgePublication {
	return kbpublication.NewService(gs)
}
