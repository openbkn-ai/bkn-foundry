// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

const maxFileInputBytes = 1 << 20

// ReadFileInput reads whole native blocks; it never silently truncates a file
// or turns a source/permission failure into an empty successful extraction.
func ReadFileInput(ctx context.Context, source kb.EvidenceSource, ref kb.SampleVersion) (*kb.FileInput, error) {
	if source == nil || ref.KnowledgeBaseID == "" || ref.FileID == "" || ref.ContentVersionID == "" {
		return nil, ErrInvalidBuild
	}
	input := &kb.FileInput{Blocks: []kb.EvidenceBlock{}, Media: []kb.EvidenceMedia{}, Text: map[string]string{}}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := source.ReadBlocks(ctx, ref, kb.PageQuery{Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		input.Blocks = append(input.Blocks, page.Entries...)
		if len(input.Blocks) > 10000 {
			return nil, ErrInvalidBuild
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return nil, ErrInvalidBuild
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	bytes := 0
	blocks := map[string]bool{}
	for _, block := range input.Blocks {
		if block.SampleVersion != ref || block.BlockID == "" || blocks[block.BlockID] || block.Start < 0 || block.End < block.Start || block.End-block.Start > maxFileInputBytes {
			return nil, ErrInvalidBuild
		}
		blocks[block.BlockID] = true
		text := ""
		if block.Start != block.End {
			var err error
			text, err = source.ReadText(ctx, ref, kb.Span{BlockID: block.BlockID, Start: block.Start, End: block.End})
			if err != nil {
				return nil, err
			}
		}
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) != block.End-block.Start {
			return nil, ErrInvalidBuild
		}
		bytes += len(text)
		if bytes > maxFileInputBytes {
			return nil, ErrInvalidBuild
		}
		input.Text[block.BlockID] = text
	}
	cursor = ""
	seen = map[string]bool{}
	for {
		page, err := source.ReadMedia(ctx, ref, kb.PageQuery{Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		input.Media = append(input.Media, page.Entries...)
		if len(input.Media) > 4000 {
			return nil, ErrInvalidBuild
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return nil, ErrInvalidBuild
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	if _, err := FileInputDigest(ref, input); err != nil {
		return nil, err
	}
	return input, nil
}

// FileInputDigest rechecks native references and canonical ordering on replay.
// A source that changes bytes under an allegedly immutable version is refused.
func FileInputDigest(ref kb.SampleVersion, input *kb.FileInput) (string, error) {
	if input == nil || len(input.Blocks) > 10000 || len(input.Media) > 4000 || len(input.Text) != len(input.Blocks) {
		return "", ErrInvalidBuild
	}
	copy := *input
	copy.Blocks = slices.Clone(input.Blocks)
	copy.Media = slices.Clone(input.Media)
	blocks := map[string]bool{}
	size := 0
	for _, block := range copy.Blocks {
		text, exists := copy.Text[block.BlockID]
		if block.SampleVersion != ref || block.BlockID == "" || blocks[block.BlockID] || block.Start < 0 || block.End < block.Start || !exists || !utf8.ValidString(text) || utf8.RuneCountInString(text) != block.End-block.Start {
			return "", ErrInvalidBuild
		}
		blocks[block.BlockID] = true
		size += len(text)
		if size > maxFileInputBytes {
			return "", ErrInvalidBuild
		}
	}
	occurrences := map[string]bool{}
	for _, media := range copy.Media {
		if media.SampleVersion != ref || media.MediaID == "" || media.OccurrenceID == "" || media.ImageRef != media.OccurrenceID || occurrences[media.OccurrenceID] {
			return "", ErrInvalidBuild
		}
		occurrences[media.OccurrenceID] = true
		for _, id := range media.BlockIDs {
			if !blocks[id] {
				return "", ErrInvalidBuild
			}
		}
	}
	slices.SortFunc(copy.Blocks, func(a, b kb.EvidenceBlock) int { return strings.Compare(a.BlockID, b.BlockID) })
	slices.SortFunc(copy.Media, func(a, b kb.EvidenceMedia) int { return strings.Compare(a.OccurrenceID, b.OccurrenceID) })
	raw, err := json.Marshal(copy)
	if err != nil || len(raw) > 4*maxFileInputBytes {
		return "", ErrInvalidBuild
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
