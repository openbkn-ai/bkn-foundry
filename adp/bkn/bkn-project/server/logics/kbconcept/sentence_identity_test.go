// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestSentenceIdentityIsOptionalHumanConfirmedAndKeepsManagedPrimaryKey(t *testing.T) {
	for _, role := range []string{"in_document", "fact", "event"} {
		object := &interfaces.DraftObjectType{PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: role}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "line", Type: "integer"}, {Name: "_id", Type: "string", ReadOnly: true}}}
		if fields, err := SentenceIdentityProperties(object); err != nil || len(fields) != 0 {
			t.Fatal("an absent conditional key was invalid", err)
		}
		owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
		if err := ConfirmSentenceIdentity(object, interfaces.SentenceIdentityDecision{Properties: []string{"number", "line"}, Confirm: true}, owner, 10); err != nil {
			t.Fatal(err)
		}
		fields, err := SentenceIdentityProperties(object)
		if err != nil || !reflect.DeepEqual(fields, []string{"line", "number"}) || !reflect.DeepEqual(object.PrimaryKeys, []string{"_id"}) {
			t.Fatal("human conditional key or managed primary key changed", fields, err)
		}
		if err = validateDocument(nil, object.Document, false); err == nil {
			t.Fatal("an AI proposal could forge human key confirmation")
		}
		if err = ConfirmSentenceIdentity(object, interfaces.SentenceIdentityDecision{Properties: []string{"_id"}, Confirm: true}, owner, 10); err == nil {
			t.Fatal("generated metadata was allowed as a business key")
		}
	}
}
