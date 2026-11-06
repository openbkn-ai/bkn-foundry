// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// conceptReasonMaxLength bounds the reason a person gives with a judgment, in characters: as long
// as a reason for leaving an item out may be, since it goes with that decision too.
const conceptReasonMaxLength = interfaces.DRAFT_REASON_MAX_LENGTH

// requirementConceptKey names a requirement concept no draft item is.
func requirementConceptKey(id string) string {
	return "requirement_concepts/" + id
}

// blockers are the refusals of adopting an item that a judgment waits out: the item can be adopted
// once a person settles them on the model, so the judgment stands meanwhile.
var blockers = map[string]string{
	perrors.BknProject_Draft_NeedsIdentity:               interfaces.CONCEPT_BLOCKED_NEEDS_IDENTITY,
	perrors.BknProject_Draft_BusinessIdentityNotVerified: interfaces.CONCEPT_BLOCKED_NEEDS_IDENTITY,
	perrors.BknProject_Draft_EndpointNotAdopted:          interfaces.CONCEPT_BLOCKED_END_NOT_ADOPTED,
	perrors.BknProject_Draft_DocumentRelationNeedsRepair: interfaces.CONCEPT_BLOCKED_REPAIR,
}

// blockerOf is why adopting was refused, when it is a refusal a judgment waits out.
func blockerOf(err error) (string, bool) {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return "", false
	}
	blocker, ok := blockers[httpErr.ErrorCode]
	return blocker, ok
}

// adoptsAs tells whether a verdict puts an item of the kind in the concept model: an object type
// as an object or an event, a relation type as a relation, a value type as a value list.
func adoptsAs(kind string, verdict string) bool {
	switch kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		return verdict == interfaces.CONCEPT_VERDICT_OBJECT || verdict == interfaces.CONCEPT_VERDICT_EVENT
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		return verdict == interfaces.CONCEPT_VERDICT_RELATION
	case interfaces.DRAFT_KIND_VALUE_TYPES:
		return verdict == interfaces.CONCEPT_VERDICT_VALUES
	}
	return false
}

// isEvidence tells an evidence item: what locates a business item's original text in its document,
// such as the file, block or image it was read from. It follows its business items into and out of
// the model and is no concept of its own.
func isEvidence(document *interfaces.DraftDocumentModel) bool {
	return document != nil && strings.HasPrefix(document.Role, "evidence_")
}

// requirementVerdicts reads a requirement concept's category as a verdict; evidence has none.
var requirementVerdicts = map[string]string{
	"object":       interfaces.CONCEPT_VERDICT_OBJECT,
	"event":        interfaces.CONCEPT_VERDICT_EVENT,
	"property":     interfaces.CONCEPT_VERDICT_PROPERTY,
	"relation":     interfaces.CONCEPT_VERDICT_RELATION,
	"kinetic":      interfaces.CONCEPT_VERDICT_KINETIC,
	"not_ontology": interfaces.CONCEPT_VERDICT_NONE,
}

// conceptJudgment is the last judgment of a concept, and the merges standing.
type conceptJudgment struct {
	verdict string
	reason  string
	review  *interfaces.DraftReview
}

func conceptDecisions(items *interfaces.DraftItems) (map[string]conceptJudgment, map[string]string) {
	judged, mergedInto := map[string]conceptJudgment{}, map[string]string{}
	for _, d := range items.Decisions {
		if d.Kind != interfaces.DRAFT_KIND_CONCEPTS {
			continue
		}
		switch d.Action {
		case interfaces.DRAFT_DECISION_JUDGE:
			if verdict, ok := d.After.(string); ok {
				judged[d.ItemID] = conceptJudgment{verdict: verdict, reason: d.Reason,
					review: &interfaces.DraftReview{By: d.By, Time: d.Time}}
			}
		case interfaces.DRAFT_DECISION_MERGE:
			if into, ok := d.After.(string); ok {
				mergedInto[d.ItemID] = into
			}
		case interfaces.DRAFT_DECISION_UNMERGE:
			delete(mergedInto, d.ItemID)
		}
	}
	return judged, mergedInto
}

