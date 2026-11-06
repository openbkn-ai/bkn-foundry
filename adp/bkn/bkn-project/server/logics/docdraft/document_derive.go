// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
)

func (s service) deriveDocumentObject(ctx context.Context, token string, draft *interfaces.Draft, original *interfaces.DraftObjectType, save interfaces.DraftObjectTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	// Derivation is one explicit operation, never an implicit side effect of a save.
	if save.Name != original.Name || save.Comment != original.Description || len(save.DataProperties) > 0 || len(save.PrimaryKeys) > 0 || save.DisplayKey != "" || save.IncrementalKey != "" || save.Document.DualSource != nil || save.Document.TimeProperty != "" || len(save.Document.ParticipantRelations) > 0 {
		return nil, documentEditError(ctx)
	}
	input, err := s.documentEditInput(ctx, token, draft, original.Document)
	if err != nil {
		return nil, err
	}
	derived, err := kbconcept.DeriveObject(input, draft.Items, original, *save.Document.Derive)
	if err != nil {
		return nil, documentValidationError(ctx, err)
	}
	// Source ID is retained in the decision's before snapshot; the source remains
	// untouched and the new object must be individually reviewed before adoption.
	decision := &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: derived.ID, Action: interfaces.DRAFT_DECISION_DERIVE, Before: map[string]any{"source_item_id": original.ID, "definition": s.Snapshot(original)}, After: s.Snapshot(derived)}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, derived)
	evidence, err := kbconcept.BuildEvidence(input, draft.Items)
	if err != nil {
		return nil, documentValidationError(ctx, err)
	}
	decisions := []*interfaces.DraftDecision{decision}
	for _, object := range evidence.Model.ObjectTypes {
		draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, object)
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: object.ID, Action: interfaces.DRAFT_DECISION_DERIVE, After: s.Snapshot(object)})
	}
	for _, relation := range evidence.Model.RelationTypes {
		draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: relation.ID, Action: interfaces.DRAFT_DECISION_DERIVE, After: s.RelationSnapshot(relation)})
	}
	if err = s.commitDocumentEdit(ctx, draft, decisions, caller, original.Document); err != nil {
		return nil, err
	}
	return draft, nil
}
