// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"testing"
)

// bknContextDocumentedFields lists the direct children of bkn_context that a
// client renders as rows of a parameter table. Every one of them must carry its
// own description: bkn_context is injected into every business tool, so a field
// without text shows up as an undocumented parameter on all of them.
var bknContextDocumentedFields = []string{
	"conversation_id", "interaction_id", "parent_operation_id",
	"causation_event_ids", "business_refs",
}

var businessRefDocumentedFields = []string{"ref_type", "ref_id", "version"}

type documentedSchema struct {
	Type                 string                     `json:"type"`
	Description          string                     `json:"description"`
	Enum                 []string                   `json:"enum"`
	MaxItems             int                        `json:"maxItems"`
	Required             []string                   `json:"required"`
	AdditionalProperties any                        `json:"additionalProperties"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Items                json.RawMessage            `json:"items"`
}

func decodeDocumentedSchema(t *testing.T, label string, raw json.RawMessage) documentedSchema {
	t.Helper()
	var schema documentedSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode %s: %v", label, err)
	}
	return schema
}

func bknContextOf(t *testing.T, label string, input json.RawMessage) documentedSchema {
	t.Helper()
	tool := decodeDocumentedSchema(t, label, input)
	raw, ok := tool.Properties["bkn_context"]
	if !ok {
		t.Fatalf("%s: input schema has no bkn_context", label)
	}
	return decodeDocumentedSchema(t, label+".bkn_context", raw)
}

// assertBKNContextIsDocumented is the acceptance guard for #1093: the served
// schema is the only place Studio and a model can read what the managed Trace
// parameters mean.
func assertBKNContextIsDocumented(t *testing.T, label string, input json.RawMessage) {
	t.Helper()
	context := bknContextOf(t, label, input)
	if context.Description == "" {
		t.Fatalf("%s: bkn_context itself must be described", label)
	}
	seen := map[string]string{context.Description: "bkn_context"}
	for _, field := range bknContextDocumentedFields {
		raw, ok := context.Properties[field]
		if !ok {
			t.Fatalf("%s: bkn_context must declare %s", label, field)
		}
		child := decodeDocumentedSchema(t, label+".bkn_context."+field, raw)
		if child.Description == "" {
			t.Fatalf("%s: bkn_context.%s has no description", label, field)
		}
		if owner, duplicate := seen[child.Description]; duplicate {
			t.Fatalf("%s: bkn_context.%s repeats the description of %s", label, field, owner)
		}
		seen[child.Description] = field
	}

	refs := decodeDocumentedSchema(t, label+".business_refs", context.Properties["business_refs"])
	item := decodeDocumentedSchema(t, label+".business_refs.items", refs.Items)
	if item.Description == "" {
		t.Fatalf("%s: business_refs.items has no description", label)
	}
	for _, field := range businessRefDocumentedFields {
		raw, ok := item.Properties[field]
		if !ok {
			t.Fatalf("%s: business_refs item must declare %s", label, field)
		}
		child := decodeDocumentedSchema(t, label+".business_refs.items."+field, raw)
		if child.Description == "" {
			t.Fatalf("%s: business_refs.items.%s has no description", label, field)
		}
		if owner, duplicate := seen[child.Description]; duplicate {
			t.Fatalf("%s: business_refs.items.%s repeats the description of %s", label, field, owner)
		}
		seen[child.Description] = "business_refs.items." + field
	}
}

// assertBKNContextValidationIsUnchanged pins what the descriptions must not
// touch. Documentation and the lifecycle contract share one declaration, so a
// wording change must not quietly widen a type, drop a required field or edit
// the ref_type vocabulary the evidence parser accepts.
func assertBKNContextValidationIsUnchanged(t *testing.T, label string, input json.RawMessage) {
	t.Helper()
	context := bknContextOf(t, label, input)
	if context.Type != "object" || context.AdditionalProperties != false {
		t.Fatalf("%s: bkn_context must stay a closed object: %#v", label, context)
	}
	if !sameStringSet(context.Required, []string{"conversation_id", "interaction_id"}) {
		t.Fatalf("%s: bkn_context required set changed: %v", label, context.Required)
	}
	for _, field := range []string{"conversation_id", "interaction_id", "parent_operation_id"} {
		child := decodeDocumentedSchema(t, label+"."+field, context.Properties[field])
		if child.Type != "string" {
			t.Fatalf("%s: bkn_context.%s must stay a string", label, field)
		}
	}
	causation := decodeDocumentedSchema(t, label+".causation_event_ids", context.Properties["causation_event_ids"])
	if causation.Type != "array" || causation.MaxItems != 64 {
		t.Fatalf("%s: causation_event_ids must stay a bounded array: %#v", label, causation)
	}

	refs := decodeDocumentedSchema(t, label+".business_refs", context.Properties["business_refs"])
	if refs.Type != "array" || refs.MaxItems != 64 {
		t.Fatalf("%s: business_refs must stay a bounded array: %#v", label, refs)
	}
	item := decodeDocumentedSchema(t, label+".business_refs.items", refs.Items)
	if item.AdditionalProperties != false ||
		!sameStringSet(item.Required, []string{"ref_type", "ref_id"}) {
		t.Fatalf("%s: business_refs item must stay a closed declaration: %#v", label, item)
	}
	refType := decodeDocumentedSchema(t, label+".business_refs.items.ref_type", item.Properties["ref_type"])
	if !sameStringSet(refType.Enum, []string{
		"knowledge_network", "object_type", "object_instance", "property", "relation_type",
		"data_resource", "metric", "logic", "function", "action_type", "action_instance",
	}) {
		t.Fatalf("%s: ref_type vocabulary changed: %v", label, refType.Enum)
	}
}

// TestBKNContextSchemaDocumentsEveryTraceField checks the schema every business
// tool publishes, in every served locale, because the locale overlay rebuilds
// the declaration after bkn_context is injected.
func TestBKNContextSchemaDocumentsEveryTraceField(t *testing.T) {
	for _, locale := range []string{defaultMCPLocale, "en-US"} {
		bundle := loadMCPLocaleBundle(locale)
		for toolKey := range allToolMeta() {
			if !isBusinessTool(toolKey) {
				continue
			}
			t.Run(locale+"/"+toolKey, func(t *testing.T) {
				input, _ := bundle.ToolSchemas(toolKey)
				label := locale + "/" + toolKey
				assertBKNContextIsDocumented(t, label, input)
				assertBKNContextValidationIsUnchanged(t, label, input)
			})
		}
	}
}
