// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// reviewActions are the decisions on an item that say a person reviewed whether it is in: adopting
// it or not, choosing its identity, saving it as a whole, making it. Renaming or describing it alone
// does not.
var reviewActions = map[string]bool{
	interfaces.DRAFT_DECISION_ADOPT: true, interfaces.DRAFT_DECISION_UNADOPT: true,
	interfaces.DRAFT_DECISION_IDENTITY: true, interfaces.DRAFT_DECISION_SAVE: true,
	interfaces.DRAFT_DECISION_CREATE: true,
}

// itemKey names a draft item as decisions and refs do: kind/item_id.
func itemKey(kind string, id string) string {
	return kind + "/" + id
}

// refsOf reads the kind/item_id refs a decision lists, as written or as read back from JSON.
func refsOf(after any) []string {
	switch refs := after.(type) {
	case []string:
		return refs
	case []any:
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// reviews reads from the draft's decisions which items a person reviewed, by kind/item_id, with the
// last such decision, and why each item left out was, when they said.
func reviews(items *interfaces.DraftItems) (map[string]*interfaces.DraftReview, map[string]string) {
	reviewed, reasons := map[string]*interfaces.DraftReview{}, map[string]string{}
	for _, d := range items.Decisions {
		review := &interfaces.DraftReview{By: d.By, Time: d.Time}
		switch {
		case d.Kind == interfaces.DRAFT_KIND_DRAFT && d.Action == interfaces.DRAFT_DECISION_ACCEPT_SUGGESTIONS:
			for _, ref := range refsOf(d.After) {
				reviewed[ref] = review
			}
		case (d.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES || d.Kind == interfaces.DRAFT_KIND_RELATION_TYPES) &&
			reviewActions[d.Action]:
			key := itemKey(d.Kind, d.ItemID)
			reviewed[key] = review
			switch d.Action {
			case interfaces.DRAFT_DECISION_UNADOPT:
				reasons[key] = d.Reason
			case interfaces.DRAFT_DECISION_ADOPT:
				delete(reasons, key)
			}
		}
	}
	return reviewed, reasons
}

// unreviewed counts the object types and relation types of the draft no person reviewed.
func unreviewed(items *interfaces.DraftItems) int {
	reviewed, _ := reviews(items)
	n := 0
	for _, ot := range items.ObjectTypes {
		if reviewed[itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID)] == nil {
			n++
		}
	}
	for _, rt := range items.RelationTypes {
		if reviewed[itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID)] == nil {
			n++
		}
	}
	return n
}

// normalized is a name as concepts match items: its words, lower-cased, joined by a single space.
func normalized(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// neededByRequirements maps each draft item the ready specification needs, by kind/item_id, to the
// knowledge questions needing it and the concepts it is: those mapped to it, or, when a concept maps
// to nothing, whose term is the item's name or one of its aliases. Without a ready specification it
// is nil.
func neededByRequirements(req *interfaces.Requirement, found bool,
	items *interfaces.DraftItems) map[string]*interfaces.DraftNeededBy {
	if !found || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		return nil
	}
	needed := map[string]*interfaces.DraftNeededBy{}
	entry := func(key string) *interfaces.DraftNeededBy {
		if needed[key] == nil {
			needed[key] = &interfaces.DraftNeededBy{Questions: []interfaces.DraftNeedingQuestion{},
				Concepts: []interfaces.DraftNeedingConcept{}}
		}
		return needed[key]
	}
	for key, questions := range neededOf(req, found) {
		for _, q := range questions {
			e := entry(key)
			e.Questions = append(e.Questions, interfaces.DraftNeedingQuestion{ID: q.ID, Text: q.Text})
		}
	}
	kinds := map[string]string{
		interfaces.REQUIREMENT_ITEM_OBJECT_TYPE:   interfaces.DRAFT_KIND_OBJECT_TYPES,
		interfaces.REQUIREMENT_ITEM_RELATION_TYPE: interfaces.DRAFT_KIND_RELATION_TYPES,
	}
	// The items by their names and aliases, as a concept's term may name one.
	byName := map[string][]string{}
	name := func(key string, names ...string) {
		for _, n := range names {
			if n = normalized(n); n != "" && !slices.Contains(byName[n], key) {
				byName[n] = append(byName[n], key)
			}
		}
	}
	for _, ot := range items.ObjectTypes {
		name(itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID), append([]string{ot.Name}, ot.Aliases...)...)
	}
	for _, rt := range items.RelationTypes {
		name(itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID), append([]string{rt.Name}, rt.Aliases...)...)
	}
	for _, c := range req.Spec.Concepts {
		concept := interfaces.DraftNeedingConcept{ID: c.ID, Term: c.Term}
		if c.MapsTo != nil {
			key := itemKey(kinds[c.MapsTo.Kind], c.MapsTo.ID)
			entry(key).Concepts = append(entry(key).Concepts, concept)
			continue
		}
		for _, key := range byName[normalized(c.Term)] {
			entry(key).Concepts = append(entry(key).Concepts, concept)
		}
	}
	return needed
}

