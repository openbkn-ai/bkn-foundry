// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import "testing"

func TestDocumentIdentityIgnoresNetworkAndContentRevision(t *testing.T) {
	first, err := DocumentID("1", "41")
	if err != nil || len(first) != 64 {
		t.Fatal(first, err)
	}
	again, _ := DocumentID("1", "41")
	otherFile, _ := DocumentID("1", "42")
	otherKB, _ := DocumentID("2", "41")
	if first != again || first == otherFile || first == otherKB {
		t.Fatal("document identity is not stable and source-scoped")
	}
}
func TestSentenceAnchorsNormalizeLayoutWhitespaceWithoutFoldingMeaning(t *testing.T) {
	for _, pair := range [][2]string{{"Order O-1 is due on 1 May.", "  Order\nO-1  is due on 1 May.  "}, {"Café product is approved.", "Café product is approved."}, {"材料 A 符合标准。", "材料\tA 符合标准。"}} {
		first, err := SentenceID("logical-network", "promise", "doc-1", pair[0], 0)
		again, err2 := SentenceID("logical-network", "promise", "doc-1", pair[1], 0)
		if err != nil || err2 != nil || first != again {
			t.Fatal("layout/canonical Unicode changed the anchor", err, err2)
		}
	}
	a, _ := SentenceID("network", "fact", "doc", "Dose is 1 mg.", 0)
	for _, sentence := range []string{"Dose is 10 mg.", "Dose is 1 mg?", "Dose is １ mg.", "dose is 1 mg."} {
		b, _ := SentenceID("network", "fact", "doc", sentence, 0)
		if a == b {
			t.Fatal("semantic content folded into the same anchor")
		}
	}
}
func TestSentenceIdentitySeparatesTypeSourceAndSameSentenceOrdinal(t *testing.T) {
	base, _ := SentenceID("network", "part", "doc", "A contains B and C.", 0)
	for _, v := range []struct {
		network, typ, doc string
		ordinal           uint32
	}{{"other", "part", "doc", 0}, {"network", "other", "doc", 0}, {"network", "part", "other", 0}, {"network", "part", "doc", 1}} {
		id, _ := SentenceID(v.network, v.typ, v.doc, "A contains B and C.", v.ordinal)
		if id == base {
			t.Fatal("anchor namespace collision")
		}
	}
	left, _ := DocumentID("a:b", "c")
	right, _ := DocumentID("a", "b:c")
	if left == right {
		t.Fatal("ambiguous component encoding")
	}
}
func TestFileObjectsAndDerivedChangesKeepTheirDeclaredInputs(t *testing.T) {
	first, _ := FileObjectID("CT-001", "doc-1")
	again, _ := FileObjectID("CT-001", "doc-1")
	other, _ := FileObjectID("CT-001", "doc-2")
	if first != again || first == other {
		t.Fatal("file object identity changed or merged different documents")
	}
	beforeAfter, _ := DerivedChangeID("promise-1", "promise-2")
	afterBefore, _ := DerivedChangeID("promise-2", "promise-1")
	if beforeAfter == afterBefore {
		t.Fatal("ordered transition endpoints were ignored")
	}
	if _, err := SentenceID("network", "fact", "doc", " \n ", 0); err == nil {
		t.Fatal("empty sentence accepted")
	}
	if _, err := DocumentID("", "41"); err == nil {
		t.Fatal("empty source accepted")
	}
}
