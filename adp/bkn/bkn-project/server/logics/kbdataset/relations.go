// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// relationStorage distinguishes explicit, grounded business edges from a join
// inferred merely from co-occurrence. Many-to-many evidence uses the same
// existing indirect-relation mechanism, not a visible technical object type.
func relationStorage(relation *interfaces.DraftRelationType) bool {
	return relation != nil && relation.Adopted && relation.Document != nil && relation.DocumentMapping != nil &&
		(relation.DocumentMapping.Mode == "linking" || relation.DocumentMapping.Mode == "same_document" ||
			(relation.DocumentMapping.Mode == "evidence" && relation.Cardinality == interfaces.DRAFT_CARDINALITY_MANY_TO_MANY))
}

func objectKey(items interfaces.DraftItems, id string) (*interfaces.DraftProperty, error) {
	for _, object := range items.ObjectTypes {
		if object == nil || object.ID != id {
			continue
		}
		if !object.Adopted || object.NeedsIdentity {
			return nil, ErrInvalidLayout
		}
		name := "_id"
		if object.Document == nil {
			if len(object.PrimaryKeys) != 1 {
				return nil, ErrInvalidLayout
			}
			name = object.PrimaryKeys[0]
		}
		for _, field := range object.Properties {
			if field != nil && field.Name == name {
				return field, nil
			}
		}
	}
	return nil, ErrInvalidLayout
}

func relationLayout(items interfaces.DraftItems, relation *interfaces.DraftRelationType) (*Layout, error) {
	source, err := objectKey(items, relation.Source)
	if err != nil {
		return nil, err
	}
	target, err := objectKey(items, relation.Target)
	if err != nil {
		return nil, err
	}
	if (source.Type != "string" && source.Type != "integer" && source.Type != "unsigned integer" && source.Type != "text") ||
		(target.Type != "string" && target.Type != "integer" && target.Type != "unsigned integer" && target.Type != "text") {
		return nil, ErrInvalidLayout
	}
	fields := []struct{ name, typ, label string }{
		{"_id", "string", "Record identifier"}, {"source_record_id", source.Type, "Source object"}, {"target_record_id", target.Type, "Target object"},
		{"document_id", "string", "Source document"}, {"content_version_id", "string", "Content version"}, {"block_id", "string", "Source block"},
		{"source_start", "integer", "Quote start"}, {"source_end", "integer", "Quote end"}, {"source_quote", "text", "Source quote"},
		{"media_id", "string", "Image resource"}, {"media_occurrence_id", "string", "Image occurrence"},
		{"property_name", "string", "Supported attribute"}, {"claim_value", "json", "Source claim"}, {"evidence", "json", "Supporting evidence"},
		{"extraction_version", "string", "Extraction version"},
	}
	object := &interfaces.DraftObjectType{ID: relation.ID, Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_link"}, PrimaryKeys: []string{"_id"}}
	for _, field := range fields {
		object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: field.name, Type: field.typ, DisplayName: field.label, ReadOnly: true, SourceOfRecord: "document"})
	}
	return BuildLayout(object)
}

func bindRelation(items *interfaces.DraftItems, relation *interfaces.DraftRelationType, spec interfaces.DraftDatasetSpec) error {
	source, err := objectKey(*items, relation.Source)
	if err != nil {
		return err
	}
	target, err := objectKey(*items, relation.Target)
	if err != nil {
		return err
	}
	relation.LinkTable = &interfaces.DraftTable{ResourceID: spec.ResourceID, CatalogID: spec.CatalogID, Name: spec.Name}
	relation.SourceMapping = []interfaces.DraftFieldMapping{{SourceProperty: source.Name, TargetProperty: "source_record_id"}}
	relation.TargetMapping = []interfaces.DraftFieldMapping{{SourceProperty: "target_record_id", TargetProperty: target.Name}}
	relation.Properties = nil
	for _, field := range spec.Layout.Fields {
		if field.OriginalName == "_id" || field.OriginalName == "source_record_id" || field.OriginalName == "target_record_id" {
			continue
		}
		relation.Properties = append(relation.Properties, &interfaces.DraftProperty{Name: field.OriginalName, DisplayName: field.DisplayName, Type: field.Type, Description: field.Description, MappedField: field.Name, ReadOnly: true, SourceOfRecord: "document"})
	}
	return nil
}
