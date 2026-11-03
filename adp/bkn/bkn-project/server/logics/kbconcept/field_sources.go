// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"slices"
)

// ConfirmFieldSources is called only by the existing authenticated object-save
// path. AI model validation always resets new fields to document authority;
// ordinary edits preserve a prior human choice only for unchanged name/type.
func ConfirmFieldSources(object *interfaces.DraftObjectType, decisions []interfaces.FieldSourceDecision, caller interfaces.AccountInfo) error {
	if object == nil || object.Document == nil || object.Document.Role != "entity" || object.Document.DualSource != nil || caller.Type != "user" || caller.ID == "" || len(decisions) > 2000 {
		return ErrInvalidSourcePlan
	}
	seen := map[string]bool{}
	for _, decision := range decisions {
		field := property(object, decision.Property)
		if field == nil || field.ReadOnly || slices.Contains(object.PrimaryKeys, field.Name) || seen[field.Name] || !decision.Confirm || decision.SourceOfRecord != "document" && decision.SourceOfRecord != "fusion" {
			return ErrInvalidSourcePlan
		}
		seen[field.Name] = true
	}
	for _, decision := range decisions {
		property(object, decision.Property).SourceOfRecord = decision.SourceOfRecord
	}
	return nil
}
