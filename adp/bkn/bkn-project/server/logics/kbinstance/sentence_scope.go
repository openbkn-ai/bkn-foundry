// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"errors"
	"strings"
	"unicode"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
)

var ErrSentenceIdentity = errors.New("multiple sentence records need a confirmed, distinct business key")

type sourceSentencePart struct {
	start, end int
	text       string
}
type sourceSentenceBlock struct {
	base  int
	runes []rune
	parts []sourceSentencePart
}

func sourceSentenceIndex(input *kb.FileInput) map[string]sourceSentenceBlock {
	result := map[string]sourceSentenceBlock{}
	for _, block := range input.Blocks {
		runes := []rune(input.Text[block.BlockID])
		entry := sourceSentenceBlock{base: block.Start, runes: runes}
		appendPart := func(start, end int) {
			text, err := normalizedSentence(string(runes[start:end]))
			if err == nil {
				entry.parts = append(entry.parts, sourceSentencePart{start: start, end: end, text: text})
			}
		}
		segment := 0
		for i := 0; i < len(runes); i++ {
			c := runes[i]
			// Unicode code points describe source syntax; these are not localized UI labels.
			boundary := strings.ContainsRune("\u3002\uff01\uff1f!?", c)
			if c == '.' {
				decimal := i > 0 && i+1 < len(runes) && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(runes[i+1])
				boundary = !decimal && (i+1 == len(runes) || unicode.IsSpace(runes[i+1]) || strings.ContainsRune("\"'\u201d\u2019)]\uff09", runes[i+1]))
			}
			if !boundary {
				continue
			}
			last := i + 1
			for last < len(runes) && strings.ContainsRune("\"'\u201d\u2019)]\uff09", runes[last]) {
				last++
			}
			appendPart(segment, last)
			segment = last
			i = last - 1
		}
		if segment < len(runes) {
			appendPart(segment, len(runes))
		}
		result[block.BlockID] = entry
	}
	return result
}
func (index sourceSentenceBlock) part(anchor locatedEvidence) (sourceSentencePart, error) {
	start, end := anchor.Start-index.base, anchor.End-index.base
	if start < 0 || end > len(index.runes) || start >= end {
		return sourceSentencePart{}, ErrInvalidBuild
	}
	for start < end && unicode.IsSpace(index.runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(index.runes[end-1]) {
		end--
	}
	for _, part := range index.parts {
		if start >= part.start && end <= part.end {
			return part, nil
		}
	}
	return sourceSentencePart{}, ErrInvalidBuild
}
func (index sourceSentenceBlock) sentence(anchor locatedEvidence) (string, error) {
	part, err := index.part(anchor)
	return part.text, err
}

// Derive anchors from verified text once per file. Normalized sentence strings
// are shared across records, so a long paragraph is never copied per instance.
func sourceSentence(input *kb.FileInput, anchor locatedEvidence) (string, error) {
	return sourceSentenceIndex(input)[anchor.BlockID].sentence(anchor)
}
func recordSentenceAnchors(input *kb.FileInput, records []*materializedRecord) ([]SentenceAnchor, error) {
	anchors := make([]SentenceAnchor, len(records))
	index := sourceSentenceIndex(input)
	counts := map[[2]string]int{}
	for i, record := range records {
		fields, err := kbconcept.SentenceIdentityProperties(record.Object)
		if err != nil {
			return nil, ErrSentenceIdentity
		}
		var sentence string
		if len(fields) != 0 {
			sentence, err = confirmedKeySentence(input, index, record, fields)
		} else {
			sentence, err = index[record.Anchor.BlockID].sentence(record.Anchor)
		}
		if err != nil {
			return nil, err
		}
		anchors[i] = SentenceAnchor{ObjectTypeID: record.Object.ID, Sentence: sentence, BusinessIdentity: "single"}
		counts[[2]string{record.Object.ID, sentence}]++
	}
	seen := map[[3]string]bool{}
	for i, record := range records {
		anchor := &anchors[i]
		if counts[[2]string{anchor.ObjectTypeID, anchor.Sentence}] <= 1 {
			continue
		}
		fields, err := kbconcept.SentenceIdentityProperties(record.Object)
		if err != nil || len(fields) == 0 {
			return nil, ErrSentenceIdentity
		}
		values := map[string]any{}
		for _, name := range fields {
			value, exists := record.Input.Values[name]
			if !exists || value.Value == nil {
				return nil, ErrSentenceIdentity
			}
			if text, ok := value.Value.(string); ok && strings.TrimSpace(text) == "" {
				return nil, ErrSentenceIdentity
			}
			values[name] = value.Value
		}
		key, err := canonicalBusinessValue(values)
		if err != nil {
			return nil, err
		}
		anchor.BusinessIdentity = string(key)
		scope := [3]string{anchor.ObjectTypeID, anchor.Sentence, anchor.BusinessIdentity}
		if seen[scope] {
			return nil, ErrSentenceIdentity
		}
		seen[scope] = true
	}
	return anchors, nil
}

// Identity uses the unique native sentence shared by the confirmed key fields'
// existing evidence. Keep the model's original anchor and property citations;
// neither incidental quote selection nor identical text at another location
// may silently choose a different record identity.
func confirmedKeySentence(input *kb.FileInput, index map[string]sourceSentenceBlock, record *materializedRecord, fields []string) (string, error) {
	type location struct {
		block      string
		start, end int
	}
	var shared map[location]string
	for _, name := range fields {
		value, exists := record.Input.Values[name]
		if !exists || value.Value == nil || len(value.Evidence) == 0 {
			return "", ErrSentenceIdentity
		}
		if text, ok := value.Value.(string); ok && strings.TrimSpace(text) == "" {
			return "", ErrSentenceIdentity
		}
		candidates := map[location]string{}
		for _, citation := range value.Evidence {
			anchor, err := locateInstance(input, citation, false)
			if err != nil {
				return "", ErrSentenceIdentity
			}
			part, err := index[anchor.BlockID].part(anchor)
			if err != nil {
				return "", ErrSentenceIdentity
			}
			candidates[location{anchor.BlockID, part.start, part.end}] = part.text
		}
		if shared == nil {
			shared = candidates
		} else {
			for key := range shared {
				if _, exists := candidates[key]; !exists {
					delete(shared, key)
				}
			}
		}
	}
	if len(shared) != 1 {
		return "", ErrSentenceIdentity
	}
	for _, sentence := range shared {
		return sentence, nil
	}
	return "", ErrSentenceIdentity
}
