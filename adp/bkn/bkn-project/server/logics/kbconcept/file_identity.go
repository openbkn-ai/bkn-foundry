// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"

func fileKeyField(object *interfaces.DraftObjectType, name string) bool {
	field := property(object, name)
	return field != nil && !field.ReadOnly && (field.Type == "string" || field.Type == "text" || field.Type == "integer" || field.Type == "unsigned integer")
}
func ConfirmFileIdentity(object *interfaces.DraftObjectType, decision interfaces.FileIdentityDecision, caller interfaces.AccountInfo, now int64) error {
	if object == nil || object.Document == nil || object.Document.Role != "file" || caller.ID == "" || caller.Type != "user" || now <= 0 {
		return ErrInvalidModel
	}
	if decision.Clear {
		if decision.Property != "" || decision.Confirm {
			return ErrInvalidModel
		}
		object.Document.FileIdentity = nil
		return nil
	}
	if !decision.Confirm || !fileKeyField(object, decision.Property) {
		return ErrInvalidModel
	}
	object.Document.FileIdentity = &interfaces.FileBusinessIdentity{Property: decision.Property, ConfirmedBy: caller, ConfirmedAt: now}
	return nil
}

// FileIdentityProperty uses the explicit human decision or an independently
// verified dual-source authority plan. A conflicting reused key is never guessed.
func FileIdentityProperty(items interfaces.DraftItems, file *interfaces.DraftObjectType) (string, error) {
	if file == nil || file.Document == nil || file.Document.Role != "file" {
		return "", ErrInvalidModel
	}
	chosen := ""
	if proof := file.Document.FileIdentity; proof != nil {
		if proof.ConfirmedBy.ID == "" || proof.ConfirmedBy.Type != "user" || proof.ConfirmedAt <= 0 || !fileKeyField(file, proof.Property) {
			return "", ErrInvalidModel
		}
		chosen = proof.Property
	}
	for _, entity := range items.ObjectTypes {
		if entity == nil || !entity.Adopted || entity.Document == nil || entity.Document.DualSource == nil || entity.Document.DualSource.FileObjectTypeID != file.ID {
			continue
		}
		if !SourcePlanReady(items, entity, true) || len(entity.PrimaryKeys) != 1 {
			return "", ErrInvalidModel
		}
		name := entity.PrimaryKeys[0]
		if !fileKeyField(file, name) || (chosen != "" && chosen != name) {
			return "", ErrInvalidModel
		}
		chosen = name
	}
	if chosen == "" {
		return "", ErrInvalidModel
	}
	return chosen, nil
}
