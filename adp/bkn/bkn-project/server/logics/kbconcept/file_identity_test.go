// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestFileBusinessNumberRequiresExplicitHumanChoiceAndKeepsManagedPrimaryKey(t *testing.T) {
	for _, domain := range []string{"contract", "patent", "inspection"} {
		t.Run(domain, func(t *testing.T) {
			object := &interfaces.DraftObjectType{ID: domain + "_file", PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "file"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "_id", Type: "string", ReadOnly: true}}}
			items := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{object}}
			if _, err := FileIdentityProperty(items, object); err == nil {
				t.Fatal("AI or a field name substituted for human confirmation")
			}
			owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
			if err := ConfirmFileIdentity(object, interfaces.FileIdentityDecision{Property: "number", Confirm: true}, owner, 10); err != nil {
				t.Fatal(err)
			}
			name, err := FileIdentityProperty(items, object)
			if err != nil || name != "number" || len(object.PrimaryKeys) != 1 || object.PrimaryKeys[0] != "_id" {
				t.Fatal("business number changed physical identity", err)
			}
			object.Properties[0].Type = "json"
			if _, err := FileIdentityProperty(items, object); err == nil {
				t.Fatal("incompatible field edit retained usable identity")
			}
		})
	}
}
func TestFileIdentityRejectsManagedMissingAndUnconfirmedFields(t *testing.T) {
	for _, scenario := range []string{"managed", "missing", "not_confirmed", "wrong_role", "anonymous"} {
		t.Run(scenario, func(t *testing.T) {
			object := &interfaces.DraftObjectType{Document: &interfaces.DraftDocumentModel{Role: "file"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "_id", Type: "string", ReadOnly: true}}}
			request := interfaces.FileIdentityDecision{Property: "number", Confirm: true}
			owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
			switch scenario {
			case "managed":
				request.Property = "_id"
			case "missing":
				request.Property = "invented"
			case "not_confirmed":
				request.Confirm = false
			case "wrong_role":
				object.Document.Role = "entity"
			case "anonymous":
				owner = interfaces.AccountInfo{}
			}
			if err := ConfirmFileIdentity(object, request, owner, 10); err == nil || object.Document.FileIdentity != nil {
				t.Fatal("untrusted business number accepted")
			}
		})
	}
}
