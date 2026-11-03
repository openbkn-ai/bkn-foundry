// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func factResolutionSignature(items interfaces.DraftItems, object *interfaces.DraftObjectType, valueName, relationID string) (string, error) {
	if object == nil || object.Document == nil || object.Document.Role != "fact" {
		return "", ErrInvalidModel
	}
	value := property(object, valueName)
	if value == nil || value.ReadOnly {
		return "", ErrInvalidModel
	}
	var relation *interfaces.DraftRelationType
	for _, candidate := range items.RelationTypes {
		if candidate != nil && candidate.ID == relationID {
			relation = candidate
			break
		}
	}
	if relation == nil || !relation.Adopted || relation.Document == nil || relation.Document.Role != "relation" || relation.DocumentRepair != "" || relation.Source != object.ID || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" {
		return "", ErrInvalidModel
	}
	mapping := relation.DocumentMapping
	mention := property(object, mapping.MentionProperty)
	if mention == nil || mention.ReadOnly || mention.Name == value.Name {
		return "", ErrInvalidModel
	}
	// A class with another business value is not a single-attribute fact. Never
	// silently assign the selected value's status to independent other values.
	for _, field := range object.Properties {
		if field == nil || !field.ReadOnly && field.Name != mention.Name && field.Name != value.Name {
			return "", ErrInvalidModel
		}
	}
	var target *interfaces.DraftObjectType
	for _, candidate := range items.ObjectTypes {
		if candidate != nil && candidate.ID == relation.Target {
			target = candidate
			break
		}
	}
	if target == nil || !target.Adopted || target.NeedsIdentity || target.Document != nil && target.Document.Role != "entity" || len(target.PrimaryKeys) != 1 || target.PrimaryKeys[0] != mapping.IdentityProperty {
		return "", ErrInvalidModel
	}
	identity := property(target, mapping.IdentityProperty)
	if identity == nil || identity.Type != mention.Type {
		return "", ErrInvalidModel
	}
	resource, mapped := "", ""
	if target.Document == nil {
		resource = target.Table.ResourceID
		mapped = identity.MappedField
		if resource == "" {
			return "", ErrInvalidModel
		}
	}
	raw, err := json.Marshal(struct{ Value, ValueType, Relation, Subject, Mention, MentionType, Identity, IdentityType, Resource, Mapped string }{value.Name, value.Type, relation.ID, target.ID, mention.Name, mention.Type, identity.Name, identity.Type, resource, mapped})
	if err != nil {
		return "", ErrInvalidModel
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ValidFactResolution(items interfaces.DraftItems, object *interfaces.DraftObjectType, proof *interfaces.FactResolutionRule) bool {
	if proof == nil || proof.ConfirmedBy.ID == "" || proof.ConfirmedBy.Type != "user" || proof.ConfirmedAt <= 0 {
		return false
	}
	signature, err := factResolutionSignature(items, object, proof.ValueProperty, proof.SubjectRelationID)
	return err == nil && signature == proof.RuleSignature
}

func ConfirmFactResolution(items interfaces.DraftItems, object *interfaces.DraftObjectType, choice interfaces.FactResolutionDecision, caller interfaces.AccountInfo, now int64) error {
	if object == nil || object.Document == nil || object.Document.Role != "fact" || caller.ID == "" || caller.Type != "user" || now <= 0 {
		return ErrInvalidModel
	}
	if choice.Clear {
		if choice.Confirm || choice.ValueProperty != "" || choice.SubjectRelationID != "" {
			return ErrInvalidModel
		}
		object.Document.FactResolution = nil
		return nil
	}
	if !choice.Confirm {
		// Human editing may save an unfinished choice without granting authority.
		// Validate references, but do not pretend a multi-value fact is resolved.
		if choice.ValueProperty != "" {
			field := property(object, choice.ValueProperty)
			if field == nil || field.ReadOnly {
				return ErrInvalidModel
			}
		}
		if choice.SubjectRelationID != "" {
			found := false
			for _, relation := range items.RelationTypes {
				if relation != nil && relation.ID == choice.SubjectRelationID && relation.Source == object.ID && relation.DocumentMapping != nil && relation.DocumentMapping.Mode == "linking" {
					found = true
				}
			}
			if !found {
				return ErrInvalidModel
			}
		}
		object.Document.FactResolution = &interfaces.FactResolutionRule{ValueProperty: choice.ValueProperty, SubjectRelationID: choice.SubjectRelationID, State: "proposed"}
		return nil
	}
	signature, err := factResolutionSignature(items, object, choice.ValueProperty, choice.SubjectRelationID)
	if err != nil {
		return err
	}
	object.Document.FactResolution = &interfaces.FactResolutionRule{ValueProperty: choice.ValueProperty, SubjectRelationID: choice.SubjectRelationID, RuleSignature: signature, State: "confirmed", ConfirmedBy: caller, ConfirmedAt: now}
	return nil
}

// RefreshFactResolutionStates projects current semantic validity for the editor.
// It is not an authority decision and is excluded from the model fingerprint.
func RefreshFactResolutionStates(items interfaces.DraftItems) {
	for _, object := range items.ObjectTypes {
		if object == nil || object.Document == nil || object.Document.FactResolution == nil {
			continue
		}
		proof := object.Document.FactResolution
		proof.State = "proposed"
		if proof.ConfirmedAt > 0 {
			proof.State = "stale"
		}
		if ValidFactResolution(items, object, proof) {
			proof.State = "confirmed"
		}
	}
}