// catalogOf is the catalog an item comes from: an object type's table's; for a relation the catalog
// adding it, else its link table's or its source's.
func catalogOf(items *interfaces.DraftItems, kind string, id string) string {
	if kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
		// One added by a catalog reads a table of it.
		return findObjectType(items, id).Table.CatalogID
	}
	rt := findRelationType(items, id)
	switch {
	case rt.AddedByCatalog != "":
		return rt.AddedByCatalog
	case rt.LinkTable != nil:
		return rt.LinkTable.CatalogID
	}
	if source := findObjectType(items, rt.Source); source != nil {
		return source.Table.CatalogID
	}
	return ""
}

// candidatesOf lists the draft's items as candidates, those the specification needs first, then
// those not reviewed, then object types before relation types, each by name; only those from
// catalogID when it is given.
func candidatesOf(draft *interfaces.Draft, needed map[string]*interfaces.DraftNeededBy,
	catalogID string) *interfaces.DraftCandidates {
	items := &draft.Items
	reviewed, reasons := reviews(items)
	rows := map[string]int64{}
	if items.Inputs != nil {
		for _, t := range items.Inputs.Tables {
			rows[t.ResourceID] = t.Rows
		}
	}
	result := &interfaces.DraftCandidates{ProjectID: draft.ProjectID, DraftUpdateTime: draft.UpdateTime,
		Candidates: []interfaces.DraftCandidate{}, Counts: map[string]int{
			interfaces.CANDIDATE_STATE_PENDING: 0, interfaces.CANDIDATE_STATE_ADOPTED: 0, interfaces.CANDIDATE_STATE_EXCLUDED: 0,
		}}
	add := func(c interfaces.DraftCandidate) {
		if catalogID != "" && catalogOf(items, c.Kind, c.ID) != catalogID {
			return
		}
		key := itemKey(c.Kind, c.ID)
		c.Reviewed, c.NeededByRequirements = reviewed[key], needed[key]
		switch {
		case c.Reviewed == nil:
			c.State = interfaces.CANDIDATE_STATE_PENDING
		case c.Adopted:
			c.State = interfaces.CANDIDATE_STATE_ADOPTED
		default:
			c.State, c.ExcludeReason = interfaces.CANDIDATE_STATE_EXCLUDED, reasons[key]
		}
		result.Counts[c.State]++
		result.Candidates = append(result.Candidates, c)
	}
	for _, ot := range items.ObjectTypes {
		table, key := ot.Table, ot.Key
		add(interfaces.DraftCandidate{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Name: ot.Name, Adopted: ot.Adopted,
			AddedByCatalog: ot.AddedByCatalog, Table: &table, Rows: rows[table.ResourceID], NeedsIdentity: ot.NeedsIdentity, Key: &key})
	}
	for _, rt := range items.RelationTypes {
		evidence := rt.Evidence
		add(interfaces.DraftCandidate{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Name: rt.Name, Adopted: rt.Adopted,
			AddedByCatalog: rt.AddedByCatalog, Table: rt.LinkTable, Evidence: &evidence})
	}
	rank := func(c interfaces.DraftCandidate) [2]bool {
		return [2]bool{c.NeededByRequirements == nil, c.State != interfaces.CANDIDATE_STATE_PENDING}
	}
	slices.SortStableFunc(result.Candidates, func(a, b interfaces.DraftCandidate) int {
		ra, rb := rank(a), rank(b)
		for i := range ra {
			if ra[i] != rb[i] {
				if !ra[i] {
					return -1
				}
				return 1
			}
		}
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result
}

func (s *draftService) GetCandidates(ctx context.Context, token string, projectID string,
	catalogID string) (*interfaces.DraftCandidates, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetCandidates")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	draft, exist, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Draft not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
	}
	req, found, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Read requirement failed")
		return nil, internalError(ctx, "Read requirement failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return candidatesOf(draft, neededByRequirements(req, found, &draft.Items), catalogID), nil
}

func (s *draftService) AcceptSuggestions(ctx context.Context, token string, projectID string,
	req interfaces.DraftAcceptSuggestions, caller interfaces.AccountInfo) (*interfaces.DraftSuggestionsAccepted, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AcceptSuggestions")
	defer span.End()

	draft, err := s.readyDraft(ctx, token, projectID, req.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	items := &draft.Items
	reviewed, _ := reviews(items)
	result := &interfaces.DraftSuggestionsAccepted{Draft: draft, Accepted: []string{}, Skipped: []string{}}
	// What drafting could not settle waits for a person: an identity to choose, an end to adopt.
	for _, ot := range items.ObjectTypes {
		key := itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID)
		switch {
		case reviewed[key] != nil:
		case ot.NeedsIdentity:
			result.Skipped = append(result.Skipped, key)
		default:
			result.Accepted = append(result.Accepted, key)
		}
	}
	for _, rt := range items.RelationTypes {
		key := itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID)
		switch {
		case reviewed[key] != nil:
		case !(objectTypeAdopted(items, rt.Source) && objectTypeAdopted(items, rt.Target)):
			result.Skipped = append(result.Skipped, key)
		default:
			result.Accepted = append(result.Accepted, key)
		}
	}
	decisions := []*interfaces.DraftDecision{}
	if len(result.Accepted) > 0 {
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_DRAFT,
			Action: interfaces.DRAFT_DECISION_ACCEPT_SUGGESTIONS, After: result.Accepted})
	}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}
