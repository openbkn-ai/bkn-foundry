// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"slices"
	"strings"
)

// LinkTarget is part of a complete, permission-checked target snapshot. Its
// business identity has already been confirmed in the model and checked over
// the entire target table. The resolver checks uniqueness again before use.
type LinkTarget struct {
	Identity any
	// RecordID is the target object's original primary key. A distinct verified
	// business number is used for matching without replacing that primary key.
	RecordID any
}

type LinkSource struct {
	ID       string
	Identity any
	Term     string
}

// ConfirmedTermLink is a human decision in the network-wide term registry.
// Proposed AI matches must remain outside this input until explicitly reviewed.
type ConfirmedTermLink struct {
	NetworkID, Term, Decision string
	Target                    any
}

type ResolvedLink struct {
	SourceID string
	Target   any
	Method   string
}

type PendingLink struct{ SourceID, Reason string }

type LinkResolution struct {
	Eligible int
	Linked   []ResolvedLink
	Pending  []PendingLink
}

// ResolveLinks prefers exact verified business identities, then human-confirmed
// terms. It never invents an edge for an absent or ambiguous target. Eligible
// counts all source mentions, including unresolved ones; precision requires
// independent reference labels and cannot be inferred from match coverage.
func ResolveLinks(network string, sources []LinkSource, targets []LinkTarget, terms []ConfirmedTermLink) (LinkResolution, error) {
	result := LinkResolution{Linked: []ResolvedLink{}, Pending: []PendingLink{}}
	if strings.TrimSpace(network) == "" {
		return result, ErrInvalidBuild
	}
	targetIndex := map[string]LinkTarget{}
	recordIDs := map[string]bool{}
	for _, target := range targets {
		key, err := businessJoinKey(target.Identity)
		if err != nil {
			return LinkResolution{}, err
		}
		if _, exists := targetIndex[key]; exists {
			return LinkResolution{}, ErrInvalidBuild
		}
		if target.RecordID == nil {
			target.RecordID = target.Identity
		}
		recordKey, err := businessJoinKey(target.RecordID)
		if err != nil || recordIDs[recordKey] {
			return LinkResolution{}, ErrInvalidBuild
		}
		recordIDs[recordKey] = true
		targetIndex[key] = target
	}
	aliases := map[string]map[string]bool{}
	rejected := map[string]bool{}
	for _, term := range terms {
		if term.NetworkID != network || strings.TrimSpace(term.Term) == "" || (term.Decision != "confirmed" && term.Decision != "rejected") {
			return LinkResolution{}, ErrInvalidBuild
		}
		if term.Decision == "rejected" {
			rejected[term.Term] = true
			continue
		}
		key, err := businessJoinKey(term.Target)
		if err != nil {
			return LinkResolution{}, err
		}
		if aliases[term.Term] == nil {
			aliases[term.Term] = map[string]bool{}
		}
		// Removed targets do not become valid merely because an older registry
		// decision still refers to them. No fallback to a stale target is allowed.
		aliases[term.Term][key] = true
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source.ID == "" || seen[source.ID] {
			return LinkResolution{}, ErrInvalidBuild
		}
		seen[source.ID] = true
		result.Eligible++
		if source.Identity != nil {
			key, err := businessJoinKey(source.Identity)
			if err != nil {
				return LinkResolution{}, err
			}
			if target, exists := targetIndex[key]; exists {
				result.Linked = append(result.Linked, ResolvedLink{SourceID: source.ID, Target: target.RecordID, Method: "business_number"})
				continue
			}
		}
		reason := "unmatched"
		matches := aliases[source.Term]
		switch {
		case rejected[source.Term]:
			reason = "term_rejected"
		case len(matches) > 1:
			reason = "ambiguous_term"
		case len(matches) == 1:
			linked := false
			for key := range matches {
				if target, exists := targetIndex[key]; exists {
					result.Linked = append(result.Linked, ResolvedLink{SourceID: source.ID, Target: target.RecordID, Method: "confirmed_term"})
					linked = true
				}
			}
			if linked {
				continue
			}
		}
		result.Pending = append(result.Pending, PendingLink{SourceID: source.ID, Reason: reason})
	}
	slices.SortFunc(result.Linked, func(a, b ResolvedLink) int { return strings.Compare(a.SourceID, b.SourceID) })
	slices.SortFunc(result.Pending, func(a, b PendingLink) int { return strings.Compare(a.SourceID, b.SourceID) })
	return result, nil
}
