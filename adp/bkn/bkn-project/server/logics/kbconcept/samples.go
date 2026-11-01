// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbconcept validates transient sample input and untrusted model proposals.
// It never writes a draft, decides adoption, or creates an instance Dataset.
package kbconcept

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	MaxSampleFiles      = 20
	MaxSampleChunks     = 500
	MaxSampleFragments  = 1000
	MaxSampleCharacters = 120000
)

var (
	ErrNoDebugSamples   = errors.New("no locked debug samples")
	ErrNoLocatedSamples = errors.New("selected debug samples have no located text; choose readable samples")
	ErrSampleLimit      = errors.New("sample input exceeds extraction limits")
	ErrInvalidSample    = errors.New("invalid or incomplete sample input")
)

// SampleInput contains live text for this request only. Callers must not persist
// raw text, credentials or held-out samples in project drafts, decisions, process
// tables or logs. Builtin Agent calls use the existing Agent execution records.
type SampleInput struct {
	Request           kb.ConceptRequest
	SelectionID       string
	SelectionRevision int64
	Files             []interfaces.KBResourceFile
}

func versionKey(kbID, fileID, version string) string { return kbID + "/" + fileID + "/" + version }

// LoadSamples reads every locked debug file, with independent total input bounds.
// Partial source failures and oversized input never become a truncated success.
func LoadSamples(ctx context.Context, source kb.SampleSource, selection *interfaces.KBSelection, language string) (*SampleInput, error) {
	if selection == nil || selection.State != "active" {
		return nil, ErrNoDebugSamples
	}
	input := &SampleInput{Request: kb.ConceptRequest{NamingLanguage: language}, SelectionID: selection.ID, SelectionRevision: selection.Revision}
	refs := []kb.SampleVersion{}
	expected := map[string]bool{}
	expectedChunks := map[string]int64{}
	var totalChunks int64
	for _, file := range selection.Files {
		if file.SampleRole != "debug" {
			continue
		}
		key := versionKey(file.KnowledgeBaseID, file.FileID, file.ContentVersionID)
		if file.KnowledgeBaseID == "" || file.FileID == "" || file.ContentVersionID == "" || expected[key] {
			return nil, ErrInvalidSample
		}
		expected[key] = true
		if file.ChunkCount == 0 {
			return nil, ErrNoLocatedSamples
		}
		if file.ChunkCount < 0 {
			return nil, ErrInvalidSample
		}
		expectedChunks[key] = file.ChunkCount
		totalChunks += file.ChunkCount
		if file.ChunkCount > MaxSampleChunks || totalChunks > MaxSampleChunks {
			return nil, ErrSampleLimit
		}
		input.Files = append(input.Files, file)
		refs = append(refs, kb.SampleVersion{KnowledgeBaseID: file.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.FileID, ContentVersionID: file.ContentVersionID}})
	}
	if len(refs) == 0 {
		return nil, ErrNoDebugSamples
	}
	if len(refs) > MaxSampleFiles {
		return nil, ErrSampleLimit
	}
	if source == nil {
		return nil, kb.ErrUnavailable
	}
	cursor := ""
	seenCursors, seenChunks, seenSpans := map[string]bool{}, map[string]bool{}, map[string]string{}
	located := map[string]bool{}
	readCounts := map[string]int64{}
	chunks, characters := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := source.ReadSampleChunks(ctx, refs, kb.PageQuery{Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		if len(page.Entries) > 200 || (len(page.Entries) == 0 && page.NextCursor != "") {
			return nil, ErrInvalidSample
		}
		for _, chunk := range page.Entries {
			key := versionKey(chunk.KnowledgeBaseID, chunk.FileID, chunk.ContentVersionID)
			identity := key + "/" + chunk.ChunkID
			if !expected[key] || chunk.ChunkID == "" || chunk.Index < 0 || seenChunks[identity] || !utf8.ValidString(chunk.Text) || (chunk.Located != "exact" && chunk.Located != "partial") {
				return nil, ErrInvalidSample
			}
			seenChunks[identity] = true
			readCounts[key]++
			chunks++
			characters += utf8.RuneCountInString(chunk.Text)
			if chunks > MaxSampleChunks || characters > MaxSampleCharacters {
				return nil, ErrSampleLimit
			}
			text := []rune(chunk.Text)
			offset := 0
			for _, span := range chunk.Spans {
				size := span.End - span.Start
				if span.BlockID == "" || span.Start < 0 || size <= 0 || size > len(text)-offset {
					return nil, ErrInvalidSample
				}
				quote := string(text[offset : offset+size])
				offset += size
				if strings.TrimSpace(quote) == "" {
					continue
				}
				spanKey := fmt.Sprintf("%s/%s/%d/%d", key, span.BlockID, span.Start, span.End)
				if old, exists := seenSpans[spanKey]; exists {
					if old != quote {
						return nil, ErrInvalidSample
					}
					continue
				}
				if len(input.Request.Samples) >= MaxSampleFragments {
					return nil, ErrSampleLimit
				}
				seenSpans[spanKey] = quote
				located[key] = true
				// A multi-block chunk is split into separately located fragments. Never
				// fabricate a contiguous quote across the gaps between native spans.
				input.Request.Samples = append(input.Request.Samples, kb.Chunk{ID: chunk.ChunkID, Text: quote, Located: chunk.Located, Spans: []kb.Span{span}, Citation: kb.Citation{KnowledgeBaseID: chunk.KnowledgeBaseID, VersionRef: chunk.VersionRef, BlockID: span.BlockID, Start: span.Start, End: span.End, Quote: quote}})
			}
			if offset != len(text) {
				return nil, ErrInvalidSample
			}
		}
		if page.NextCursor == "" {
			break
		}
		if len(page.NextCursor) > 1024 || seenCursors[page.NextCursor] {
			return nil, ErrInvalidSample
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	for key := range expected {
		if readCounts[key] != expectedChunks[key] {
			return nil, ErrInvalidSample
		}
		if !located[key] {
			return nil, ErrNoLocatedSamples
		}
	}
	return input, nil
}
