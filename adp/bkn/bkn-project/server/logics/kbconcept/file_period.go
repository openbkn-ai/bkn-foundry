// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"

func fileDateField(object *interfaces.DraftObjectType, name string) bool {
	if name == "" {
		return true
	}
	field := property(object, name)
	return field != nil && !field.ReadOnly && (field.Type == "date" || field.Type == "datetime" || field.Type == "timestamp")
}

func ValidFilePeriod(object *interfaces.DraftObjectType, proof *interfaces.FileEffectivePeriod) bool {
	return object != nil && object.Document != nil && object.Document.Role == "file" && proof != nil && proof.ConfirmedBy.ID != "" && proof.ConfirmedBy.Type == "user" && proof.ConfirmedAt > 0 && (proof.From != "" || proof.Until != "") && proof.From != proof.Until && fileDateField(object, proof.From) && fileDateField(object, proof.Until)
}

func ConfirmFilePeriod(object *interfaces.DraftObjectType, decision interfaces.FilePeriodDecision, caller interfaces.AccountInfo, now int64) error {
	if object == nil || object.Document == nil || object.Document.Role != "file" || caller.ID == "" || caller.Type != "user" || now <= 0 {
		return ErrInvalidModel
	}
	if decision.Clear {
		if decision.From != "" || decision.Until != "" || decision.Confirm {
			return ErrInvalidModel
		}
		object.Document.FilePeriod = nil
		return nil
	}
	proof := &interfaces.FileEffectivePeriod{From: decision.From, Until: decision.Until, ConfirmedBy: caller, ConfirmedAt: now}
	if !decision.Confirm || !ValidFilePeriod(object, proof) {
		return ErrInvalidModel
	}
	object.Document.FilePeriod = proof
	return nil
}
