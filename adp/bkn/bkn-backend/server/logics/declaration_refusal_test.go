// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func refusalIssues(t *testing.T, err error) (*httperrors.HTTPError, []DeclarationIssueDetail) {
	t.Helper()
	var refusal *httperrors.HTTPError
	require.True(t, errors.As(err, &refusal), "%v", err)
	issues, _ := refusal.Metadata["issues"].([]DeclarationIssueDetail)
	return refusal, issues
}

// A refused save keeps the English text in error_details, as before, and carries one record per
// issue in metadata.issues: the rule, the definition, and the params.
func TestSaveRefusalCarriesIssueRecords(t *testing.T) {
	nr, _ := supplyNetwork().checker()
	saved := SavedDefinitions{RelationTypes: []*interfaces.RelationType{
		relationType("ships_from", "order", "warehouse", interfaces.RelationDeclarations{Cardinality: "many_to_one", Importance: "secondary"})}}

	err := nr.CheckRefusal(context.Background(), "kn1", "main", saved,
		berrors.BknBackend_RelationType_InvalidParameter_Declarations, berrors.BknBackend_RelationType_InternalError)
	refusal, issues := refusalIssues(t, err)

	assert.Equal(t, http.StatusBadRequest, refusal.HTTPCode)
	assert.Equal(t, `relation type "ships_via_site" fallback_for "ships_from": the relation it stands in for is secondary; a fallback path stands in for a primary relation`,
		refusal.ErrorDetails, "the English text stays a string in error_details")
	require.Len(t, issues, 1)
	assert.Equal(t, bknsdk.RuleFallbackTargetNotPrimary, issues[0]["rule"])
	assert.Equal(t, "ships_via_site", issues[0]["relation_type_id"], "the definition that refers first")
	assert.Equal(t, map[string]any{"fallback_for": "ships_from"}, issues[0]["params"])
	assert.Equal(t, []map[string]string{
		{"kind": "relation_type", "id": "ships_via_site"}, {"kind": "relation_type", "id": "ships_from"},
	}, issues[0]["involved"])
	assert.NotEmpty(t, issues[0]["message"])
}

// Several issues are listed in order and joined in the text, as they were.
func TestSaveRefusalListsEveryIssue(t *testing.T) {
	nr, _ := supplyNetwork().checker()
	saved := SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType("warehouse", deprecated(""))}}

	err := nr.CheckRefusal(context.Background(), "kn1", "main", saved,
		berrors.BknBackend_ObjectType_InvalidParameter_Declarations, berrors.BknBackend_ObjectType_InternalError)
	refusal, issues := refusalIssues(t, err)

	require.Len(t, issues, 2)
	for _, issue := range issues {
		assert.Equal(t, bknsdk.RuleEndpointDeprecated, issue["rule"])
		assert.Equal(t, map[string]any{"object_type": "warehouse"}, issue["params"])
	}
	assert.Equal(t, "ships_from", issues[0]["relation_type_id"])
	assert.Equal(t, "ships_via_site", issues[1]["relation_type_id"])
	assert.Contains(t, refusal.ErrorDetails, "; ")
}

// An error of the specification library becomes an issue of the definition that was saved; one
// without a rule still becomes one, with the rule declaration_invalid.
func TestSpecIssueDetail(t *testing.T) {
	err := bknsdk.ValidateObjectTypeDeclarations(&bknsdk.BknObjectType{
		BknObjectTypeFrontmatter: bknsdk.BknObjectTypeFrontmatter{ID: "order", Name: "order"},
		DataProperties:           []*bknsdk.DataProperty{{Name: "placed_at", Type: "string", Semantic: bknsdk.SemanticEventTime}},
	})
	require.Error(t, err)

	issue := SpecIssueDetail(interfaces.RESOURCE_TYPE_OBJECT_TYPE, "order", err)
	assert.Equal(t, bknsdk.RuleSemanticTypeMismatch, issue["rule"])
	assert.Equal(t, "order", issue["object_type_id"])
	assert.Equal(t, "placed_at", issue["property"])
	assert.Equal(t, map[string]any{"semantic": "event_time", "type": "string", "allowed": []string{"date", "datetime", "timestamp"}}, issue["params"])
	assert.Equal(t, err.Error(), issue["message"])

	other := SpecIssueDetail(interfaces.RESOURCE_TYPE_ACTION_TYPE, "ship", errors.New("not a rule"))
	assert.Equal(t, "declaration_invalid", other["rule"])
	assert.Equal(t, "ship", other["action_type_id"])
	assert.NotContains(t, other, "property")
	assert.NotContains(t, other, "params")
}

