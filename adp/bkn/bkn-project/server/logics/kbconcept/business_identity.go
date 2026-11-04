// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"

func businessIdentityShape(source, target *interfaces.DraftObjectType, relation *interfaces.DraftRelationType, pending bool) bool {
	if source == nil || target == nil || relation == nil || relation.Document == nil || relation.Document.Role != "relation" || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" || relation.Cardinality != interfaces.DRAFT_CARDINALITY_ONE_TO_ONE || source.Document == nil || source.Document.Role != "entity" || target.Document != nil || target.Table.ResourceID == "" || target.NeedsIdentity {
		return false
	}
	if !pending && source.NeedsIdentity {
		return false
	}
	mapping := relation.DocumentMapping
	if !(len(source.PrimaryKeys) == 1 && source.PrimaryKeys[0] == mapping.MentionProperty) && !(pending && source.NeedsIdentity && len(source.PrimaryKeys) == 0) {
		return false
	}
	from, to := property(source, mapping.MentionProperty), property(target, mapping.IdentityProperty)
	if from == nil || to == nil || from.ReadOnly || to.MappedField == "" || from.Type != to.Type {
		return false
	}
	switch to.Type {
	case "string", "text", "integer", "unsigned integer":
		return true
	default:
		return false
	}
}

// verifiedReferenceShape also permits foreign-key references to a checked table
// business key. These references do not identify the source as the same entity.
func verifiedReferenceShape(source, target *interfaces.DraftObjectType, relation *interfaces.DraftRelationType, pending bool) bool {
	if businessIdentityShape(source, target, relation, pending) {
		return true
	}
	if source == nil || target == nil || relation == nil || relation.Document == nil || relation.Document.Role != "relation" || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" || source.Document == nil || target.Document != nil || target.Table.ResourceID == "" || target.NeedsIdentity || len(target.PrimaryKeys) != 1 {
		return false
	}
	if relation.Cardinality != interfaces.DRAFT_CARDINALITY_ONE_TO_ONE && relation.Cardinality != interfaces.DRAFT_CARDINALITY_MANY_TO_ONE {
		return false
	}
	switch source.Document.Role {
	case "entity", "file", "in_document", "fact", "event":
	default:
		return false
	}
	mapping := relation.DocumentMapping
	// An entity's own identity is still governed by the same-business rule.
	if source.Document.Role == "entity" && (source.NeedsIdentity || (len(source.PrimaryKeys) == 1 && source.PrimaryKeys[0] == mapping.MentionProperty)) {
		return false
	}
	from, to := property(source, mapping.MentionProperty), property(target, mapping.IdentityProperty)
	if from == nil || to == nil || from.ReadOnly || to.MappedField == "" || from.Type != to.Type {
		return false
	}
	switch to.Type {
	case "string", "text", "integer", "unsigned integer":
		return true
	default:
		return false
	}
}

// VerifiedReferenceTarget returns the target of a reference eligible for live
// uniqueness verification. It does not grant same-business field authority.
func VerifiedReferenceTarget(items interfaces.DraftItems, relation *interfaces.DraftRelationType) *interfaces.DraftObjectType {
	source, target := identityEndpoints(items, relation)
	if !verifiedReferenceShape(source, target, relation, false) {
		return nil
	}
	return target
}

// BusinessIdentityTarget returns only an eligible same-business pair. It does
// not attest uniqueness; only the coordinator can create that checked proof.
func BusinessIdentityTarget(items interfaces.DraftItems, relation *interfaces.DraftRelationType) *interfaces.DraftObjectType {
	source, target := identityEndpoints(items, relation)
	if !businessIdentityShape(source, target, relation, false) {
		return nil
	}
	return target
}
func identityEndpoints(items interfaces.DraftItems, relation *interfaces.DraftRelationType) (source, target *interfaces.DraftObjectType) {
	if relation == nil {
		return nil, nil
	}
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
	return source, target
}

func businessIdentityReady(items interfaces.DraftItems, relation *interfaces.DraftRelationType) bool {
	target := VerifiedReferenceTarget(items, relation)
	if target == nil || relation.DocumentMapping.BusinessIdentity == nil {
		return false
	}
	proof := relation.DocumentMapping.BusinessIdentity
	field := property(target, relation.DocumentMapping.IdentityProperty)
	return proof.TargetObjectTypeID == target.ID && proof.ResourceID == target.Table.ResourceID && proof.Property == field.Name && proof.MappedField == field.MappedField && proof.Type == field.Type && proof.Rows >= 0 && proof.Distinct == proof.Rows && proof.Nulls == 0 && proof.Blanks == 0 && proof.ConfirmedBy.ID != "" && (proof.ConfirmedBy.Type == "user" || proof.ConfirmedBy.Type == "app") && proof.CheckedAt > 0
}
