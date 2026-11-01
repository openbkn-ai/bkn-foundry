// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func documentRelationCandidate(original *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave) (*interfaces.DraftRelationType, error) {
	raw, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	candidate := &interfaces.DraftRelationType{}
	if err = json.Unmarshal(raw, candidate); err != nil {
		return nil, err
	}
	candidate.Name, candidate.Description = save.Name, save.Comment
	candidate.Source, candidate.Target = save.SourceObjectTypeID, save.TargetObjectTypeID
	if save.Cardinality != "" {
		candidate.Cardinality = save.Cardinality
	}
	if save.DocumentMapping != nil {
		candidate.DocumentMapping = save.DocumentMapping
	}
	return candidate, nil
}
func (s *draftService) saveDocumentRelation(ctx context.Context, token string, draft *interfaces.Draft, original *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	if (save.Type != "" && save.Type != "direct") || (len(save.MappingRules) > 0 && string(save.MappingRules) != "null") {
		return nil, invalidRelation(ctx, map[string]any{"reason": relationNoMapping})
	}
	candidate, err := documentRelationCandidate(original, save)
	if err != nil {
		return nil, invalidRelation(ctx, nil)
	}
	before := relationSnapshot(original)
	beforeName, beforeDescription, beforeAdopted := original.Name, original.Description, original.Adopted
	if strings.HasPrefix(original.Document.Role, "evidence_") {
		if candidate.Source != original.Source || candidate.Target != original.Target || candidate.Cardinality != original.Cardinality || !reflect.DeepEqual(candidate.DocumentMapping, original.DocumentMapping) {
			return nil, invalidRelation(ctx, nil)
		}
	} else {
		input, err := s.documentEditInput(ctx, token, draft, original.Document)
		if err != nil {
			return nil, err
		}
		candidate, err = kbconcept.ValidateEditedRelation(input, draft.Items, original, candidate)
		if err != nil {
			return nil, invalidRelation(ctx, nil)
		}
	}
	name, description := candidate.Name, candidate.Description
	*original = *candidate
	original.Name, original.Description, original.Adopted = beforeName, beforeDescription, beforeAdopted
	edit := interfaces.DraftItemEdit{Name: &name, Description: &description}
	if beforeAdopted && !(objectTypeAdopted(&draft.Items, original.Source) && objectTypeAdopted(&draft.Items, original.Target)) {
		adopted := false
		edit.Adopted = &adopted
		original.ProposedBecause = proposedEndNotAdopted
	}
	decisions, err := applyEdit(ctx, &draft.Items, interfaces.DRAFT_KIND_RELATION_TYPES, original.ID, edit)
	if err != nil {
		return nil, err
	}
	after := relationSnapshot(original)
	if !sameJoin(before, after) {
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: original.ID, Action: interfaces.DRAFT_DECISION_SAVE, Before: before, After: after})
	}
	if err = s.commitDocumentEdit(ctx, draft, decisions, caller, original.Document); err != nil {
		return nil, err
	}
	return draft, nil
}
func (s *draftService) createDocumentRelation(ctx context.Context, token string, draft *interfaces.Draft, relation *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	source := findObjectType(&draft.Items, save.SourceObjectTypeID)
	if source == nil || source.Document == nil || strings.HasPrefix(source.Document.Role, "evidence_") || save.DocumentMapping == nil {
		return nil, invalidRelation(ctx, nil)
	}
	input, err := s.documentEditInput(ctx, token, draft, source.Document)
	if err != nil {
		return nil, err
	}
	reason, _ := i18n.Render(i18n.Language(draftLanguage(draft)), "BknProject.Draft.DocumentRelation.ManualReason", nil)
	relation.Source, relation.Target = save.SourceObjectTypeID, save.TargetObjectTypeID
	relation.Cardinality = save.Cardinality
	if relation.Cardinality == "" {
		relation.Cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
	}
	relation.Document = &interfaces.DraftDocumentModel{Role: "relation", Layer: "fact", Reason: reason, Citations: source.Document.Citations, SelectionID: source.Document.SelectionID, SelectionRevision: source.Document.SelectionRevision}
	relation.DocumentMapping = save.DocumentMapping
	checked, err := kbconcept.ValidateEditedRelation(input, draft.Items, relation, relation)
	if err != nil {
		return nil, invalidRelation(ctx, nil)
	}
	checked.Origin = interfaces.DRAFT_ORIGIN_MANUAL
	checked.Adopted = objectTypeAdopted(&draft.Items, checked.Source) && objectTypeAdopted(&draft.Items, checked.Target)
	if !checked.Adopted {
		checked.ProposedBecause = proposedEndNotAdopted
	}
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, checked)
	decisions := []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: checked.ID, Action: interfaces.DRAFT_DECISION_CREATE, After: relationSnapshot(checked)}}
	if err = s.commitDocumentEdit(ctx, draft, decisions, caller, source.Document); err != nil {
		return nil, err
	}
	return draft, nil
}