// adoptionBlocker tries adopting the item on a copy of the draft and says what refused it, if a
// refusal a judgment waits out did.
func adoptionBlocker(ctx context.Context, items *interfaces.DraftItems, kind string, id string) string {
	raw, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	var copied interfaces.DraftItems
	if json.Unmarshal(raw, &copied) != nil {
		return ""
	}
	adopted := true
	_, err = applyEdit(ctx, &copied, kind, id, interfaces.DraftItemEdit{Adopted: &adopted})
	blocker, _ := blockerOf(err)
	return blocker
}

// conceptsOf projects the concepts of the draft, when it is ready, and of the ready requirement
// specification: one per draft item that is no evidence, with the requirement concepts that are it
// among its sources, then one per requirement concept no item is. A person's judgments and merges
// are read from the draft's decisions.
func conceptsOf(ctx context.Context, projectID string, draft *interfaces.Draft, req *interfaces.Requirement,
	found bool) *interfaces.DraftConcepts {
	result := &interfaces.DraftConcepts{ProjectID: projectID, Concepts: []interfaces.DraftConcept{},
		Counts: map[string]int{}}
	items := &interfaces.DraftItems{}
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		items, result.DraftUpdateTime = &draft.Items, draft.UpdateTime
	}
	judged, mergedInto := conceptDecisions(items)
	reviewed, excluded := reviews(items)

	byKey := map[string]*interfaces.DraftConcept{}
	order := []string{}
	add := func(c interfaces.DraftConcept) {
		byKey[c.Key] = &c
		order = append(order, c.Key)
	}
	evidenceObjects := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		if isEvidence(ot.Document) {
			evidenceObjects[ot.ID] = true
			continue
		}
		c := interfaces.DraftConcept{Key: itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID), Name: ot.Name,
			Description: ot.Description, Suggested: interfaces.CONCEPT_VERDICT_OBJECT,
			Item: &interfaces.DraftConceptItem{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Adopted: ot.Adopted}}
		if ot.Document == nil {
			c.SuggestedBecause = "table_entity"
			c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE,
				ID: ot.Table.ResourceID, Name: ot.Table.Name}}
		} else {
			if ot.Document.Role == "event" {
				c.Suggested = interfaces.CONCEPT_VERDICT_EVENT
			}
			c.SuggestedBecause = "document_" + ot.Document.Role
			c.Sources = documentSources(ot.Document)
		}
		add(c)
	}
	for _, rt := range items.RelationTypes {
		if isEvidence(rt.Document) || evidenceObjects[rt.Source] || evidenceObjects[rt.Target] {
			continue
		}
		c := interfaces.DraftConcept{Key: itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID), Name: rt.Name,
			Description: rt.Description, Suggested: interfaces.CONCEPT_VERDICT_RELATION, SuggestedBecause: rt.Origin,
			Item: &interfaces.DraftConceptItem{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Adopted: rt.Adopted}}
		switch {
		case rt.Document != nil:
			c.Sources = documentSources(rt.Document)
		case rt.LinkTable != nil:
			c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE,
				ID: rt.LinkTable.ResourceID, Name: rt.LinkTable.Name}}
		default:
			if source := findObjectType(items, rt.Source); source != nil && source.Document == nil {
				c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE,
					ID: source.Table.ResourceID, Name: source.Table.Name}}
			}
		}
		add(c)
	}
	for _, vt := range items.ValueTypes {
		add(interfaces.DraftConcept{Key: itemKey(interfaces.DRAFT_KIND_VALUE_TYPES, vt.ID), Name: vt.Name,
			Description: vt.Description, Suggested: interfaces.CONCEPT_VERDICT_VALUES, SuggestedBecause: "code_table",
			Sources: []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE,
				ID: vt.CodeTable.ResourceID, Name: vt.CodeTable.Name}},
			Item: &interfaces.DraftConceptItem{Kind: interfaces.DRAFT_KIND_VALUE_TYPES, ID: vt.ID, Adopted: vt.Adopted}})
	}

	if found && req != nil && req.Status == interfaces.REQUIREMENT_STATUS_READY {
		result.RequirementUpdateTime = req.UpdateTime
		// The items by their names and aliases, as a concept's term may name one.
		byName := map[string]string{}
		for _, key := range order {
			c := byKey[key]
			names := []string{c.Name}
			switch c.Item.Kind {
			case interfaces.DRAFT_KIND_OBJECT_TYPES:
				names = append(names, findObjectType(items, c.Item.ID).Aliases...)
			case interfaces.DRAFT_KIND_RELATION_TYPES:
				names = append(names, findRelationType(items, c.Item.ID).Aliases...)
			}
			for _, n := range names {
				if n = normalized(n); n != "" && byName[n] == "" {
					byName[n] = key
				}
			}
		}
		kinds := map[string]string{
			interfaces.REQUIREMENT_ITEM_OBJECT_TYPE:   interfaces.DRAFT_KIND_OBJECT_TYPES,
			interfaces.REQUIREMENT_ITEM_RELATION_TYPE: interfaces.DRAFT_KIND_RELATION_TYPES,
		}
		for _, rc := range req.Spec.Concepts {
			verdict, ok := requirementVerdicts[rc.Category]
			if !ok {
				// Evidence follows the business items; another category is not read.
				continue
			}
			documents := []string{}
			for _, citation := range rc.Citations {
				if !slices.Contains(documents, citation.DocumentID) {
					documents = append(documents, citation.DocumentID)
				}
			}
			source := interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_REQUIREMENT, ID: rc.ID,
				Name: rc.Term, DocumentIDs: documents}
			key := ""
			if rc.MapsTo != nil {
				key = itemKey(kinds[rc.MapsTo.Kind], rc.MapsTo.ID)
			} else if rc.Category == "object" || rc.Category == "event" || rc.Category == "relation" {
				// Named like an item, as the specification maps them: a property, kinetic or
				// not-included concept is one only by an explicit mapping.
				key = byName[normalized(rc.Term)]
			}
			if item := byKey[key]; item != nil {
				item.Sources = append(item.Sources, source)
				continue
			}
			c := interfaces.DraftConcept{Key: requirementConceptKey(rc.ID), Name: rc.Term, Description: rc.Definition,
				Suggested: verdict, SuggestedBecause: "requirement_" + rc.Category,
				Sources: []interfaces.DraftConceptSource{source}}
			if rc.Category == "not_ontology" {
				c.Reason = rc.NotOntologyReason
			}
			// The specification holds what it is: its category, which a person edits there.
			if rc.Edited {
				c.State = interfaces.CONCEPT_STATE_JUDGED
			}
			add(c)
		}
	}

	// Merged concepts give their sources to the one they went into.
	for _, key := range order {
		into := byKey[mergedInto[key]]
		if into == nil || mergedInto[mergedInto[key]] != "" {
			continue
		}
		into.Merged = append(into.Merged, interfaces.DraftConceptRef{Key: key, Name: byKey[key].Name})
		into.Sources = append(into.Sources, byKey[key].Sources...)
	}

	for _, key := range order {
		if byKey[mergedInto[key]] != nil && mergedInto[mergedInto[key]] == "" {
			continue
		}
		c := byKey[key]
		if c.Item == nil {
			c.Verdict = c.Suggested
			if c.State == "" {
				c.State = interfaces.CONCEPT_STATE_PENDING
			}
		} else {
			judgeConcept(ctx, items, c, judged[key], reviewed[key], excluded[key])
		}
		result.Counts[c.State]++
		result.Counts["verdict:"+c.Verdict]++
		result.Concepts = append(result.Concepts, *c)
	}
	rank := map[string]int{interfaces.CONCEPT_STATE_PENDING: 0, interfaces.CONCEPT_STATE_BLOCKED: 1,
		interfaces.CONCEPT_STATE_JUDGED: 2, interfaces.CONCEPT_STATE_IN_MODEL: 3}
	slices.SortStableFunc(result.Concepts, func(a, b interfaces.DraftConcept) int {
		return rank[a.State] - rank[b.State]
	})
	return result
}

