// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strconv"
	"strings"
	"testing"
)

type sampleReader struct {
	pages []kb.SamplePage
	calls int
	refs  [][]kb.SampleVersion
	err   error
}

func (s *sampleReader) ReadSampleChunks(_ context.Context, refs []kb.SampleVersion, q kb.PageQuery) (kb.SamplePage, error) {
	s.refs = append(s.refs, refs)
	i := s.calls
	s.calls++
	if s.err != nil {
		return kb.SamplePage{}, s.err
	}
	if i >= len(s.pages) {
		return kb.SamplePage{}, errors.New("unexpected page")
	}
	return s.pages[i], nil
}
func selected() *interfaces.KBSelection {
	return &interfaces.KBSelection{ID: "selection", State: "active", Revision: 3, Files: []interfaces.KBResourceFile{
		{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8", ChunkCount: 1, SampleRole: "debug"},
		{KnowledgeBaseID: "2", FileID: "51", ContentVersionID: "20", SampleRole: "acceptance"},
	}}
}
func chunk() kb.SampleChunk {
	return kb.SampleChunk{SampleVersion: kb.SampleVersion{KnowledgeBaseID: "1", VersionRef: kb.VersionRef{FileID: "41", ContentVersionID: "8"}}, ChunkID: "91", Index: 0, Text: "订单甲😀é乙", Located: "exact", Spans: []kb.Span{{BlockID: "p1b1", Start: 10, End: 14}, {BlockID: "p1b2", Start: 30, End: 33}}}
}
func TestLoadOnlyDebugSamplesAndSplitDisjointUnicodeSpans(t *testing.T) {
	r := &sampleReader{pages: []kb.SamplePage{{Entries: []kb.SampleChunk{chunk()}, NextCursor: "next"}, {Entries: []kb.SampleChunk{}}}}
	input, err := LoadSamples(context.Background(), r, selected(), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 2 || len(r.refs[0]) != 1 || r.refs[0][0].FileID != "41" {
		t.Fatal("held-out files must never be read")
	}
	if len(input.Request.Samples) != 2 {
		t.Fatalf("disjoint spans cannot be fabricated into one quote: %d", len(input.Request.Samples))
	}
	a, b := input.Request.Samples[0].Citation, input.Request.Samples[1].Citation
	if a.Quote != "订单甲😀" || b.Quote != "é乙" || a.Start != 10 || b.Start != 30 {
		t.Fatalf("wrong Unicode offsets/quotes: %#v %#v", a, b)
	}
}
func TestLoadFailsAsAWholeOnSourceErrorsAndUntrustedPages(t *testing.T) {
	for _, reason := range []string{"acceptance", "version", "duplicate", "span", "oversize", "no_source"} {
		t.Run(reason, func(t *testing.T) {
			c := chunk()
			page := kb.SamplePage{Entries: []kb.SampleChunk{c}}
			switch reason {
			case "acceptance":
				page.Entries[0].KnowledgeBaseID = "2"
				page.Entries[0].FileID = "51"
				page.Entries[0].ContentVersionID = "20"
			case "version":
				page.Entries[0].ContentVersionID = "9"
			case "duplicate":
				page.Entries = append(page.Entries, c)
			case "span":
				page.Entries[0].Spans[0].End = 999
			case "oversize":
				page.Entries[0].Text = strings.Repeat("文", MaxSampleCharacters+1)
			case "no_source":
				page.Entries = nil
			}
			r := &sampleReader{pages: []kb.SamplePage{page, page}}
			if input, err := LoadSamples(context.Background(), r, selected(), "zh-CN"); err == nil || input != nil {
				t.Fatal("incomplete/untrusted input reached model caller")
			}
		})
	}
	r := &sampleReader{err: kb.ErrVersionGone}
	if _, err := LoadSamples(context.Background(), r, selected(), "zh-CN"); !errors.Is(err, kb.ErrVersionGone) {
		t.Fatalf("version loss must stay distinct: %v", err)
	}
}
func TestLoadBoundsAndReleasedSelections(t *testing.T) {
	for _, state := range []string{"released", "pending", "retained"} {
		s := selected()
		s.State = state
		if _, err := LoadSamples(context.Background(), nil, s, "zh-CN"); err == nil {
			t.Fatal(state)
		}
	}
	s := selected()
	s.Files[0].SampleRole = "acceptance"
	if _, err := LoadSamples(context.Background(), nil, s, "zh-CN"); !errors.Is(err, ErrNoDebugSamples) {
		t.Fatal(err)
	}
	s = selected()
	s.Files = nil
	for i := 0; i <= MaxSampleFiles; i++ {
		s.Files = append(s.Files, interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: strconv.Itoa(100 + i), ContentVersionID: strconv.Itoa(200 + i), ChunkCount: 1, SampleRole: "debug"})
	}
	if _, err := LoadSamples(context.Background(), nil, s, "zh-CN"); !errors.Is(err, ErrSampleLimit) {
		t.Fatalf("file bound not checked: %v", err)
	}
	s = selected()
	s.Files = append(s.Files, s.Files[0])
	if _, err := LoadSamples(context.Background(), nil, s, "zh-CN"); !errors.Is(err, ErrInvalidSample) {
		t.Fatalf("duplicate manifest not refused: %v", err)
	}
}

func TestRepeatedCursorIsDetectedWithDifferentChunks(t *testing.T) {
	first, second := chunk(), chunk()
	second.ChunkID = "92"
	second.Index = 1
	r := &sampleReader{pages: []kb.SamplePage{{Entries: []kb.SampleChunk{first}, NextCursor: "again"}, {Entries: []kb.SampleChunk{second}, NextCursor: "again"}}}
	s := selected()
	s.Files[0].ChunkCount = 2
	if _, err := LoadSamples(context.Background(), r, s, "zh-CN"); !errors.Is(err, ErrInvalidSample) || r.calls != 2 {
		t.Fatalf("repeated cursor branch not checked: %v", err)
	}
}
func TestZeroChunkDebugSampleHasAnActionableDistinctError(t *testing.T) {
	s := selected()
	s.Files[0].ChunkCount = 0
	r := &sampleReader{}
	if _, err := LoadSamples(context.Background(), r, s, "zh-CN"); !errors.Is(err, ErrNoLocatedSamples) || r.calls != 0 {
		t.Fatalf("empty sample must be distinguished before reading: %v", err)
	}
}

func TestPartialChunksKeepTheirLocatedFragmentsWithoutInventingGaps(t *testing.T) {
	c := chunk()
	c.Located = "partial"
	r := &sampleReader{pages: []kb.SamplePage{{Entries: []kb.SampleChunk{c}}}}
	input, err := LoadSamples(context.Background(), r, selected(), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Request.Samples) != 2 || input.Request.Samples[0].Citation.Quote != "订单甲😀" || input.Request.Samples[1].Citation.Quote != "é乙" {
		t.Fatal("partial spans were joined or silently dropped")
	}
}
