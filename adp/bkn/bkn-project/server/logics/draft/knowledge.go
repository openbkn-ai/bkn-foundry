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
// without an edition that reads it, nor before the draft is ready. Files counts the files the
// concepts and rules cite.
func Knowledge(ctx context.Context, draft *interfaces.Draft) interfaces.RequirementKnowledge {
	knowledge := interfaces.RequirementKnowledge{Concepts: []*interfaces.RequirementKnowledgeItem{},
		Rules: []*interfaces.RequirementKnowledgeItem{}}
	reader, ok := documentHooks().(projecthooks.KnowledgeReader)
	if !ok || draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return knowledge
	}
	held, rules := reader.Knowledge(conceptsOf(ctx, draft.ProjectID, draft, nil, false).Concepts)
	files := map[string]bool{}
	item := func(c interfaces.DraftConcept) *interfaces.RequirementKnowledgeItem {
		for _, source := range c.Sources {
			if source.Kind == interfaces.CONCEPT_SOURCE_DOCUMENT && source.ID != "" {
				files[source.ID] = true
			}
		}
		return &interfaces.RequirementKnowledgeItem{Name: c.Name, Description: c.Description, Kind: c.Verdict,
			Adopted: c.Item != nil && c.Item.Adopted}
	}
	for _, c := range held {
		knowledge.Concepts = append(knowledge.Concepts, item(c))
	}
	for _, c := range rules {
		knowledge.Rules = append(knowledge.Rules, item(c))
	}
	knowledge.Files = len(files)
	return knowledge
}
