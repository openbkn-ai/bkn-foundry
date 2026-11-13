// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
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

// documentConceptPrefix starts the key of a concept of the document samples that drafting modeled
// no item for.
const documentConceptPrefix = "document_concepts/"

// documentConceptKey names a concept of the document samples that drafting modeled no item for.
func documentConceptKey(id string) string {
	return documentConceptPrefix + id
}

// addedConceptPrefix starts the key of a concept a person added from Ask AI's revisions.
const addedConceptPrefix = "ai_concepts/"

// judgedHere tells a concept no item is that is judged on this page, as a document's concept or
// one added from Ask AI is; a requirement concept is judged in the specification.
func judgedHere(key string) bool {
	return strings.HasPrefix(key, documentConceptPrefix) || strings.HasPrefix(key, addedConceptPrefix)
}

// blockers are the refusals of adopting an item that a judgment waits out: the item can be adopted
// once a person settles them on the model, so the judgment stands meanwhile.
var blockers = map[string]string{
	perrors.BknProject_Draft_NeedsIdentity:               interfaces.CONCEPT_BLOCKED_NEEDS_IDENTITY,
	perrors.BknProject_Draft_EndpointNotAdopted:          interfaces.CONCEPT_BLOCKED_END_NOT_ADOPTED,
	perrors.BknProject_Draft_DocumentRelationNeedsRepair: interfaces.CONCEPT_BLOCKED_REPAIR,
}

// blockerOf is why adopting was refused, when it is a refusal a judgment waits out.
func blockerOf(err error) (string, bool) {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return "", false
	}
	if blocker, ok := blockers[httpErr.ErrorCode]; ok {
		return blocker, true
	}
	return documentHooks().ConceptBlocker(httpErr.ErrorCode)
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

// isEvidence tells an evidence item, which follows its business items and is no concept of its own.
// The edition that has document items knows them.
func isEvidence(document *interfaces.DraftDocumentModel) bool {
	return document != nil && documentHooks().Concept(document).Evidence
}

