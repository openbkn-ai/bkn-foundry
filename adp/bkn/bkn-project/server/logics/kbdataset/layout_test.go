// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"reflect"
	"testing"
)

func layoutObject(role string) *interfaces.DraftObjectType {
	return &interfaces.DraftObjectType{ID: "business_record", Name: "Business record", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: role}, PrimaryKeys: []string{"_id"}, Properties: []*interfaces.DraftProperty{
		{Name: "_id", Type: "string", ReadOnly: true, SourceOfRecord: "document"},
		{Name: "number", Type: "string", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit business identifier"}},
		{Name: "source_quote", Type: "text", ReadOnly: true, SourceOfRecord: "document"},
	}}
}

func TestLayoutKeepsMetadataIDOutOfPhysicalSchema(t *testing.T) {
	for _, role := range []string{"file", "in_document", "fact", "event", "evidence_document", "evidence_block", "evidence_image"} {
		t.Run(role, func(t *testing.T) {
			object := layoutObject(role)
			layout, err := BuildLayout(object)
			if err != nil {
				t.Fatal(err)
			}
			physical := layout.Mapping["_id"]
			if physical == "" || physical == "_id" || !reflect.DeepEqual(layout.PrimaryKeys, []string{physical}) {
				t.Fatal("reserved metadata id used as physical primary key", layout)
			}
			for _, field := range layout.Fields {
				if field.Name == "_id" {
					t.Fatal("OpenSearch metadata id declared as an ordinary field")
				}
			}
			if layout.Mapping["source_quote"] != "source_quote" || len(layout.Fields) != len(object.Properties)+2 || layout.Mapping["model_fingerprint"] == "" || layout.Mapping["extraction_version"] == "" {
				t.Fatal("quote column or model field lost")
			}
			if object.Properties[0].MappedField != "" {
				t.Fatal("planning mutated the reviewed draft")
			}
		})
	}
}
func TestLayoutIDAliasCannotShadowBusinessFields(t *testing.T) {
	object := layoutObject("entity")
	object.PrimaryKeys = []string{"number"}
	first, err := BuildLayout(object)
	if err != nil {
		t.Fatal(err)
	}
	object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: first.Mapping["_id"], Type: "string"})
	next, err := BuildLayout(object)
	if err != nil {
		t.Fatal(err)
	}
	if next.Mapping["_id"] == first.Mapping["_id"] || next.Mapping[first.Mapping["_id"]] != first.Mapping["_id"] {
		t.Fatal("business field shadowed by the internal id mapping")
	}
	if !reflect.DeepEqual(next.PrimaryKeys, []string{"number"}) {
		t.Fatal("business identity replaced by metadata id")
	}
}
func TestLayoutIsCanonicalButTracksSchemaChanges(t *testing.T) {
	object := layoutObject("file")
	before, err := BuildLayout(object)
	if err != nil {
		t.Fatal(err)
	}
	object.Properties[0], object.Properties[1] = object.Properties[1], object.Properties[0]
	reordered, err := BuildLayout(object)
	if err != nil || !reflect.DeepEqual(before, reordered) {
		t.Fatal("field presentation order changed storage layout", err)
	}
	object.Properties[0].Type = "integer"
	changed, err := BuildLayout(object)
	if err != nil || before.Fingerprint == changed.Fingerprint {
		t.Fatal("type change did not change the schema fingerprint", err)
	}
}
func TestLayoutRejectsIncompleteOrCorruptModels(t *testing.T) {
	for _, scenario := range []string{"unadopted", "no_document", "unknown_role", "missing_key", "unknown_key", "duplicate", "nil_property", "unsupported_type", "unsafe_field", "wrong_local_identity"} {
		t.Run(scenario, func(t *testing.T) {
			object := layoutObject("file")
			switch scenario {
			case "unadopted":
				object.Adopted = false
			case "no_document":
				object.Document = nil
			case "unknown_role":
				object.Document.Role = "invented"
			case "missing_key":
				object.PrimaryKeys = nil
			case "unknown_key":
				object.PrimaryKeys = []string{"unknown"}
			case "duplicate":
				object.Properties = append(object.Properties, object.Properties[0])
			case "nil_property":
				object.Properties = append(object.Properties, nil)
			case "unsupported_type":
				object.Properties[1].Type = "invented"
			case "unsafe_field":
				object.Properties[1].Name = "field.with.path"
			case "wrong_local_identity":
				object.PrimaryKeys = []string{"number"}
			}
			if result, err := BuildLayout(object); err == nil || result != nil {
				t.Fatal("invalid model prepared", result, err)
			}
		})
	}
}