// judgeConcept sets what the concept is and where it stands. A judgment holds until a person
// adopts the item or leaves it out on the model after it; such a decision is the verdict then.
func judgeConcept(ctx context.Context, items *interfaces.DraftItems, c *interfaces.DraftConcept,
	judgment conceptJudgment, reviewed *interfaces.DraftReview, excludeReason string) {
	c.Verdict = c.Suggested
	switch {
	case judgment.review != nil && (reviewed == nil || judgment.review.Time >= reviewed.Time):
		c.Verdict, c.Judged = judgment.verdict, judgment.review
		if judgment.reason != "" {
			c.Reason = judgment.reason
		}
	case reviewed != nil && c.Item != nil:
		// Decided on the model: adopted it is what drafting suggested, left out it is not included.
		c.Judged = reviewed
		if !c.Item.Adopted {
			c.Verdict, c.Reason = interfaces.CONCEPT_VERDICT_NONE, excludeReason
		}
	}
	switch {
	case c.Judged == nil:
		c.State = interfaces.CONCEPT_STATE_PENDING
	case c.Item != nil && adoptsAs(c.Item.Kind, c.Verdict) && c.Item.Adopted:
		c.State = interfaces.CONCEPT_STATE_IN_MODEL
	case c.Item != nil && adoptsAs(c.Item.Kind, c.Verdict):
		if c.Blocked = adoptionBlocker(ctx, items, c.Item.Kind, c.Item.ID); c.Blocked != "" {
			c.State = interfaces.CONCEPT_STATE_BLOCKED
		} else {
			c.State = interfaces.CONCEPT_STATE_JUDGED
		}
	default:
		c.State = interfaces.CONCEPT_STATE_JUDGED
	}
}

