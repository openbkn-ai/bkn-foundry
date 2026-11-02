// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"reflect"
	"testing"
)

func TestEvidenceRowsKeepResourceOccurrencesAndAllBlockProofs(t *testing.T) {
	ref := kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "2", ContentVersionID: "3"}}
	blocks := []kb.EvidenceBlock{{SampleVersion: ref, BlockID: "b1", Start: 0, End: 2}, {SampleVersion: ref, BlockID: "b2", Start: 2, End: 3}}
	media := []kb.EvidenceMedia{{SampleVersion: ref, MediaID: "same-native-resource", OccurrenceID: "first-position", ImageRef: "first-position", Kind: "asset", MimeType: "image/png", BlockIDs: []string{"b1", "b2"}}, {SampleVersion: ref, MediaID: "same-native-resource", OccurrenceID: "second-position", ImageRef: "second-position", Kind: "asset", MimeType: "image/png", BlockIDs: []string{"b2"}}}
	text := map[string]string{"b1": "甲😀", "b2": "乙"}
	rows, err := BuildEvidenceRows(ref, "Invoice", true, blocks, media, text)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Objects["evidence_media"]) != 1 || len(rows.Objects["evidence_image"]) != 2 || len(rows.Edges) != 5 {
		t.Fatal("media resource or occurrence proofs collapsed")
	}
	again, err := BuildEvidenceRows(ref, "Invoice", true, []kb.EvidenceBlock{blocks[1], blocks[0]}, []kb.EvidenceMedia{media[1], media[0]}, text)
	if err != nil || !reflect.DeepEqual(rows, again) {
		t.Fatal("native paging order changed stable writes", err)
	}
	gated, err := BuildEvidenceRows(ref, "Invoice", false, blocks, media, text)
	if err != nil || len(gated.Objects) != 1 || len(gated.Edges) != 0 {
		t.Fatal("original-text switch bypassed", err)
	}
	media[0].BlockIDs = append(media[0].BlockIDs, "outside-this-version")
	if _, err = BuildEvidenceRows(ref, "Invoice", true, blocks, media, text); err == nil {
		t.Fatal("foreign block proof accepted")
	}
}
func TestStandaloneImageHasResourceAndOccurrenceWithoutOCR(t *testing.T) {
	ref := kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "2", ContentVersionID: "3"}}
	media := []kb.EvidenceMedia{{SampleVersion: ref, MediaID: "original-resource", OccurrenceID: "original-position", ImageRef: "original-position", Kind: "original", MimeType: "image/jpeg"}}
	rows, err := BuildEvidenceRows(ref, "Photo", true, nil, media, nil)
	if err != nil || len(rows.Objects["evidence_image"]) != 1 || len(rows.Objects["evidence_block"]) != 0 || len(rows.Edges) != 1 {
		t.Fatal("standalone image incorrectly requires OCR", err)
	}
}
