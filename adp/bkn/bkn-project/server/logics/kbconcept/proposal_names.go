// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
)

// PrepareProposalNames applies the document API naming convention before any
// human sees or adopts a new proposal. Existing draft identities never change.
// Collisions are rejected rather than merging objects or guessing a new name.
func PrepareProposalNames(existing interfaces.DraftItems, proposal kb.ConceptProposal) (kb.ConceptProposal, error) {
	raw, err := json.Marshal(proposal)
	if err != nil {
		return kb.ConceptProposal{}, ErrInvalidModel
	}
	var result kb.ConceptProposal
	if json.Unmarshal(raw, &result) != nil {
		return result, ErrInvalidModel
	}
	used := map[string]bool{}
	for _, object := range existing.ObjectTypes {
		if object != nil {
			used[object.ID] = true
		}
	}
	original := map[string]bool{}
	for _, object := range result.ObjectTypes {
		if object == nil || !modelID.MatchString(object.ID) || original[object.ID] || used[object.ID] {
			return result, ErrInvalidModel
		}
		original[object.ID] = true
	}
	renamed := map[string]string{}
	for _, object := range result.ObjectTypes {
		name := object.ID
		if object.Document != nil && object.Document.Role == "entity" && !strings.HasSuffix(name, "_doc") {
			if len(name) > 36 {
				name = name[:36]
			}
			name += "_doc"
		}
		if used[name] {
			return result, ErrInvalidModel
		}
		used[name] = true
		renamed[object.ID] = name
		object.ID = name
	}
	for _, relation := range result.RelationTypes {
		if relation == nil {
			continue
		}
		if name, ok := renamed[relation.Source]; ok {
			relation.Source = name
		}
		if name, ok := renamed[relation.Target]; ok {
			relation.Target = name
		}
	}
	for index := range result.Evidence {
		if result.Evidence[index].Kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
			if name, ok := renamed[result.Evidence[index].ID]; ok {
				result.Evidence[index].ID = name
			}
		}
	}
	return result, nil
}
