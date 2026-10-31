// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// undecidedItem is an item drafting left out that no one decided on.
type undecidedItem struct {
	Kind   string
	ItemID string
	// Because is why drafting left it out: needs_identity for an object type, the relation's
	// proposed_because, or not_adopted.
	Because string
}

// undecidedNotAdopted is why an item is left out when drafting gave no other reason.
const undecidedNotAdopted = "not_adopted"

// undecidedItems lists the object types and relations drafting did not adopt and no one decided on,
// by kind then item ID. An item is decided by adopting or not adopting it, or by a confirmation
// that accepted it as undecided, until drafting the source changes leaves a relation out again;
// choosing an identity alone does not say whether it is in. They do
// not block confirming, but confirming them unawares would leave them out silently.
func undecidedItems(items *interfaces.DraftItems) []undecidedItem {
	decided := map[string]bool{}
	for _, d := range items.Decisions {
		switch {
		case d.Action == interfaces.DRAFT_DECISION_ADOPT || d.Action == interfaces.DRAFT_DECISION_UNADOPT:
			decided[d.Kind+"/"+d.ItemID] = true
		case d.Kind == interfaces.DRAFT_KIND_RELATION_TYPES && d.Action == interfaces.DRAFT_DECISION_SYNC:
			// Drafting the source changes left the relation out for a person to decide again,
			// whatever was decided on it before.
			delete(decided, d.Kind+"/"+d.ItemID)
		case d.Kind == interfaces.DRAFT_KIND_DRAFT && (d.Action == interfaces.DRAFT_DECISION_ACCEPT_UNDECIDED ||
			d.Action == interfaces.DRAFT_DECISION_ACCEPT_SUGGESTIONS):
			// Accepting the suggestions decides what it lists as drafting left it.
			if refs, ok := d.After.([]string); ok {
				for _, ref := range refs {
					decided[ref] = true
				}
			} else if refs, ok := d.After.([]any); ok {
				// Decisions read back from the database hold JSON arrays.
				for _, ref := range refs {
					if s, ok := ref.(string); ok {
						decided[s] = true
					}
				}
			}
		}
	}
	undecided := []undecidedItem{}
	for _, ot := range items.ObjectTypes {
		if ot.Adopted || decided[interfaces.DRAFT_KIND_OBJECT_TYPES+"/"+ot.ID] {
			continue
		}
		because := undecidedNotAdopted
		if ot.NeedsIdentity {
			because = interfaces.BLOCKER_NEEDS_IDENTITY
		}
		undecided = append(undecided, undecidedItem{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: ot.ID, Because: because})
	}
	for _, rt := range items.RelationTypes {
		if rt.Adopted || decided[interfaces.DRAFT_KIND_RELATION_TYPES+"/"+rt.ID] {
			continue
		}
		because := rt.ProposedBecause
		if because == "" {
			because = undecidedNotAdopted
		}
		undecided = append(undecided, undecidedItem{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: rt.ID, Because: because})
	}
	slices.SortFunc(undecided, func(a, b undecidedItem) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.ItemID, b.ItemID)
	})
	return undecided
}

// undecidedRefs names undecided items as kind/item_id, as an accept_undecided decision lists them.
func undecidedRefs(undecided []undecidedItem) []string {
	refs := make([]string, 0, len(undecided))
	for _, u := range undecided {
		refs = append(refs, u.Kind+"/"+u.ItemID)
	}
	return refs
}
