// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"slices"
	"strings"
)

var ErrInvalidSourcePlan = errors.New("dual-source field decisions do not match the current model")

func sourceObject(items interfaces.DraftItems, id string) *interfaces.DraftObjectType {
	for _, object := range items.ObjectTypes {
		if object != nil && object.ID == id {
			return object
		}
	}
	return nil
}
func sourceRelation(items interfaces.DraftItems, id string) *interfaces.DraftRelationType {
	for _, relation := range items.RelationTypes {
		if relation != nil && relation.ID == id {
			return relation
		}
	}
	return nil
}

// SourcePlanSignature validates existing model placement. It never invents
// business fields, projects table values or silently moves a person's models.
func SourcePlanSignature(items interfaces.DraftItems, documentID string, choice interfaces.DualSourceChoice, adopted bool) (string, error) {
	document, system, file := sourceObject(items, documentID), sourceObject(items, choice.SystemObjectTypeID), sourceObject(items, choice.FileObjectTypeID)
	relation := sourceRelation(items, choice.RelationTypeID)
	if document == nil || document.Document == nil || document.Document.Role != "entity" || system == nil || system.Document != nil || file == nil || file.Document == nil || file.Document.Role != "file" || relation == nil || relation.Source != document.ID || relation.Target != system.ID || !businessIdentityShape(document, system, relation, false) || !LinkIdentityReady(items, relation) || len(choice.Fields) == 0 || len(choice.Fields) > 2000 {
		return "", ErrInvalidSourcePlan
	}
	if file.Document.SelectionID != document.Document.SelectionID || file.Document.SelectionRevision != document.Document.SelectionRevision {
		return "", ErrInvalidSourcePlan
	}
	if adopted && (!document.Adopted || !system.Adopted || !file.Adopted || !relation.Adopted) {
		return "", ErrInvalidSourcePlan
	}
	identity := document.PrimaryKeys[0]
	identityField := property(file, identity)
	if identityField == nil || identityField.ReadOnly || identityField.Type != property(document, identity).Type {
		return "", ErrInvalidSourcePlan
	}
	for _, field := range system.Properties {
		if field != nil && !field.ReadOnly && field.MappedField == "" {
			return "", ErrInvalidSourcePlan
		}
	}
	required := map[string]bool{}
	for _, object := range []*interfaces.DraftObjectType{system, document} {
		for _, field := range object.Properties {
			if field != nil && !field.ReadOnly {
				required[field.Name] = true
			}
		}
	}
	seen := map[string]bool{}
	involved := map[string]*interfaces.DraftObjectType{document.ID: document, system.ID: system, file.ID: file}
	rules := slices.Clone(choice.Fields)
	for _, rule := range rules {
		if rule.Name == "" || seen[rule.Name] {
			return "", ErrInvalidSourcePlan
		}
		seen[rule.Name] = true
		master, current, claim := property(system, rule.Name), property(document, rule.Name), property(file, rule.Name)
		if (master != nil && master.ReadOnly) || (current != nil && current.ReadOnly) || (claim != nil && claim.ReadOnly) {
			return "", ErrInvalidSourcePlan
		}
		if rule.SourceOfRecord == "fusion" {
			if !slices.Equal(rule.EvidenceOrder, []string{"effective_time", "evidence_strength", "recorded_at"}) {
				return "", ErrInvalidSourcePlan
			}
		} else if len(rule.EvidenceOrder) != 0 {
			return "", ErrInvalidSourcePlan
		}
		switch rule.Category {
		case "system_only":
			if master == nil || current != nil || claim != nil || rule.SourceOfRecord != "table" || rule.DestinationObjectTypeID != system.ID {
				return "", ErrInvalidSourcePlan
			}
		case "both":
			if master == nil || claim == nil || master.Type != claim.Type || (current != nil && rule.Name != identity) || rule.SourceOfRecord != "table" || rule.DestinationObjectTypeID != system.ID {
				return "", ErrInvalidSourcePlan
			}
		case "text_only":
			if master != nil || current == nil || (claim != nil && current.Type != claim.Type) || (rule.SourceOfRecord != "document" && rule.SourceOfRecord != "fusion") || rule.DestinationObjectTypeID != document.ID {
				return "", ErrInvalidSourcePlan
			}
		case "temporal":
			destination := sourceObject(items, rule.DestinationObjectTypeID)
			if current != nil || destination == nil || destination.Document == nil || (destination.Document.Role != "fact" && destination.Document.Role != "event") || destination.Document.SelectionID != document.Document.SelectionID || destination.Document.SelectionRevision != document.Document.SelectionRevision || rule.SourceOfRecord != "fusion" {
				return "", ErrInvalidSourcePlan
			}
			field := property(destination, rule.Name)
			if field == nil || field.ReadOnly || (claim != nil && field.Type != claim.Type) || (master != nil && field.Type != master.Type) || (adopted && !destination.Adopted) {
				return "", ErrInvalidSourcePlan
			}
			involved[destination.ID] = destination
		default:
			return "", ErrInvalidSourcePlan
		}
	}
	for name := range required {
		if !seen[name] {
			return "", ErrInvalidSourcePlan
		}
	}
	// Physical Dataset bindings, transient verification counts and display labels
	// do not change the field placement decision. Table field mappings do.
	type fieldSchema struct{ Name, Type, MappedField string }
	type objectSchema struct {
		ScopeDescription, TimeProperty    string
		ParticipantRelations              []string
		ID, Role, ResourceID, SelectionID string
		Revision                          int64
		Keys                              []string
		Fields                            []fieldSchema
	}
	schema := []objectSchema{}
	for _, object := range involved {
		value := objectSchema{ID: object.ID, Keys: slices.Clone(object.PrimaryKeys), Fields: []fieldSchema{}}
		if object.Document != nil {
			value.Role = object.Document.Role
			value.ScopeDescription = object.Description
			value.TimeProperty = object.Document.TimeProperty
			value.ParticipantRelations = slices.Clone(object.Document.ParticipantRelations)
			slices.Sort(value.ParticipantRelations)
			value.SelectionID = object.Document.SelectionID
			value.Revision = object.Document.SelectionRevision
		} else {
			value.ResourceID = object.Table.ResourceID
		}
		for _, field := range object.Properties {
			if field == nil || field.ReadOnly {
				continue
			}
			mapped := ""
			if object.Document == nil {
				mapped = field.MappedField
			}
			value.Fields = append(value.Fields, fieldSchema{field.Name, field.Type, mapped})
		}
		slices.SortFunc(value.Fields, func(a, b fieldSchema) int { return strings.Compare(a.Name, b.Name) })
		schema = append(schema, value)
	}
	slices.SortFunc(schema, func(a, b objectSchema) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(rules, func(a, b interfaces.SourceFieldRule) int { return strings.Compare(a.Name, b.Name) })
	raw, err := json.Marshal(struct {
		Document, System, File, Relation, Mention, Identity string
		Fields                                              []interfaces.SourceFieldRule
		Schema                                              []objectSchema
	}{document.ID, system.ID, file.ID, relation.ID, relation.DocumentMapping.MentionProperty, relation.DocumentMapping.IdentityProperty, rules, schema})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func SourcePlanReady(items interfaces.DraftItems, document *interfaces.DraftObjectType, adopted bool) bool {
	if document == nil || document.Document == nil || document.Document.DualSource == nil {
		return false
	}
	plan := document.Document.DualSource
	if plan.ConfirmedAt <= 0 || plan.ConfirmedBy.ID == "" || (plan.ConfirmedBy.Type != "user" && plan.ConfirmedBy.Type != "app") {
		return false
	}
	signature, err := SourcePlanSignature(items, document.ID, plan.DualSourceChoice, adopted)
	return err == nil && signature == plan.Signature
}
