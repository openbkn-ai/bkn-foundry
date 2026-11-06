// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"context"
	"encoding/json"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"net/http"
	"slices"
	"time"
)

func ApplySourceFieldDecision(ctx context.Context, draft *interfaces.Draft, edited *interfaces.DraftObjectType, decision *interfaces.DualSourceDecision, caller interfaces.AccountInfo) error {
	if decision == nil {
		return nil
	}
	invalid := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidSourceFields)
	}
	if edited.Document == nil || edited.Document.Role != "entity" {
		return invalid()
	}
	for _, field := range edited.Properties {
		if !field.ReadOnly && field.SourceOfRecord == "fusion" {
			field.SourceOfRecord = "document"
		}
	}
	if decision.Clear {
		edited.Document.DualSource = nil
		return nil
	}
	items := draft.Items
	items.ObjectTypes = slices.Clone(items.ObjectTypes)
	for index, object := range items.ObjectTypes {
		if object.ID == edited.ID {
			items.ObjectTypes[index] = edited
		}
	}
	signature, err := kbconcept.SourcePlanSignature(items, edited.ID, decision.DualSourceChoice, false)
	if err != nil {
		return invalid()
	}
	// Copy user choices, never caller-supplied audit metadata or backing bindings.
	raw, err := json.Marshal(decision.DualSourceChoice)
	if err != nil {
		return invalid()
	}
	plan := &interfaces.DraftDualSourcePlan{}
	if json.Unmarshal(raw, &plan.DualSourceChoice) != nil {
		return invalid()
	}
	if decision.Confirm {
		plan.ConfirmedBy = caller
		plan.ConfirmedAt = time.Now().UnixMilli()
		plan.Signature = signature
		for _, rule := range plan.Fields {
			if rule.DestinationObjectTypeID == edited.ID {
				for _, field := range edited.Properties {
					if field.Name == rule.Name && !field.ReadOnly {
						field.SourceOfRecord = rule.SourceOfRecord
					}
				}
			}
		}
	}
	edited.Document.DualSource = plan
	return nil
}

// Same-business identity bridges require explicit field authority decisions.
// Other document relations retain the existing confirmation workflow.
func checkSourceFieldDecisions(ctx context.Context, draft *interfaces.Draft) error {
	required := map[string]bool{}
	for _, relation := range draft.Items.RelationTypes {
		if relation != nil && relation.Adopted && kbconcept.BusinessIdentityTarget(draft.Items, relation) != nil {
			required[relation.Source] = true
		}
	}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Document != nil && object.Document.DualSource != nil && object.Document.DualSource.ConfirmedAt > 0 {
			required[object.ID] = true
		}
		if object != nil && object.Adopted && required[object.ID] && !kbconcept.SourcePlanReady(draft.Items, object, true) {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_DualSourceNotConfirmed)
		}
	}
	return nil
}
