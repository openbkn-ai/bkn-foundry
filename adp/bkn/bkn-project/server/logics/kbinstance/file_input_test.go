// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

type nativeFileSource struct {
	ref    kb.SampleVersion
	text   string
	blocks []kb.EvidenceBlock
	media  []kb.EvidenceMedia
	err    error
	loop   bool
}

func (s *nativeFileSource) ReadBlocks(_ context.Context, ref kb.SampleVersion, _ kb.PageQuery) (kb.BlockPage, error) {
	if ref != s.ref {
		return kb.BlockPage{}, errors.New("foreign scope")
	}
	next := ""
	if s.loop {
		next = "repeated"
	}
	return kb.BlockPage{Entries: s.blocks, NextCursor: next}, s.err
}
func (s *nativeFileSource) ReadMedia(_ context.Context, ref kb.SampleVersion, _ kb.PageQuery) (kb.MediaPage, error) {
	if ref != s.ref {
		return kb.MediaPage{}, errors.New("foreign scope")
	}
	return kb.MediaPage{Entries: s.media}, s.err
}
func (s *nativeFileSource) ReadText(_ context.Context, ref kb.SampleVersion, span kb.Span) (string, error) {
	if ref != s.ref || span.BlockID != "native-1" {
		return "", errors.New("foreign location")
	}
	return s.text, s.err
}
func nativeFixture() *nativeFileSource {
	ref := kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "2", ContentVersionID: "3"}}
	return &nativeFileSource{ref: ref, text: "甲😀乙", blocks: []kb.EvidenceBlock{{SampleVersion: ref, BlockID: "native-1", Start: 10, End: 13}}, media: []kb.EvidenceMedia{{SampleVersion: ref, MediaID: "resource", OccurrenceID: "occurrence", ImageRef: "occurrence", BlockIDs: []string{"native-1"}}}}
}
func TestFileReadPreservesUnicodeLocationsAndDoesNotLoseNativeMedia(t *testing.T) {
	source := nativeFixture()
	input, err := ReadFileInput(context.Background(), source, source.ref)
	if err != nil || input.Text["native-1"] != "甲😀乙" || len(input.Media) != 1 || input.Blocks[0].Start != 10 || input.Blocks[0].End != 13 {
		t.Fatal("native evidence changed", err)
	}
	before, err := FileInputDigest(source.ref, input)
	if err != nil {
		t.Fatal(err)
	}
	copy := *input
	copy.Text = map[string]string{"native-1": "甲😀丙"}
	after, err := FileInputDigest(source.ref, &copy)
	if err != nil || after == before {
		t.Fatal("same-version source mutation not detected")
	}
	if !reflect.DeepEqual(input.Blocks, source.blocks) {
		t.Fatal("digest mutated source metadata")
	}
}
func TestFileReadRejectsPartialForeignOrUnboundedEvidence(t *testing.T) {
	for _, scenario := range []string{"revoked", "foreign_block", "foreign_media", "unknown_image_block", "duplicate_block", "duplicate_occurrence", "wrong_span", "short_text", "cycle", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			s := nativeFixture()
			switch scenario {
			case "revoked":
				s.err = &kb.SourceError{StatusCode: 403}
			case "foreign_block":
				s.blocks[0].ContentVersionID = "99"
			case "foreign_media":
				s.media[0].FileID = "99"
			case "unknown_image_block":
				s.media[0].BlockIDs = []string{"unknown"}
			case "duplicate_block":
				s.blocks = append(s.blocks, s.blocks[0])
			case "duplicate_occurrence":
				s.media = append(s.media, s.media[0])
			case "wrong_span":
				s.blocks[0].Start = -1
			case "short_text":
				s.text = "甲"
			case "cycle":
				s.loop = true
			case "oversized":
				s.text = strings.Repeat("x", maxFileInputBytes+1)
				s.blocks[0].End = s.blocks[0].Start + len(s.text)
			}
			if input, err := ReadFileInput(context.Background(), s, s.ref); err == nil || input != nil {
				t.Fatal("invalid file partially accepted", scenario)
			}
		})
	}
}
func TestStandaloneImageRequiresNoInventedOCRBlock(t *testing.T) {
	s := nativeFixture()
	s.blocks = nil
	s.text = ""
	s.media[0].BlockIDs = nil
	input, err := ReadFileInput(context.Background(), s, s.ref)
	if err != nil || len(input.Blocks) != 0 || len(input.Media) != 1 {
		t.Fatal("standalone image evidence lost", err)
	}
}
