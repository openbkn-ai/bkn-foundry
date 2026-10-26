// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeRelation(t *testing.T, body string) *RelationType {
	t.Helper()
	var rt RelationType
	require.NoError(t, json.Unmarshal([]byte(body), &rt))
	return &rt
}

// A relation type records which declaration fields its JSON carried, null included, and
// mapping_rules.properties among them; properties written beside mapping_rules is not one.
func TestRelationTypeRecordsDeclaredFields(t *testing.T) {
	rt := decodeRelation(t, `{"id":"r","name":"n","type":"direct","mapping_rules":[]}`)
	for _, field := range RelationDeclarationFields {
		assert.False(t, rt.Declares(field), field)
	}

	rt = decodeRelation(t, `{"id":"r","reverse_name":null,"aliases":[],"lifecycle":{},"importance":"primary",
		"mapping_rules":{"properties":[]}}`)
	for _, field := range []string{DECLARATION_REVERSE_NAME, DECLARATION_ALIASES, DECLARATION_LIFECYCLE,
		DECLARATION_IMPORTANCE, DECLARATION_PROPERTIES} {
		assert.True(t, rt.Declares(field), field)
	}
	assert.False(t, rt.Declares(DECLARATION_CARDINALITY))

	rt = decodeRelation(t, `{"id":"r","properties":[],"mapping_rules":{"backing_data_source":null}}`)
	assert.False(t, rt.Declares(DECLARATION_PROPERTIES), "properties is a key of mapping_rules")

	rt = decodeRelation(t, `{"id":"r","cardinality":"many_to_one","constraints":{"required":["source"]},
		"lifecycle":{"status":"deprecated","reason":"r","replaced_by":"r2"}}`)
	assert.Equal(t, "many_to_one", rt.Cardinality)
	assert.Equal(t, []string{"source"}, rt.Constraints.Required)
	assert.Equal(t, &Lifecycle{Status: LIFECYCLE_DEPRECATED, Reason: "r", ReplacedBy: "r2"}, rt.Lifecycle)
}

// KeepUndeclared takes from the stored relation type every declaration the request left out; the
// relation properties only while both are indirect.
func TestRelationTypeKeepUndeclared(t *testing.T) {
	stored := &RelationType{RelationDeclarations: RelationDeclarations{
		ReverseName: "雇员", Aliases: []string{"在职"}, Cardinality: "many_to_one",
		Constraints: &RelationConstraints{Required: []string{"source"}},
		Lifecycle:   &Lifecycle{Status: LIFECYCLE_DEPRECATED, Reason: "r"},
		Importance:  "secondary", FallbackFor: "primary", Verification: "unconfirmed",
	}}
	stored.MappingRules = &InDirectMapping{Properties: []*DataProperty{{Name: "title"}}}

	update := decodeRelation(t, `{"id":"r","name":"新名"}`)
	update.MappingRules = &InDirectMapping{}
	update.KeepUndeclared(stored)
	assert.Equal(t, stored.RelationDeclarations, update.RelationDeclarations)
	assert.Equal(t, stored.RelationProperties(), update.RelationProperties())

	update = decodeRelation(t, `{"id":"r","reverse_name":"","aliases":null,"cardinality":"one_to_one",
		"lifecycle":null,"mapping_rules":{"properties":[]}}`)
	update.MappingRules = &InDirectMapping{}
	update.KeepUndeclared(stored)
	assert.Empty(t, update.ReverseName)
	assert.Empty(t, update.Aliases)
	assert.Equal(t, "one_to_one", update.Cardinality)
	assert.Nil(t, update.Lifecycle)
	assert.Equal(t, "unconfirmed", update.Verification, "left out, so kept")
	assert.Empty(t, update.RelationProperties(), "carried as [], so cleared")

	direct := decodeRelation(t, `{"id":"r"}`)
	direct.MappingRules = []Mapping{}
	direct.KeepUndeclared(stored)
	assert.Nil(t, direct.RelationProperties(), "a direct relation has no relation properties")
}

func TestRelationTypeNormalizeAndDeclaredFields(t *testing.T) {
	rt := &RelationType{RelationDeclarations: RelationDeclarations{
		Importance: IMPORTANCE_PRIMARY, Lifecycle: &Lifecycle{Status: LIFECYCLE_ACTIVE},
		Constraints: &RelationConstraints{}, Aliases: []string{},
	}}
	rt.MappingRules = &InDirectMapping{Properties: []*DataProperty{}}
	assert.Empty(t, rt.DeclaredFields(), "defaults declare nothing")
	rt.NormalizeDeclarations()
	assert.Equal(t, RelationDeclarations{}, rt.RelationDeclarations)
	assert.Nil(t, rt.RelationProperties())

	rt.Verification, rt.FallbackFor, rt.ReverseName = "confirmed", "p", "反"
	assert.Equal(t, []string{DECLARATION_REVERSE_NAME, DECLARATION_FALLBACK_FOR, DECLARATION_VERIFICATION}, rt.DeclaredFields())
}

func TestNormalizeLifecycle(t *testing.T) {
	assert.Nil(t, NormalizeLifecycle(nil))
	assert.Nil(t, NormalizeLifecycle(&Lifecycle{}))
	assert.Nil(t, NormalizeLifecycle(&Lifecycle{Status: LIFECYCLE_ACTIVE}))
	kept := &Lifecycle{Status: LIFECYCLE_ACTIVE, Reason: "left for the check to refuse"}
	assert.Same(t, kept, NormalizeLifecycle(kept))
	assert.Nil(t, LifecycleOf("", "", ""))
	assert.Equal(t, &Lifecycle{Status: LIFECYCLE_DEPRECATED, Reason: "r"}, LifecycleOf(LIFECYCLE_DEPRECATED, "r", ""))
}

// The relation property constraints of a relation type document are indexed as text, as the data
// property constraints of an object type are, and read back.
func TestIndexedConstraintsOfRelationProperties(t *testing.T) {
	doc := map[string]any{
		"mapping_rules": map[string]any{
			"properties": []any{
				map[string]any{"name": "since", "constraints": map[string]any{"range": map[string]any{"min": "2020-01-01"}}},
				map[string]any{"name": "title"},
			},
		},
	}
	require.NoError(t, EncodeIndexedConstraints(doc))
	props := doc["mapping_rules"].(map[string]any)["properties"].([]any)
	assert.Equal(t, `{"range":{"min":"2020-01-01"}}`, props[0].(map[string]any)["constraints"])
	assert.NotContains(t, props[1].(map[string]any), "constraints")

	require.NoError(t, DecodeIndexedConstraints(doc))
	decoded := props[0].(map[string]any)["constraints"].(*PropertyConstraints)
	assert.Equal(t, "2020-01-01", decoded.Range.Min)

	direct := map[string]any{"mapping_rules": []any{map[string]any{"source_property": map[string]any{"name": "a"}}}}
	require.NoError(t, EncodeIndexedConstraints(direct), "a direct relation's mapping rules are a list and left alone")
}
