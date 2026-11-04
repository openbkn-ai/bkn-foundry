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
		result = append(result, &materializedRecord{Object: object, Anchor: anchor, Input: InstanceRecord{Values: map[string]InstanceValue{"number": {Value: []string{"A", "B"}[i], Evidence: []InstanceCitation{{BlockID: "native", Quote: quote}}}, "amount": {Value: []json.Number{"200", "100"}[i]}}}})
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

func confirmedEvidenceRecord(t *testing.T) (*kb.FileInput, *materializedRecord) {
	t.Helper()
	text := "Item A row 7. Item A row 7 again. Delivery next week."
	input := &kb.FileInput{Blocks: []kb.EvidenceBlock{{BlockID: "native", Start: 0, End: utf8.RuneCountInString(text)}}, Text: map[string]string{"native": text}}
	object := &interfaces.DraftObjectType{ID: "observation", Document: &interfaces.DraftDocumentModel{Role: "in_document"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}, {Name: "line", Type: "string"}}}
	if err := kbconcept.ConfirmSentenceIdentity(object, interfaces.SentenceIdentityDecision{Properties: []string{"number", "line"}, Confirm: true}, interfaces.AccountInfo{ID: "owner", Type: "user"}, 10); err != nil {
		t.Fatal(err)
	}
	citation := InstanceCitation{BlockID: "native", Quote: "Item A row 7."}
	anchor, err := locateInstance(input, InstanceCitation{BlockID: "native", Quote: "Delivery next week."}, false)
	if err != nil {
		t.Fatal(err)
	}
	return input, &materializedRecord{Object: object, Anchor: anchor, Input: InstanceRecord{Values: map[string]InstanceValue{"number": {Value: "A", Evidence: []InstanceCitation{citation}}, "line": {Value: "7", Evidence: []InstanceCitation{citation}}}}}
}
func TestConfirmedKeyEvidenceDeterminesIdentityWithoutChangingOriginalEvidence(t *testing.T) {
	input, record := confirmedEvidenceRecord(t)
	original, _ := json.Marshal(record.Input.Values)
	anchors, err := recordSentenceAnchors(input, []*materializedRecord{record})
	if err != nil || anchors[0].Sentence != "Item A row 7." {
		t.Fatal("incidental AI anchor determined identity", anchors, err)
	}
	before, err := AssignSentenceAnchors("network", "document", anchors)
	if err != nil {
		t.Fatal(err)
	}
	originalAnchor := record.Anchor
	record.Anchor, err = locateInstance(input, InstanceCitation{BlockID: "native", Quote: "Item A row 7."}, false)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err = recordSentenceAnchors(input, []*materializedRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	after, err := AssignSentenceAnchors("network", "document", anchors)
	current, _ := json.Marshal(record.Input.Values)
	if err != nil || !reflect.DeepEqual(before, after) || string(original) != string(current) {
		t.Fatal("anchor choice changed identity or field evidence", err)
	}
	record.Anchor, err = locateInstance(input, InstanceCitation{BlockID: "native", Quote: input.Text["native"]}, false)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err = recordSentenceAnchors(input, []*materializedRecord{record})
	if err != nil || anchors[0].Sentence != "Item A row 7." {
		t.Fatal("incidental paragraph overrode confirmed key evidence", err)
	}
	record.Anchor = originalAnchor
	if _, err = recordSentenceAnchors(input, []*materializedRecord{record}); err != nil || record.Anchor != originalAnchor {
		t.Fatal("source evidence was replaced", err)
	}
}
func TestConfirmedKeyEvidenceRequiresOneSharedNativeSentence(t *testing.T) {
	for _, scenario := range []string{"missing", "missing_value", "multiple", "disjoint", "unknown_block", "duplicate_native_location"} {
		t.Run(scenario, func(t *testing.T) {
			input, record := confirmedEvidenceRecord(t)
			first := InstanceCitation{BlockID: "native", Quote: "Item A row 7."}
			second := InstanceCitation{BlockID: "native", Quote: "Item A row 7 again."}
			keys := []InstanceCitation{first}
			switch scenario {
			case "missing":
				keys = nil
			case "missing_value":
				record.Input.Values["number"] = InstanceValue{Value: nil, Evidence: keys}
			case "multiple":
				keys = []InstanceCitation{first, second}
			case "disjoint":
				record.Input.Values["number"] = InstanceValue{Value: "A", Evidence: []InstanceCitation{second}}
			case "unknown_block":
				keys = []InstanceCitation{{BlockID: "unknown", Quote: first.Quote}}
			case "duplicate_native_location":
				input.Blocks = append(input.Blocks, kb.EvidenceBlock{BlockID: "copy", Start: 0, End: utf8.RuneCountInString(first.Quote)})
				input.Text["copy"] = first.Quote
				keys = []InstanceCitation{first, {BlockID: "copy", Quote: first.Quote}}
			}
			record.Input.Values["line"] = InstanceValue{Value: "7", Evidence: keys}
			if scenario == "multiple" || scenario == "duplicate_native_location" {
				record.Input.Values["number"] = InstanceValue{Value: "A", Evidence: keys}
			}
			if _, err := recordSentenceAnchors(input, []*materializedRecord{record}); !errors.Is(err, ErrSentenceIdentity) {
				t.Fatal("missing or ambiguous key evidence guessed an anchor", err)
			}
		})
	}
}
