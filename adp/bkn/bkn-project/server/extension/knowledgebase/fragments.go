// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"errors"
	"unicode/utf8"
)

var ErrSampleSpanLayout = errors.New("knowledge base sample span layout is invalid")

// SampleFragment is one independently located native interval. Separators that
// KB span_text inserts between blocks are not evidence and belong to no span.
type SampleFragment struct {
	Span Span
	Text string
}

// SplitSampleText follows KB's native span_text contract: intervals in the same
// block are concatenated directly, while a block transition inserts exactly
// two newline code points. Native offsets remain unchanged. Reject missing,
// altered or trailing generated text rather than shifting a quote into it.
func SplitSampleText(chunk SampleChunk) ([]SampleFragment, error) {
	if !utf8.ValidString(chunk.Text) {
		return nil, ErrSampleSpanLayout
	}
	text := []rune(chunk.Text)
	fragments := make([]SampleFragment, 0, len(chunk.Spans))
	offset, previous := 0, ""
	for i, span := range chunk.Spans {
		if span.BlockID == "" || span.Start < 0 || span.End <= span.Start {
			return nil, ErrSampleSpanLayout
		}
		if i > 0 && span.BlockID != previous {
			if len(text)-offset < 2 || text[offset] != '\n' || text[offset+1] != '\n' {
				return nil, ErrSampleSpanLayout
			}
			offset += 2
		}
		size := span.End - span.Start
		if size > len(text)-offset {
			return nil, ErrSampleSpanLayout
		}
		fragments = append(fragments, SampleFragment{Span: span, Text: string(text[offset : offset+size])})
		offset += size
		previous = span.BlockID
	}
	if offset != len(text) {
		return nil, ErrSampleSpanLayout
	}
	return fragments, nil
}
