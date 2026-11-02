// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// SentenceAnchor is derived from a validated business record and a verified
// source sentence. BusinessIdentity is the coordinator's canonical business-key
// encoding, never an ordinal or identifier accepted from an Agent.
type SentenceAnchor struct {
	ObjectTypeID     string
	Sentence         string
	BusinessIdentity string
}

func normalizedSentence(sentence string) (string, error) {
	if !utf8.ValidString(sentence) {
		return "", ErrInvalidIdentity
	}
	normalized := norm.NFC.String(strings.Join(strings.Fields(sentence), " "))
	if normalized == "" {
		return "", ErrInvalidIdentity
	}
	return normalized, nil
}

// AssignSentenceAnchors sorts unique business identities within each type and
// normalized sentence, implementing D6 without depending on proposal order.
// Duplicate mentions retain one identity and may keep independent evidence.
// The returned ids align with the caller's original order; invalid batches
// return no partial identities and neither input nor source offsets are edited.
func AssignSentenceAnchors(network, document string, anchors []SentenceAnchor) ([]string, error) {
	if _, err := identityDigest("bkn-anchor-batch-v1", network, document); err != nil {
		return nil, err
	}
	type group struct{ object, sentence string }
	groups := make(map[group][]string)
	membership := make([]group, len(anchors))
	normalized := map[string]string{}
	for i, anchor := range anchors {
		sentence, found := normalized[anchor.Sentence]
		var err error
		if !found {
			sentence, err = normalizedSentence(anchor.Sentence)
			if err == nil {
				normalized[anchor.Sentence] = sentence
			}
		}
		if err != nil || !utf8.ValidString(anchor.ObjectTypeID) || strings.TrimSpace(anchor.ObjectTypeID) == "" || !utf8.ValidString(anchor.BusinessIdentity) || strings.TrimSpace(anchor.BusinessIdentity) == "" {
			return nil, ErrInvalidIdentity
		}
		key := group{anchor.ObjectTypeID, sentence}
		membership[i] = key
		groups[key] = append(groups[key], anchor.BusinessIdentity)
	}
	for key, identities := range groups {
		slices.Sort(identities)
		identities = slices.Compact(identities)
		if uint64(len(identities)) > math.MaxUint32 {
			return nil, ErrInvalidIdentity
		}
		groups[key] = identities
	}
	ids := make([]string, len(anchors))
	for i, anchor := range anchors {
		key := membership[i]
		ordinal, found := slices.BinarySearch(groups[key], anchor.BusinessIdentity)
		if !found {
			return nil, ErrInvalidIdentity
		}
		id, err := SentenceID(network, key.object, document, key.sentence, uint32(ordinal))
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}
