// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"errors"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

var ErrUnverifiedCitation = errors.New("proposal citation does not match a locked debug sample")

// ValidateCitations verifies every quote against one actual source interval,
// using Unicode code points. The returned copy follows the selected source's
// original-text policy: disabled quotes become locations, never snapshots.
// It does not mutate transient input used by the model in this request.
func ValidateCitations(input *SampleInput, proposed []kb.Citation) ([]kb.Citation, error) {
	if input == nil || len(proposed) == 0 || len(proposed) > 20 {
		return nil, ErrUnverifiedCitation
	}
	includeOriginal := map[string]bool{}
	for _, file := range input.Files {
		includeOriginal[versionKey(file.KnowledgeBaseID, file.FileID, file.ContentVersionID)] = file.IncludeOriginal
	}
	result := make([]kb.Citation, 0, len(proposed))
	for _, quote := range proposed {
		key := versionKey(quote.KnowledgeBaseID, quote.FileID, quote.ContentVersionID)
		allowed, selected := includeOriginal[key]
		if !selected || quote.BlockID == "" || quote.Start < 0 || quote.End <= quote.Start || !utf8.ValidString(quote.Quote) || quote.Quote == "" || utf8.RuneCountInString(quote.Quote) > 4000 {
			return nil, ErrUnverifiedCitation
		}
		verified := false
		for _, fragment := range input.Request.Samples {
			ref := fragment.Citation
			if ref.KnowledgeBaseID != quote.KnowledgeBaseID || ref.VersionRef != quote.VersionRef || ref.BlockID != quote.BlockID || quote.Start < ref.Start || quote.End > ref.End {
				continue
			}
			text := []rune(ref.Quote)
			start, end := quote.Start-ref.Start, quote.End-ref.Start
			if end <= len(text) && string(text[start:end]) == quote.Quote {
				verified = true
				break
			}
		}
		if !verified {
			return nil, ErrUnverifiedCitation
		}
		if !allowed {
			quote.Quote = ""
		}
		result = append(result, quote)
	}
	return result, nil
}
