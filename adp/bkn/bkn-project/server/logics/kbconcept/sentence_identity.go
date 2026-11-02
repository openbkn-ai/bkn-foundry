// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"slices"
)

func sentenceIdentityRole(object *interfaces.DraftObjectType) bool {
	return object != nil && object.Document != nil && (object.Document.Role == "in_document" || object.Document.Role == "fact" || object.Document.Role == "event")
}
func sentenceKeyField(object *interfaces.DraftObjectType, name string) bool {
	field := property(object, name)
	if field == nil || field.Type == "json" {
		return false
	}
	return !field.ReadOnly || ((object.Document.Role == "fact" || object.Document.Role == "event") && (name == "effective_from" || name == "effective_to"))
}
func ConfirmSentenceIdentity(object *interfaces.DraftObjectType, decision interfaces.SentenceIdentityDecision, caller interfaces.AccountInfo, now int64) error {
	if !sentenceIdentityRole(object) || caller.ID == "" || caller.Type != "user" || now <= 0 {
		return ErrInvalidModel
	}
	if decision.Clear {
		if len(decision.Properties) != 0 || decision.Confirm {
			return ErrInvalidModel
		}
		object.Document.SentenceIdentity = nil
		return nil
	}
	if !decision.Confirm || len(decision.Properties) == 0 || len(decision.Properties) > 8 {
		return ErrInvalidModel
	}
	fields := slices.Clone(decision.Properties)
	slices.Sort(fields)
	for i, name := range fields {
		if !sentenceKeyField(object, name) || i > 0 && fields[i-1] == name {
			return ErrInvalidModel
		}
	}
	object.Document.SentenceIdentity = &interfaces.SentenceBusinessIdentity{Properties: fields, ConfirmedBy: caller, ConfirmedAt: now}
	return nil
}

// An absent key is valid for a single record. The instance builder requires a
// usable key only when one type produces multiple records in the same sentence.
func SentenceIdentityProperties(object *interfaces.DraftObjectType) ([]string, error) {
	if !sentenceIdentityRole(object) {
		return nil, ErrInvalidModel
	}
	proof := object.Document.SentenceIdentity
	if proof == nil {
		return nil, nil
	}
	if proof.ConfirmedBy.ID == "" || proof.ConfirmedBy.Type != "user" || proof.ConfirmedAt <= 0 || len(proof.Properties) == 0 || len(proof.Properties) > 8 {
		return nil, ErrInvalidModel
	}
	fields := slices.Clone(proof.Properties)
	slices.Sort(fields)
	for i, name := range fields {
		if !sentenceKeyField(object, name) || i > 0 && fields[i-1] == name {
			return nil, ErrInvalidModel
		}
	}
	return fields, nil
}