// documentSources are a document item's knowledge-base files, by the citations it was drafted from.
func documentSources(document *interfaces.DraftDocumentModel) []interfaces.DraftConceptSource {
	sources := []interfaces.DraftConceptSource{}
	seen := map[string]bool{}
	for _, citation := range document.Citations {
		if seen[citation.FileID] {
			continue
		}
		seen[citation.FileID] = true
		sources = append(sources, interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT,
			ID: citation.FileID})
	}
	if len(sources) == 0 {
		sources = append(sources, interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT})
	}
	return sources
}

// readConcepts reads the project's draft and specification and projects their concepts.
func (s *draftService) readConcepts(ctx context.Context, projectID string,
	draft *interfaces.Draft) (*interfaces.DraftConcepts, error) {
	req, found, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Read requirement failed", err)
	}
	return conceptsOf(ctx, projectID, draft, req, found), nil
}

func (s *draftService) GetConcepts(ctx context.Context, token string, projectID string) (*interfaces.DraftConcepts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetConcepts")
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
		draft = nil
	} else if err := documentHooks().CheckRead(ctx, token, draft); err != nil {
		return nil, err
	}
	concepts, err := s.readConcepts(ctx, projectID, draft)
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return concepts, nil
}

// findConcept is the concept of the key that stands on its own, not merged into another.
func findConcept(concepts *interfaces.DraftConcepts, key string) *interfaces.DraftConcept {
	for i := range concepts.Concepts {
		if concepts.Concepts[i].Key == key {
			return &concepts.Concepts[i]
		}
	}
	return nil
}

