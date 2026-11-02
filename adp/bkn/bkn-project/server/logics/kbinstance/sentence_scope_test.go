// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
)

func sentenceRecords(t *testing.T) (*kb.FileInput, []*materializedRecord) {
	t.Helper()
	text := "Order A 100; order B 200."
	input := &kb.FileInput{Blocks: []kb.EvidenceBlock{{BlockID: "native", Start: 0, End: utf8.RuneCountInString(text)}}, Text: map[string]string{"native": text}}
	object := &interfaces.DraftObjectType{ID: "line", Document: &interfaces.DraftDocumentModel{Role: "in_document"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "amount", Type: "integer"}}}
	result := []*materializedRecord{}
	for i, quote := range []string{"Order A", "order B"} {
		anchor, err := locateInstance(input, InstanceCitation{BlockID: "native", Quote: quote}, false)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, &materializedRecord{Object: object, Anchor: anchor, Input: InstanceRecord{Values: map[string]InstanceValue{"number": {Value: []string{"A", "B"}[i]}, "amount": {Value: []json.Number{"200", "100"}[i]}}}})
	}
	return input, result
}
func TestMultipleSentenceRecordsNeedConfirmedKeysAndRemainStableAfterValueCorrection(t *testing.T) {
	input, records := sentenceRecords(t)
	if _, err := recordSentenceAnchors(input, records); !errors.Is(err, ErrSentenceIdentity) {
		t.Fatal("all field values silently substituted for a business key", err)
	}
	if err := kbconcept.ConfirmSentenceIdentity(records[0].Object, interfaces.SentenceIdentityDecision{Properties: []string{"number"}, Confirm: true}, interfaces.AccountInfo{ID: "owner", Type: "user"}, 10); err != nil {
		t.Fatal(err)
	}
	anchors, err := recordSentenceAnchors(input, records)
	if err != nil {
		t.Fatal(err)
	}
	before, err := AssignSentenceAnchors("network", "document", anchors)
	if err != nil {
		t.Fatal(err)
	}
	records[0].Input.Values["amount"] = InstanceValue{Value: json.Number("100")}
	records[1].Input.Values["amount"] = InstanceValue{Value: json.Number("200")}
	anchors, err = recordSentenceAnchors(input, records)
	if err != nil {
		t.Fatal(err)
	}
	after, err := AssignSentenceAnchors("network", "document", anchors)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("correcting ordinary values reassigned object identities", err)
	}
	records[1].Input.Values["number"] = InstanceValue{Value: "A"}
	if _, err = recordSentenceAnchors(input, records); !errors.Is(err, ErrSentenceIdentity) {
		t.Fatal("duplicate sentence business keys were silently merged", err)
	}
}
func TestSingleSentenceRecordRequiresNoExtraIdentityConfiguration(t *testing.T) {
	input, records := sentenceRecords(t)
	anchors, err := recordSentenceAnchors(input, records[:1])
	if err != nil || len(anchors) != 1 {
		t.Fatal("single record required an unnecessary configuration", err)
	}
}
func TestVerifiedSentenceIsIndependentOfQuoteLengthAndPreservesDecimalPoints(t *testing.T) {
	text := "Price 25.50 for A. Another record."
	input := &kb.FileInput{Blocks: []kb.EvidenceBlock{{BlockID: "b", Start: 10, End: 10 + utf8.RuneCountInString(text)}}, Text: map[string]string{"b": text}}
	for _, quote := range []string{"25.50", "for A", "Price 25.50 for A."} {
		anchor, err := locateInstance(input, InstanceCitation{BlockID: "b", Quote: quote}, false)
		if err != nil {
			t.Fatal(err)
		}
		sentence, err := sourceSentence(input, anchor)
		if err != nil || sentence != "Price 25.50 for A." {
			t.Fatal("quote selection or decimal point changed the sentence", sentence, err)
		}
	}
	anchor, _ := locateInstance(input, InstanceCitation{BlockID: "b", Quote: text}, false)
	if _, err := sourceSentence(input, anchor); err == nil {
		t.Fatal("a multi-sentence passage was accepted as a sentence anchor")
	}
}