// followEvidence has the evidence follow the business items, where the edition has evidence.
func followEvidence(items *interfaces.DraftItems) []*interfaces.DraftDecision {
	return documentHooks().FollowEvidence(items)
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

// addedConcept is a concept a person added from Ask AI, with why.
type addedConcept struct {
	key        string
	definition interfaces.DraftConceptDefinition
	reason     string
}

// definitionOf reads a decision's definition, as written or as read back from the store.
func definitionOf(after any) (interfaces.DraftConceptDefinition, bool) {
	var definition interfaces.DraftConceptDefinition
	raw, err := json.Marshal(after)
	if err != nil || json.Unmarshal(raw, &definition) != nil || strings.TrimSpace(definition.Name) == "" {
		return definition, false
	}
	return definition, true
}

// conceptDefinitions are the last definitions of the concepts no item is, and the concepts added
// from Ask AI in the order they were added.
func conceptDefinitions(items *interfaces.DraftItems) (map[string]interfaces.DraftConceptDefinition, []addedConcept) {
	defined, added := map[string]interfaces.DraftConceptDefinition{}, []addedConcept{}
	for _, d := range items.Decisions {
		if d.Kind != interfaces.DRAFT_KIND_CONCEPTS {
			continue
		}
		definition, ok := definitionOf(d.After)
		switch {
		case !ok:
		case d.Action == interfaces.DRAFT_DECISION_DEFINE:
			defined[d.ItemID] = definition
		case d.Action == interfaces.DRAFT_DECISION_ADD && strings.HasPrefix(d.ItemID, addedConceptPrefix):
			added = append(added, addedConcept{key: d.ItemID, definition: definition, reason: d.Reason})
		}
	}
	return defined, added
}

// understandConcept names the concept of an object type drafted from a table by what the
// catalog's semantic understanding gave the table, as the caller read it: its business name and
// description, while the object type still has the table's own name; the table's own name becomes
// an alias and names the source. It tells whether the table was understood; table is nil when it
// was not read.
func understandConcept(c *interfaces.DraftConcept, ot *interfaces.DraftObjectType, table *interfaces.VegaTable,
	language string) bool {
	physical := physicalName(table)
	c.Sources[0].Name = physical
	// Unnamed, a table keeps the name discovery gave it: its physical name, with its schema before
	// vega 0.2.1.
	if given := givenName(table); given == "" || given == physical {
		return false
	}
	if ot.Name == physical {
		c.Name, _ = pickName(language, physical, givenName(table), firstClause(table.Description))
		if table.Description != "" {
			c.Description = table.Description
		}
	}
	if c.Name != physical && !slices.Contains(c.Aliases, physical) {
		c.Aliases = append(c.Aliases, physical)
	}
	c.SuggestedBecause, c.AI = interfaces.CONCEPT_BECAUSE_UNDERSTOOD, true
	return true
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
	return conceptsWith(ctx, projectID, draft, req, found, nil)
}

// conceptsWith is conceptsOf with the tables of the draft's object types as the caller reads them
// now, by their ids: a table's concept is named as the catalog's semantic understanding named it.
func conceptsWith(ctx context.Context, projectID string, draft *interfaces.Draft, req *interfaces.Requirement,
	found bool, tables map[string]*interfaces.VegaTable) *interfaces.DraftConcepts {
	return projectConcepts(ctx, projectID, draft, req, found, tables, nil)
}

// tableConceptPrefix starts the key of a concept of a catalog table before drafting: the table is
// a candidate object until drafting models it, when the concept is its item's.
const tableConceptPrefix = "table_concepts/"

// tableConceptKey names the concept of a catalog table before drafting.
func tableConceptKey(resourceID string) string {
	return tableConceptPrefix + resourceID
}

// beforeDrafting is what the concepts are read from while the project has no ready draft: what
// the network already has, the tables of its catalogs as listed, and the project's naming language.
type beforeDrafting struct {
	existing interfaces.DraftItems
	tables   []*interfaces.VegaTable
	language string
}

// projectConcepts is conceptsWith, and before drafting, when before is given, the concepts of what
// the network already has and one candidate object per catalog table no existing object type reads,
// named as the catalog's semantic understanding named the table. Those are not judged here until
// drafting models them.
func projectConcepts(ctx context.Context, projectID string, draft *interfaces.Draft, req *interfaces.Requirement,
	found bool, tables map[string]*interfaces.VegaTable, before *beforeDrafting) *interfaces.DraftConcepts {
	result := &interfaces.DraftConcepts{ProjectID: projectID, Concepts: []interfaces.DraftConcept{},
		Counts: map[string]int{}}
	items := &interfaces.DraftItems{}
	if draft != nil && draft.Status == interfaces.DRAFT_STATUS_READY {
		items, result.DraftUpdateTime = &draft.Items, draft.UpdateTime
	} else if before != nil {
		items = &before.existing
	}
	judged, mergedInto := conceptDecisions(items)
	defined, added := conceptDefinitions(items)
	reviewed, excluded := reviews(items)
	confirmed := map[string]*interfaces.DraftReview{}
	if result.DraftUpdateTime > 0 {
		confirmed = confirmedItems(draft)
	}
	language := itemsLanguage(items)
	if before != nil && result.DraftUpdateTime == 0 && before.language != "" {
		language = before.language
	}

	byKey := map[string]*interfaces.DraftConcept{}
	order := []string{}
	add := func(c interfaces.DraftConcept) {
		byKey[c.Key] = &c
		order = append(order, c.Key)
	}
	evidenceObjects := map[string]bool{}
	// existing are the concepts of what the network already has: in the model as the network has it.
	existing := map[string]bool{}
	for _, ot := range items.ObjectTypes {
		if isEvidence(ot.Document) {
			evidenceObjects[ot.ID] = true
			continue
		}
		c := interfaces.DraftConcept{Key: itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, ot.ID), Name: ot.Name,
			Description: ot.Description, Aliases: slices.Clone(ot.Aliases), Suggested: interfaces.CONCEPT_VERDICT_OBJECT,
			Item: &interfaces.DraftConceptItem{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: ot.ID, Adopted: ot.Adopted}}
		switch {
		case ot.Existing:
			// Named as the network names it, from the network and the table it reads.
			c.SuggestedBecause = interfaces.CONCEPT_BECAUSE_NETWORK
			c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_NETWORK, ID: ot.ID, Name: ot.Name}}
			if ot.Table.ResourceID != "" {
				c.Sources = append(c.Sources, interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_TABLE,
					ID: ot.Table.ResourceID, Name: ot.Table.Name})
			}
			existing[c.Key] = true
		case ot.Document == nil:
			c.SuggestedBecause = interfaces.CONCEPT_BECAUSE_TABLE
			c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE,
				ID: ot.Table.ResourceID, Name: ot.Table.Name}}
			if table := tables[ot.Table.ResourceID]; table != nil && !understandConcept(&c, ot, table, language) {
				result.NotUnderstood++
			}
		default:
			documentConcept(&c, ot.Document)
		}
		add(c)
	}
	if before != nil && result.DraftUpdateTime == 0 {
		// Before drafting, each table no existing object type reads is a candidate object.
		read := existingTableIDs(items)
		for _, table := range before.tables {
			if table == nil || read[table.ID] {
				continue
			}
			physical := physicalName(table)
			c := interfaces.DraftConcept{Key: tableConceptKey(table.ID), Name: physical, Aliases: []string{},
				Suggested: interfaces.CONCEPT_VERDICT_OBJECT, SuggestedBecause: interfaces.CONCEPT_BECAUSE_TABLE,
				Sources: []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_TABLE, ID: table.ID, Name: physical}}}
			if !understandConcept(&c, &interfaces.DraftObjectType{Name: physical}, table, language) {
				result.NotUnderstood++
			}
			add(c)
		}
	}
	for _, rt := range items.RelationTypes {
		if isEvidence(rt.Document) || evidenceObjects[rt.Source] || evidenceObjects[rt.Target] {
			continue
		}
		c := interfaces.DraftConcept{Key: itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, rt.ID), Name: rt.Name,
			Description: rt.Description, Aliases: slices.Clone(rt.Aliases), Suggested: interfaces.CONCEPT_VERDICT_RELATION, SuggestedBecause: rt.Origin,
			Item: &interfaces.DraftConceptItem{Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ID: rt.ID, Adopted: rt.Adopted}}
		switch {
		case rt.Existing:
			c.SuggestedBecause = interfaces.CONCEPT_BECAUSE_NETWORK
			c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_NETWORK, ID: rt.ID, Name: rt.Name}}
			existing[c.Key] = true
		case rt.Document != nil:
			documentConcept(&c, rt.Document)
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
	// What the document samples hold that drafting modeled no item for, with drafting's reason.
	for _, dc := range items.DocumentConcepts {
		if dc == nil {
			continue
		}
		// The edition reads the files it cites; one it says nothing of is found in a document.
		sources := documentHooks().Concept(&interfaces.DraftDocumentModel{Citations: dc.Citations}).Sources
		if len(sources) == 0 {
			sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT}}
		}
		add(interfaces.DraftConcept{Key: documentConceptKey(dc.ID), Name: dc.Name, Description: dc.Description,
			Suggested: dc.Verdict, SuggestedBecause: "document_concept", Reason: dc.Reason, Sources: sources})
	}

	if found && req != nil && req.Status == interfaces.REQUIREMENT_STATUS_READY {
		result.RequirementUpdateTime = req.UpdateTime
		// The items by their names and aliases, as a concept's term may name one.
		byName := map[string]string{}
		for _, key := range order {
			c := byKey[key]
			if c.Item == nil && !strings.HasPrefix(key, tableConceptPrefix) {
				// A concept of the documents is no item a requirement concept could be.
				continue
			}
			for _, n := range append([]string{c.Name}, c.Aliases...) {
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
				// One scenario drafting added for an item it asks about, citing no document, restates
				// the item: it is no source of it.
				if rc.Source != interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS || len(rc.Citations) > 0 {
					item.Sources = append(item.Sources, source)
				}
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

	// The concepts a person added from Ask AI, with the sources of the one they were split from.
	for _, a := range added {
		c := interfaces.DraftConcept{Key: a.key, Name: a.definition.Name, Description: a.definition.Description,
			Suggested: a.definition.Verdict, SuggestedBecause: interfaces.CONCEPT_BECAUSE_AI, Reason: a.reason, AI: true,
			Sources: []interfaces.DraftConceptSource{}}
		if !slices.Contains(interfaces.CONCEPT_VERDICTS, c.Suggested) {
			c.Suggested = interfaces.CONCEPT_VERDICT_OBJECT
		}
		if from := byKey[a.definition.From]; from != nil {
			c.Sources = append(c.Sources, from.Sources...)
		}
		add(c)
	}
	// What a person named and defined a concept no item is, from Ask AI's revisions.
	for key, definition := range defined {
		if c := byKey[key]; c != nil && c.Item == nil {
			c.Name, c.Description, c.AI = definition.Name, definition.Description, true
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
		switch {
		case existing[key]:
			// No one judges it here: it is in the model as the network has it.
			c.Verdict, c.State = c.Suggested, interfaces.CONCEPT_STATE_IN_MODEL
		case c.Item != nil:
			review := reviewed[key]
			// Confirming the model with the item in it confirms what drafting adopted, when no one
			// judged the concept: a judgment says more of what it is.
			if at := confirmed[key]; at != nil && c.Item.Adopted && judged[key].review == nil &&
				(review == nil || at.Time > review.Time) {
				review = at
			}
			judgeConcept(ctx, items, c, judged[key], review, excluded[key])
		case judgedHere(key):
			// No item: a person's judgment is all there is to it.
			judgeConcept(ctx, items, c, judged[key], nil, "")
		default:
			c.Verdict = c.Suggested
			if c.State == "" {
				c.State = interfaces.CONCEPT_STATE_PENDING
			}
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

// confirmedItems are the items the last confirmation that committed wrote, by key, with who
// confirmed them and when: they went into the model as they were. A confirmation of an earlier
// draft, which drafting again carries over, confirmed nothing of this one.
func confirmedItems(draft *interfaces.Draft) map[string]*interfaces.DraftReview {
	confirmed := map[string]*interfaces.DraftReview{}
	c := draft.Items.Confirmation
	if c == nil || c.Commit == "" || c.Time == 0 || c.Time < draft.CreateTime {
		return confirmed
	}
	for _, path := range c.Files {
		dir, file, ok := strings.Cut(path, "/")
		id, bkn := strings.CutSuffix(file, ".bkn")
		if !ok || !bkn || id == "" || strings.Contains(id, "/") {
			continue
		}
		confirmed[itemKey(dir, id)] = &interfaces.DraftReview{By: c.By, Time: c.Time}
	}
	return confirmed
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
	// Unjudged, the verdict is what drafting did: an item it left out is not included, as
	// accepting the suggestions records it, until a person judges it otherwise.
	if c.Judged == nil && c.Item != nil && !c.Item.Adopted && adoptsAs(c.Item.Kind, c.Verdict) {
		c.Verdict = interfaces.CONCEPT_VERDICT_NONE
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

// documentConcept reads a document item's suggestion and sources as the edition knows them; one
// it says nothing of is found in a document.
func documentConcept(c *interfaces.DraftConcept, document *interfaces.DraftDocumentModel) {
	read := documentHooks().Concept(document)
	if read.Suggested != "" {
		c.Suggested = read.Suggested
	}
	if read.Because != "" {
		c.SuggestedBecause = read.Because
	}
	c.Sources = read.Sources
	if len(c.Sources) == 0 {
		c.Sources = []interfaces.DraftConceptSource{{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT}}
	}
}

// readConcepts reads the project's draft and specification and projects their concepts, the
// tables named as the caller reads them.
func (s *draftService) readConcepts(ctx context.Context, projectID string,
	draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	req, found, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Read requirement failed", err)
	}
	return conceptsWith(ctx, projectID, draft, req, found, s.conceptTables(ctx, draft, caller)), nil
}

// conceptsBeforeDrafting projects the concepts of a project with no ready draft: what the network
// already has, a candidate object per table of its catalogs, and the requirement concepts. A list
// that cannot be read is left out, as the concepts page lists what it can: a failed draft or the
// person's access to a catalog must not empty it.
func (s *draftService) conceptsBeforeDrafting(ctx context.Context, projectID string, entry *interfaces.ProjectEntry,
	draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	req, found, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Read requirement failed", err)
	}
	before := &beforeDrafting{}
	if entry.Project != nil {
		before.language = entry.NamingLanguage
	}
	// What the project's earlier confirmations wrote is its own, not what the network had.
	var owned []string
	if draft != nil {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	if existing, err := s.existingItems(ctx, entry, owned); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read what the network has for the concepts: %v", err))
	} else {
		before.existing = existing
	}
	if s.va != nil && entry.Project != nil {
		for _, catalogID := range entry.CatalogIDs {
			tables, err := s.va.ListTables(ctx, caller, catalogID)
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("List the tables of catalog %s for the concepts: %v", catalogID, err))
				continue
			}
			before.tables = append(before.tables, tables...)
		}
	}
	return projectConcepts(ctx, projectID, draft, req, found, nil, before), nil
}

// conceptTables reads the tables of the draft's object types as the caller sees them now, for
// the names the catalog's semantic understanding gave them since drafting. The concepts are
// listed without them when they cannot be read: the names drafting gave stand.
func (s *draftService) conceptTables(ctx context.Context, draft *interfaces.Draft,
	caller interfaces.AccountInfo) map[string]*interfaces.VegaTable {
	if s.va == nil || draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil
	}
	ids := []string{}
	for _, ot := range draft.Items.ObjectTypes {
		// What the network already has is named as the network names it.
		if ot.Document == nil && !ot.Existing && ot.Table.ResourceID != "" && !slices.Contains(ids, ot.Table.ResourceID) {
			ids = append(ids, ot.Table.ResourceID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	read, err := s.va.GetTables(ctx, caller, ids)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read the tables of the concepts: %v", err))
		return nil
	}
	tables := map[string]*interfaces.VegaTable{}
	for _, table := range read {
		tables[table.ID] = table
	}
	return tables
}

func (s *draftService) GetConcepts(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetConcepts")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
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
	var concepts *interfaces.DraftConcepts
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY {
		concepts, err = s.conceptsBeforeDrafting(ctx, projectID, entry, draft, caller)
	} else {
		concepts, err = s.readConcepts(ctx, projectID, draft, caller)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return concepts, nil
}

// findConcept is the concept of the key that stands on its own, not merged into another.
// conceptItemExisting tells whether a concept's item is one the network already has.
func conceptItemExisting(items *interfaces.DraftItems, item *interfaces.DraftConceptItem) bool {
	switch item.Kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		ot := findObjectType(items, item.ID)
		return ot != nil && ot.Existing
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		rt := findRelationType(items, item.ID)
		return rt != nil && rt.Existing
	}
	return false
}

func findConcept(concepts *interfaces.DraftConcepts, key string) *interfaces.DraftConcept {
	for i := range concepts.Concepts {
		if concepts.Concepts[i].Key == key {
			return &concepts.Concepts[i]
		}
	}
	return nil
}

// JudgeConcept records what a concept is, and in the same write adopts its draft item or leaves it
// out accordingly, with the evidence items following. A concept of the document samples that has no
// item only records the judgment. An item that cannot be adopted yet, for want
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
	concepts, err := s.readConcepts(ctx, projectID, draft, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	concept := findConcept(concepts, req.Key)
	if concept == nil {
		span.SetStatus(codes.Error, "Concept not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	if concept.Item == nil && !judgedHere(req.Key) {
		// What a requirement concept is lives in the specification: it is edited there.
		span.SetStatus(codes.Error, "Requirement concept")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody).
			WithErrorDetails(map[string]any{"reason": "requirement_concept"})
	}

	decisions, document, err := s.judgeDecisions(ctx, &draft.Items, concept, req.Verdict, req.Reason)
	if err != nil {
		span.SetStatus(codes.Error, "Edit refused")
		return nil, err
	}
	if err := s.writeConceptDecisions(ctx, draft, decisions, caller, document); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return s.readConcepts(ctx, projectID, draft, caller)
}

// judgeDecisions records what a concept is, and adopts its draft item or leaves it out
// accordingly, with the evidence items following; it gives the decisions and the item's document.
// An item that cannot be adopted yet keeps the judgment and waits.
func (s *draftService) judgeDecisions(ctx context.Context, items *interfaces.DraftItems,
	concept *interfaces.DraftConcept, verdict string, reason string) ([]*interfaces.DraftDecision,
	*interfaces.DraftDocumentModel, error) {
	decisions := []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_CONCEPTS, ItemID: concept.Key,
		Action: interfaces.DRAFT_DECISION_JUDGE, Before: concept.Verdict, After: verdict, Reason: reason}}
	var document *interfaces.DraftDocumentModel
	item := concept.Item
	if item == nil {
		return decisions, nil, nil
	}
	adopt := adoptsAs(item.Kind, verdict)
	edit := interfaces.DraftItemEdit{Adopted: &adopt}
	// A reason goes with leaving the item out, as on the model; the judgment keeps it either way.
	if !adopt {
		edit.Reason = reason
	} else {
		// Into the model, the object type takes the name semantic understanding gave its table.
		decisions = append(decisions, understoodName(ctx, items, concept)...)
	}
	edited, err := applyEdit(ctx, items, item.Kind, item.ID, edit)
	if _, waits := blockerOf(err); err != nil && !(adopt && waits) {
		return nil, nil, err
	}
	decisions = append(decisions, edited...)
	if err == nil && adopt && item.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES {
		decisions = append(decisions, regateRelations(s.setting, items, item.ID)...)
	}
	decisions = append(decisions, followEvidence(items)...)
	switch item.Kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		document = findObjectType(items, item.ID).Document
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		document = findRelationType(items, item.ID).Document
	}
	return decisions, document, nil
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
	concepts, err := s.readConcepts(ctx, projectID, draft, caller)
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
		if from.Item != nil && conceptItemExisting(&draft.Items, from.Item) {
			span.SetStatus(codes.Error, "Existing item")
			return nil, existingItem(ctx)
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
	return s.readConcepts(ctx, projectID, draft, caller)
}

// understoodName gives the object type of a concept semantic understanding named the name,
// description and aliases the concept shows, the table's own name among the aliases, when the
// draft takes them; what it refuses is left as drafting named it.
func understoodName(ctx context.Context, items *interfaces.DraftItems, concept *interfaces.DraftConcept) []*interfaces.DraftDecision {
	if concept.Item == nil || concept.Item.Kind != interfaces.DRAFT_KIND_OBJECT_TYPES ||
		concept.SuggestedBecause != interfaces.CONCEPT_BECAUSE_UNDERSTOOD {
		return nil
	}
	ot := findObjectType(items, concept.Item.ID)
	if ot == nil || ot.Name == concept.Name {
		return nil
	}
	name, description, aliases := concept.Name, concept.Description, slices.Clone(concept.Aliases)
	edit := interfaces.DraftItemEdit{Name: &name, Description: &description, Aliases: &aliases}
	if checkEdit(ctx, &edit) != nil {
		edit.Description = nil
		if checkEdit(ctx, &edit) != nil {
			return nil
		}
	}
	// Tried on a copy first: an edit refused part way leaves the draft as it was.
	raw, err := json.Marshal(items)
	var copied interfaces.DraftItems
	if err != nil || json.Unmarshal(raw, &copied) != nil {
		return nil
	}
	if _, err := applyEdit(ctx, &copied, concept.Item.Kind, concept.Item.ID, edit); err != nil {
		return nil
	}
	decisions, _ := applyEdit(ctx, items, concept.Item.Kind, concept.Item.ID, edit)
	return decisions
}

// writeConceptDecisions writes as editing the item would: a document item through the document
// edit, which keeps its fact resolution current.
func (s *draftService) writeConceptDecisions(ctx context.Context, draft *interfaces.Draft,
	decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, document *interfaces.DraftDocumentModel) error {
	return s.commitItem(ctx, draft, decisions, caller, document)
}