// An alias that is the name of another definition is an issue of the definition that holds the alias,
// whichever of the two the save wrote.
func TestAliasConflictDetail(t *testing.T) {
	incoming := []NamedDefinition{{ID: "client", Name: "client", Aliases: []string{"customer"}}}
	stored := []NamedDefinition{{ID: "customer", Name: "customer"}}

	conflict := FindAliasConflict("object type", incoming, stored, nil)
	require.NotNil(t, conflict)
	issue := AliasConflictDetail(interfaces.RESOURCE_TYPE_OBJECT_TYPE, conflict)
	assert.Equal(t, bknsdk.RuleAliasConflict, issue["rule"])
	assert.Equal(t, "client", issue["object_type_id"])
	assert.Equal(t, map[string]any{"alias": "customer", "conflicts_with": "customer"}, issue["params"])
	assert.Equal(t, `object type "client" alias "customer" is the name of object type "customer"`, issue["message"])

	reverse := FindAliasConflict("object type", []NamedDefinition{{ID: "customer", Name: "customer"}}, nil,
		[]NamedDefinition{{ID: "client", Name: "client", Aliases: []string{"customer"}}})
	require.NotNil(t, reverse)
	assert.Equal(t, "client", reverse.DefinitionID, "the definition that holds the alias, though the save wrote the other")
	assert.Equal(t, `object type "customer" is named "customer", which is an alias of object type "client"`, reverse.Message)

	assert.Nil(t, FindAliasConflict("object type", incoming, nil, nil))
	assert.Equal(t, "", AliasConflict("object type", incoming, nil, nil))
}

// A refused delete keeps the sentences in error_details.references and adds the records to
// metadata.references: what is referred to, who refers to it, and through which declaration.
func TestDeleteRefusalCarriesReferenceRecords(t *testing.T) {
	cases := []struct {
		name string
		kind string
		ids  []string
		want Reference
	}{
		{"replaced_by", interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"order"}, Reference{
			Kind: "object_type", ID: "order", ReferrerKind: "object_type", ReferrerID: "legacy_order", Via: "replaced_by",
			Message: `object type "order" is the lifecycle replaced_by of object type "legacy_order"`}},
		{"replaced_by of an action type", interfaces.RESOURCE_TYPE_ACTION_TYPE, []string{"ship"}, Reference{
			Kind: "action_type", ID: "ship", ReferrerKind: "action_type", ReferrerID: "ship_v1", Via: "replaced_by",
			Message: `action type "ship" is the lifecycle replaced_by of action type "ship_v1"`}},
		{"fallback_for", interfaces.RESOURCE_TYPE_RELATION_TYPE, []string{"ships_from"}, Reference{
			Kind: "relation_type", ID: "ships_from", ReferrerKind: "relation_type", ReferrerID: "ships_via_site", Via: "fallback_for",
			Message: `relation type "ships_from" is the fallback_for of relation type "ships_via_site"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nr, _ := supplyNetwork().checker()
			err := nr.DeleteRefusal(context.Background(), "kn1", "main", tc.kind, tc.ids,
				berrors.BknBackend_ObjectType_StillReferenced, berrors.BknBackend_ObjectType_InternalError)
			var refusal *httperrors.HTTPError
			require.True(t, errors.As(err, &refusal), "%v", err)

			assert.Equal(t, http.StatusConflict, refusal.HTTPCode)
			assert.Equal(t, map[string]any{"references": []string{tc.want.Message}}, refusal.ErrorDetails, "the sentences stay")
			assert.Equal(t, []Reference{tc.want}, refusal.Metadata["references"])
		})
	}
}