// JudgeConcept records what a concept is, and in the same write adopts its draft item or leaves it
// out accordingly, with the evidence items following. An item that cannot be adopted yet, for want
// of an identity, an end or a repair, keeps the judgment and waits: the concept is blocked.
func (s *draftService) JudgeConcept(ctx context.Context, token string, projectID string,
	req interfaces.DraftConceptJudge, caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "JudgeConcept")
	defer span.End()

	req.Reason = strings.TrimSpace(req.Reason)
	if !slices.Contains(interfaces.CONCEPT_VERDICTS, req.Verdict) ||
		utf8.RuneCountInString(req.Reason) > conceptReasonMaxLength {
		span.SetStatus(codes.Error, "Invalid judgment")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody)
	}
	draft, err := s.readyDraft(ctx, token, projectID, req.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	concepts, err := s.readConcepts(ctx, projectID, draft)
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	concept := findConcept(concepts, req.Key)
	if concept == nil {
		span.SetStatus(codes.Error, "Concept not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	if concept.Item == nil {
		// What a requirement concept is lives in the specification: it is edited there.
		span.SetStatus(codes.Error, "Requirement concept")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody).
			WithErrorDetails(map[string]any{"reason": "requirement_concept"})
	}

	decisions := []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_CONCEPTS, ItemID: req.Key,
		Action: interfaces.DRAFT_DECISION_JUDGE, Before: concept.Verdict, After: req.Verdict, Reason: req.Reason}}
	var document *interfaces.DraftDocumentModel
	if item := concept.Item; item != nil {
		adopt := adoptsAs(item.Kind, req.Verdict)
		edit := interfaces.DraftItemEdit{Adopted: &adopt}
		// A reason goes with leaving the item out, as on the model; the judgment keeps it either way.
		if !adopt {
			edit.Reason = req.Reason
		}
		edited, err := applyEdit(ctx, &draft.Items, item.Kind, item.ID, edit)
		if _, waits := blockerOf(err); err != nil && !(adopt && waits) {
			span.SetStatus(codes.Error, "Edit refused")
			return nil, err
		}
		decisions = append(decisions, edited...)
		if err == nil && adopt && item.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
			decisions = append(decisions, regateRelations(s.setting, &draft.Items, item.ID)...)
		}
		decisions = append(decisions, followEvidence(&draft.Items)...)
		switch item.Kind {
		case interfaces.DRAFT_KIND_OBJECT_TYPES:
			document = findObjectType(&draft.Items, item.ID).Document
		case interfaces.DRAFT_KIND_RELATION_TYPES:
			document = findRelationType(&draft.Items, item.ID).Document
		}
	}
	if err := s.writeConceptDecisions(ctx, draft, decisions, caller, document); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return s.readConcepts(ctx, projectID, draft)
}

// MergeConcept merges a concept into another, or takes it out again when Into is empty. Only the
// concepts merge: the draft items they are stay as they are.
func (s *draftService) MergeConcept(ctx context.Context, token string, projectID string,
	req interfaces.DraftConceptMerge, caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "MergeConcept")
	defer span.End()

	draft, err := s.readyDraft(ctx, token, projectID, req.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	concepts, err := s.readConcepts(ctx, projectID, draft)
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	_, mergedInto := conceptDecisions(&draft.Items)
	decision := &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS, ItemID: req.Key}
	switch {
	case req.Into == "":
		if mergedInto[req.Key] == "" {
			span.SetStatus(codes.Error, "Concept not merged")
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
		}
		decision.Action, decision.Before = interfaces.DRAFT_DECISION_UNMERGE, mergedInto[req.Key]
	default:
		// Both stand on their own, and the one merged in gives no concepts of its own away.
		from, into := findConcept(concepts, req.Key), findConcept(concepts, req.Into)
		if from == nil || into == nil || req.Key == req.Into {
			span.SetStatus(codes.Error, "Concept not found")
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
		}
		if len(from.Merged) > 0 {
			span.SetStatus(codes.Error, "Concept holds merged concepts")
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody)
		}
		decision.Action, decision.After = interfaces.DRAFT_DECISION_MERGE, req.Into
	}
	if err := s.commit(ctx, draft, []*interfaces.DraftDecision{decision}, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return s.readConcepts(ctx, projectID, draft)
}

// writeConceptDecisions writes as editing the item would: a document item through the document
// edit, which keeps its fact resolution current.
func (s *draftService) writeConceptDecisions(ctx context.Context, draft *interfaces.Draft,
	decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, document *interfaces.DraftDocumentModel) error {
	return s.commitItem(ctx, draft, decisions, caller, document)
}
