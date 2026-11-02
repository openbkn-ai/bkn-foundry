// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
)

type ReferenceLocation struct {
	BlockID string `json:"block_id"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

func visitReferenceCitations(proposal *FileExtraction, visit func(string, *InstanceCitation) error) error {
	for i := range proposal.Records {
		record := &proposal.Records[i]
		prefix := "record:" + strconv.Itoa(i)
		if err := visit(prefix+":anchor", &record.Anchor); err != nil {
			return err
		}
		for name, value := range record.Values {
			for j := range value.Evidence {
				if err := visit(prefix+":field:"+name+":"+strconv.Itoa(j), &value.Evidence[j]); err != nil {
					return err
				}
			}
			record.Values[name] = value
		}
	}
	for i := range proposal.Relations {
		for j := range proposal.Relations[i].Evidence {
			if err := visit("relation:"+strconv.Itoa(i)+":"+strconv.Itoa(j), &proposal.Relations[i].Evidence[j]); err != nil {
				return err
			}
		}
	}
	return nil
}
func referenceForStorage(review ReferenceReview, input *kb.FileInput, original bool) (*ReferenceReview, error) {
	raw, err := json.Marshal(review.Extraction)
	if err != nil {
		return nil, err
	}
	proposal, err := parseFileExtraction(string(raw))
	if err != nil {
		return nil, err
	}
	stored := review
	stored.Extraction = *proposal
	stored.Locations = map[string]ReferenceLocation{}
	err = visitReferenceCitations(&stored.Extraction, func(key string, citation *InstanceCitation) error {
		located, err := locateInstance(input, *citation, true)
		if err != nil {
			return err
		}
		stored.Locations[key] = ReferenceLocation{BlockID: located.BlockID, Start: located.Start, End: located.End}
		if !original {
			citation.Quote = ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

// Restore quotes only in memory after a fresh authorized read of the exact
// retained version. When originals are off, the Dataset stores locators alone.
func hydrateReference(review *ReferenceReview, input *kb.FileInput, original bool) error {
	if review == nil || input == nil {
		return ErrInvalidBuild
	}
	visited := 0
	err := visitReferenceCitations(&review.Extraction, func(key string, citation *InstanceCitation) error {
		location, exists := review.Locations[key]
		if !exists || citation.BlockID != location.BlockID || location.Start < 0 || location.End <= location.Start {
			return ErrInvalidBuild
		}
		visited++
		for _, block := range input.Blocks {
			if block.BlockID == location.BlockID {
				text := input.Text[block.BlockID]
				start, end := location.Start-block.Start, location.End-block.Start
				if !utf8.ValidString(text) || start < 0 || end > utf8.RuneCountInString(text) {
					return ErrInvalidBuild
				}
				quote := string([]rune(text)[start:end])
				if original && citation.Quote != quote || !original && citation.Quote != "" {
					return ErrInvalidBuild
				}
				citation.Quote = quote
				return nil
			}
		}
		return ErrInvalidBuild
	})
	if err != nil || visited != len(review.Locations) {
		return ErrInvalidBuild
	}
	return nil
}
