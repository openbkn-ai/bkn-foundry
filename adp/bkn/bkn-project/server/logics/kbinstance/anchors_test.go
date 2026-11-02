// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import "testing"

func TestSentenceAnchorsIgnoreProposalOrderAndKeepEveryBusinessIdentity(t *testing.T) {
	for _, domain := range []struct{ typ, sentence string }{{"delivery_commitment", "Orders A and B arrive in May."}, {"research_result", "Samples A and B passed the assay."}, {"component", "零件 A 和 B 符合标准。"}} {
		original := []SentenceAnchor{{ObjectTypeID: domain.typ, Sentence: domain.sentence, BusinessIdentity: "B"}, {ObjectTypeID: domain.typ, Sentence: domain.sentence, BusinessIdentity: "A"}, {ObjectTypeID: domain.typ, Sentence: domain.sentence, BusinessIdentity: "B"}}
		ids, err := AssignSentenceAnchors("lineage", "document", original)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := SentenceID("lineage", domain.typ, "document", domain.sentence, 0)
		b, _ := SentenceID("lineage", domain.typ, "document", domain.sentence, 1)
		if ids[0] != b || ids[1] != a || ids[2] != b {
			t.Fatal("model order or duplicate mentions changed identity")
		}
		reordered, err := AssignSentenceAnchors("lineage", "document", []SentenceAnchor{original[1], original[0]})
		if err != nil || reordered[0] != a || reordered[1] != b {
			t.Fatal("reordered extraction changed identity", err)
		}
		if original[0].BusinessIdentity != "B" {
			t.Fatal("caller input mutated")
		}
	}
}

func TestSentenceAnchorGroupsUseCanonicalWhitespaceAndSeparateTypes(t *testing.T) {
	anchors := []SentenceAnchor{{ObjectTypeID: "part", Sentence: "Café A\n and B.", BusinessIdentity: "B"}, {ObjectTypeID: "part", Sentence: "Café A and B.", BusinessIdentity: "A"}, {ObjectTypeID: "claim", Sentence: "Café A and B.", BusinessIdentity: "B"}}
	ids, err := AssignSentenceAnchors("lineage", "document", anchors)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := SentenceID("lineage", "part", "document", "Café A and B.", 1)
	other, _ := SentenceID("lineage", "claim", "document", "Café A and B.", 0)
	if ids[0] != want || ids[2] != other || ids[0] == ids[2] {
		t.Fatal("normalization or type namespace is inconsistent")
	}
}

func TestInvalidSentenceAnchorBatchIsRejectedWithoutPartialIdentities(t *testing.T) {
	for _, invalid := range []SentenceAnchor{{ObjectTypeID: "part", Sentence: "A exists."}, {ObjectTypeID: "part", Sentence: "  ", BusinessIdentity: "A"}, {Sentence: "A exists.", BusinessIdentity: "A"}, {ObjectTypeID: "part", Sentence: "A exists.", BusinessIdentity: string([]byte{0xff})}} {
		ids, err := AssignSentenceAnchors("lineage", "document", []SentenceAnchor{{ObjectTypeID: "part", Sentence: "A exists.", BusinessIdentity: "A"}, invalid})
		if err == nil || ids != nil {
			t.Fatal("partially valid batch accepted")
		}
	}
}
