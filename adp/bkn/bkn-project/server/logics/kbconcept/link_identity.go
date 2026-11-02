// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"

// EventParticipantsNeedRepair prevents an event from bypassing a blocked
// participant by leaving that relationship unadopted.
func EventParticipantsNeedRepair(items interfaces.DraftItems, object *interfaces.DraftObjectType) bool {
	if object == nil || object.Document == nil || object.Document.Role != "event" {
		return false
	}
	for _, id := range object.Document.ParticipantRelations {
		for _, relation := range items.RelationTypes {
			if relation != nil && relation.ID == id && relation.DocumentRepair != "" {
				return true
			}
		}
	}
	return false
}

// LinkIdentityReady is deliberately stricter than accepting an unadopted
// proposal: the target must have the exact identity a person confirmed before
// the relation can be adopted, compiled, or used to write links.
func LinkIdentityReady(items interfaces.DraftItems, relation *interfaces.DraftRelationType) bool {
	if relation == nil || relation.DocumentRepair != "" || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" {
		return false
	}
	var source, target *interfaces.DraftObjectType
	for _, object := range items.ObjectTypes {
		if object == nil {
			continue
		}
		if object.ID == relation.Source {
			source = object
		}
		if object.ID == relation.Target {
			target = object
		}
	}
	if source == nil || target == nil || source.Document == nil || target.NeedsIdentity {
		return false
	}
	if len(target.PrimaryKeys) != 1 || target.PrimaryKeys[0] != relation.DocumentMapping.IdentityProperty {
		return businessIdentityReady(items, relation)
	}
	from, to := property(source, relation.DocumentMapping.MentionProperty), property(target, relation.DocumentMapping.IdentityProperty)
	return from != nil && to != nil && !from.ReadOnly && from.Type == to.Type && !(source.ID == target.ID && from.Name == to.Name)
}

// SameDocumentScopeReady also checks stored drafts made before the scope rule.
// Ignored proposals do not prevent confirming unrelated adopted definitions.
func SameDocumentScopeReady(items interfaces.DraftItems, relation *interfaces.DraftRelationType) bool {
	if relation == nil || relation.DocumentRepair != "" || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "same_document" {
		return false
	}
	var source, target *interfaces.DraftObjectType
	for _, object := range items.ObjectTypes {
		if object == nil {
			continue
		}
		if object.ID == relation.Source {
			source = object
		}
		if object.ID == relation.Target {
			target = object
		}
	}
	return fileScopedObject(source) && fileScopedObject(target)
}
