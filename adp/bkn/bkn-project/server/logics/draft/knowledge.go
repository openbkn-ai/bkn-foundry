// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Knowledge is what the draft's sources beyond tables hold for kinetic analysis, scenario drafting
// and Ask AI, as the edition reads it from the draft's concepts with a person's judgments: none
// without an edition that reads it, nor before the draft is ready.
func Knowledge(ctx context.Context, draft *interfaces.Draft) interfaces.RequirementKnowledge {
	none := interfaces.RequirementKnowledge{Concepts: []*interfaces.RequirementKnowledgeItem{},
		Rules: []*interfaces.RequirementKnowledgeItem{}}
	reader, ok := documentHooks().(projecthooks.KnowledgeReader)
	if !ok || draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return none
	}
	knowledge := reader.Knowledge(conceptsOf(ctx, draft.ProjectID, draft, nil, false).Concepts)
	if knowledge.Concepts == nil {
		knowledge.Concepts = none.Concepts
	}
	if knowledge.Rules == nil {
		knowledge.Rules = none.Rules
	}
	return knowledge
}
