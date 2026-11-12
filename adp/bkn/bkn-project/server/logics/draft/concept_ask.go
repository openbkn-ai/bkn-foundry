// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI on the concept extraction page: a question about the project's concepts, answered by the
// Ontology Assistant from the concepts as they stand. It proposes revisions of them, renaming and
// defining, judging, merging, splitting, adding and naming aliases, each checked against the
// concepts as judging or merging by hand would be; nothing changes until a person adopts one,
// which records it as their decision.

const (
	// askKindConcepts is what the message asks about.
	askKindConcepts = "concepts"
	// conceptAskMax bounds the concepts the message carries, so a large project stays small.
	conceptAskMax = 150
	// conceptAskDescriptionMax shortens the definitions the message carries.
	conceptAskDescriptionMax = 120
	// conceptAskMaxRevisions bounds the revisions of one answer.
	conceptAskMaxRevisions = 8
	// conceptSplitMaxParts bounds the concepts a split makes.
	conceptSplitMaxParts = 4
)

// Why a revision of the concepts is left out, besides the reasons Ask AI on an item gives.
const (
	askDroppedUnknownConcept  = "unknown_concept"
	askDroppedVerdictNotFit   = "verdict_not_fit"
	askDroppedInSpecification = "in_specification"
)

// conceptAskTask tells the Assistant what it is asked of the concepts, as the message carries it.
const conceptAskTask = "kind is concepts: the engineer reviews the concepts extracted from the project's " +
	"data tables, knowledge-base files and requirement documents before modeling them. concepts lists them, " +
	"each with its key, name, aliases, definition, verdict (object, event, property, values, relation, " +
	"kinetic or none, not included), state, item (the kind of draft item it is, when it is one), the " +
	"revisions it takes, and its sources (table:<name>, document or requirement:<term>). A table's own " +
	"name, such as erp_material, is no business name: name such concepts in business words. Answer in the " +
	"language named in the message. Then give revisions, only those the question asks for or the answer " +
	"shows are needed: define (key, a clearer business name and definition), verdict (key and the verdict " +
	"it should have), merge (key and into, the concept it is the same as), split (key and parts, the " +
	"concepts it holds, the first being what it stays), add (name, definition and verdict of a concept " +
	"the sources hold that the list misses) and aliases (key and the whole list of its other names). Use " +
	"only keys as given, and only revisions a concept takes. Give each a reason, one short sentence."

// askConcept is a concept as the message carries it.
type askConcept struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	Verdict     string   `json:"verdict"`
	State       string   `json:"state"`
	Item        string   `json:"item,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Merged      []string `json:"merged,omitempty"`
	// Revisions are those the concept takes.
	Revisions []string `json:"revisions"`
}

// conceptRevisionOutput is a revision of the concepts as the Agent gives it.
type conceptRevisionOutput struct {
	Field       string                        `json:"field"`
	Key         string                        `json:"key"`
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	Verdict     string                        `json:"verdict"`
	Into        string                        `json:"into"`
	Aliases     []string                      `json:"aliases"`
	Parts       []interfaces.DraftConceptPart `json:"parts"`
	Reason      string                        `json:"reason"`
}

// conceptAskFormat is the JSON Schema of the revisions that follow the answer.
func conceptAskFormat() map[string]any {
	str := func(max int, description string) map[string]any {
		return map[string]any{"type": "string", "maxLength": max, "description": description}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required,
			"additionalProperties": false}
	}
	verdict := map[string]any{"type": "string", "enum": interfaces.CONCEPT_VERDICTS}
	revision := object(map[string]any{
		"field": map[string]any{"type": "string", "enum": interfaces.CONCEPT_REVISIONS},
		"key":   map[string]any{"type": "string", "description": "The key of the concept revised, as given."},
		"name":  str(interfaces.DRAFT_NAME_MAX_LENGTH, "A business name, for define and add."),
		"description": str(askValueMaxLength,
			"A definition in one or two sentences, for define and add."),
		"verdict": verdict,
		"into":    map[string]any{"type": "string", "description": "The key of the concept to merge into."},
		"aliases": map[string]any{"type": "array", "maxItems": interfaces.DRAFT_ALIASES_MAX,
			"items": str(interfaces.DRAFT_ALIAS_MAX_LENGTH, "")},
		"parts": map[string]any{"type": "array", "minItems": 2, "maxItems": conceptSplitMaxParts,
			"items": object(map[string]any{
				"name":        str(interfaces.DRAFT_NAME_MAX_LENGTH, ""),
				"description": str(askValueMaxLength, ""),
				"verdict":     verdict,
			}, "name", "verdict")},
		"reason": str(askReasonMaxLength, "One short sentence on why."),
	}, "field", "reason")
	return object(map[string]any{
		"revisions": map[string]any{"type": "array", "maxItems": conceptAskMaxRevisions, "items": revision},
	}, "revisions")
}

// conceptVerdictsOf are the verdicts that fit a concept: what its item can be, anything without one.
func conceptVerdictsOf(c *interfaces.DraftConcept) []string {
	if c.Item == nil {
		return interfaces.CONCEPT_VERDICTS
	}
	switch c.Item.Kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		return []string{interfaces.CONCEPT_VERDICT_OBJECT, interfaces.CONCEPT_VERDICT_EVENT,
			interfaces.CONCEPT_VERDICT_PROPERTY, interfaces.CONCEPT_VERDICT_KINETIC, interfaces.CONCEPT_VERDICT_NONE}
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		return []string{interfaces.CONCEPT_VERDICT_RELATION, interfaces.CONCEPT_VERDICT_NONE}
	case interfaces.DRAFT_KIND_VALUE_TYPES:
		return []string{interfaces.CONCEPT_VERDICT_VALUES, interfaces.CONCEPT_VERDICT_PROPERTY,
			interfaces.CONCEPT_VERDICT_NONE}
	}
	return nil
}

// conceptRevisionsOf are the revisions a concept takes: a requirement concept no item is is edited
// in the specification, so it only merges; aliases are an object or relation type's of a table.
func conceptRevisionsOf(items *interfaces.DraftItems, c *interfaces.DraftConcept) []string {
	if c.Item == nil && !judgedHere(c.Key) {
		return []string{interfaces.CONCEPT_REVISION_MERGE}
	}
	revisions := []string{interfaces.CONCEPT_REVISION_DEFINE, interfaces.CONCEPT_REVISION_VERDICT,
		interfaces.CONCEPT_REVISION_MERGE, interfaces.CONCEPT_REVISION_SPLIT}
	if aliasable(items, c) {
		revisions = append(revisions, interfaces.CONCEPT_REVISION_ALIASES)
	}
	return revisions
}

// aliasable tells a concept that is an object or relation type of a table, which takes aliases.
func aliasable(items *interfaces.DraftItems, c *interfaces.DraftConcept) bool {
	if c.Item == nil {
		return false
	}
	switch c.Item.Kind {
	case interfaces.DRAFT_KIND_OBJECT_TYPES:
		ot := findObjectType(items, c.Item.ID)
		return ot != nil && ot.Document == nil
	case interfaces.DRAFT_KIND_RELATION_TYPES:
		rt := findRelationType(items, c.Item.ID)
		return rt != nil && rt.Document == nil
	}
	return false
}

// askConceptsOf are the concepts as the message carries them, the first conceptAskMax.
func askConceptsOf(items *interfaces.DraftItems, concepts *interfaces.DraftConcepts) []askConcept {
	asked := []askConcept{}
	for i := range concepts.Concepts[:min(len(concepts.Concepts), conceptAskMax)] {
		c := &concepts.Concepts[i]
		a := askConcept{Key: c.Key, Name: c.Name, Aliases: c.Aliases, Verdict: c.Verdict, State: c.State,
			Description: truncateRunes(c.Description, conceptAskDescriptionMax),
			Revisions:   conceptRevisionsOf(items, c)}
		if c.Item != nil {
			a.Item = c.Item.Kind
		}
		for _, source := range c.Sources {
			if source.Name == "" {
				a.Sources = append(a.Sources, source.Kind)
			} else {
				a.Sources = append(a.Sources, source.Kind+":"+source.Name)
			}
		}
		for _, m := range c.Merged {
			a.Merged = append(a.Merged, m.Name)
		}
		asked = append(asked, a)
	}
	return asked
}

// conceptName checks a name as an item's edit does, and tells it trimmed.
func conceptName(ctx context.Context, name string) (string, bool) {
	edit := interfaces.DraftItemEdit{Name: &name}
	if checkEdit(ctx, &edit) != nil {
		return "", false
	}
	return *edit.Name, true
}

// conceptDescription checks a definition as an item's edit does, and tells it trimmed.
func conceptDescription(ctx context.Context, description string) (string, bool) {
	edit := interfaces.DraftItemEdit{Description: &description}
	if checkEdit(ctx, &edit) != nil {
		return "", false
	}
	return *edit.Description, true
}

// checkConceptRevision checks one revision against the concepts as they stand, and gives it as a
// person adopts it, or why it is left out.
func checkConceptRevision(ctx context.Context, items *interfaces.DraftItems, concepts *interfaces.DraftConcepts,
	out conceptRevisionOutput) (*interfaces.DraftConceptRevision, string) {
	if !slices.Contains(interfaces.CONCEPT_REVISIONS, out.Field) {
		return nil, askDroppedNotEditable
	}
	if out.Field == interfaces.CONCEPT_REVISION_ADD {
		name, ok := conceptName(ctx, out.Name)
		if !ok {
			return nil, askDroppedInvalidName
		}
		description, ok := conceptDescription(ctx, out.Description)
		if !ok {
			return nil, askDroppedInvalidDescription
		}
		for _, c := range concepts.Concepts {
			if normalized(c.Name) == normalized(name) {
				return nil, askDroppedRepeated
			}
		}
		verdict := out.Verdict
		if verdict == "" {
			verdict = interfaces.CONCEPT_VERDICT_OBJECT
		}
		if !slices.Contains(interfaces.CONCEPT_VERDICTS, verdict) {
			return nil, askDroppedInvalid
		}
		return &interfaces.DraftConceptRevision{Name: name, Description: description, Verdict: verdict}, ""
	}

	concept := findConcept(concepts, out.Key)
	if concept == nil {
		return nil, askDroppedUnknownConcept
	}
	if !slices.Contains(conceptRevisionsOf(items, concept), out.Field) {
		if concept.Item == nil && !judgedHere(concept.Key) {
			return nil, askDroppedInSpecification
		}
		return nil, askDroppedNotEditable
	}
	revision := &interfaces.DraftConceptRevision{Key: concept.Key}
	switch out.Field {
	case interfaces.CONCEPT_REVISION_DEFINE:
		name, description := concept.Name, concept.Description
		var ok bool
		if strings.TrimSpace(out.Name) != "" {
			if name, ok = conceptName(ctx, out.Name); !ok {
				return nil, askDroppedInvalidName
			}
		}
		if strings.TrimSpace(out.Description) != "" {
			if description, ok = conceptDescription(ctx, out.Description); !ok {
				return nil, askDroppedInvalidDescription
			}
		}
		if name == concept.Name && description == concept.Description {
			return nil, askDroppedUnchanged
		}
		revision.Name, revision.Description = name, description
	case interfaces.CONCEPT_REVISION_VERDICT:
		if !slices.Contains(conceptVerdictsOf(concept), out.Verdict) {
			return nil, askDroppedVerdictNotFit
		}
		if out.Verdict == concept.Verdict {
			return nil, askDroppedUnchanged
		}
		revision.Verdict = out.Verdict
	case interfaces.CONCEPT_REVISION_MERGE:
		// As merging by hand: both stand on their own, and the one merged in holds none.
		if out.Into == concept.Key || findConcept(concepts, out.Into) == nil || len(concept.Merged) > 0 {
			return nil, askDroppedInvalid
		}
		revision.Into = out.Into
	case interfaces.CONCEPT_REVISION_SPLIT:
		if len(out.Parts) < 2 || len(out.Parts) > conceptSplitMaxParts {
			return nil, askDroppedInvalid
		}
		seen := map[string]bool{}
		for i, part := range out.Parts {
			name, ok := conceptName(ctx, part.Name)
			if !ok {
				return nil, askDroppedInvalidName
			}
			description, ok := conceptDescription(ctx, part.Description)
			if !ok {
				return nil, askDroppedInvalidDescription
			}
			// The first part is what the concept stays, so its verdict fits it.
			fits := interfaces.CONCEPT_VERDICTS
			if i == 0 {
				fits = conceptVerdictsOf(concept)
			}
			if !slices.Contains(fits, part.Verdict) {
				return nil, askDroppedVerdictNotFit
			}
			if seen[normalized(name)] {
				return nil, askDroppedRepeated
			}
			// The other parts are new concepts: a split adopted already made them.
			for _, c := range concepts.Concepts {
				if i > 0 && normalized(c.Name) == normalized(name) {
					return nil, askDroppedRepeated
				}
			}
			seen[normalized(name)] = true
			revision.Parts = append(revision.Parts, interfaces.DraftConceptPart{Name: name,
				Description: description, Verdict: part.Verdict})
		}
	case interfaces.CONCEPT_REVISION_ALIASES:
		name, current := "", []string(nil)
		if ot := findObjectType(items, concept.Item.ID); concept.Item.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES && ot != nil {
			name, current = ot.Name, ot.Aliases
		} else if rt := findRelationType(items, concept.Item.ID); rt != nil {
			name, current = rt.Name, rt.Aliases
		}
		checked, reason := aliasesRevision(ctx, items, concept.Item.Kind, concept.Item.ID, name, current, out.Aliases)
		if checked == nil {
			return nil, reason
		}
		revision.Aliases = checked.Value.([]string)
	}
	return revision, ""
}

func (s *draftService) AskConcepts(ctx context.Context, token string, projectID string,
	req interfaces.DraftConceptAskRequest, caller interfaces.AccountInfo,
	stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskConcepts")
	defer span.End()

	draft, in, err := s.readyToAsk(ctx, token, projectID, req.Question, req.History)
	if err != nil {
		span.SetStatus(codes.Error, "Not askable")
		return nil, err
	}
	concepts, err := s.readConcepts(ctx, projectID, draft, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}
	in.Kind, in.Task = askKindConcepts, conceptAskTask
	in.Concepts, in.Editable = askConceptsOf(&draft.Items, concepts), interfaces.CONCEPT_REVISIONS

	answer, err := s.ask(ctx, caller, askTurn{input: in, format: conceptAskFormat(),
		finish: func(out *askOutput) *interfaces.DraftAskAnswer {
			var result struct {
				Revisions []conceptRevisionOutput `json:"revisions"`
			}
			_ = json.Unmarshal(out.raw, &result)
			answer := &interfaces.DraftAskAnswer{Revisions: []interfaces.DraftAskRevision{},
				Dropped: []interfaces.DraftAskDropped{}}
			seen := map[string]bool{}
			for _, r := range result.Revisions[:min(len(result.Revisions), conceptAskMaxRevisions)] {
				revision, reason := checkConceptRevision(ctx, &draft.Items, concepts, r)
				if revision != nil {
					seenKey := r.Field + "/" + revision.Key
					if r.Field == interfaces.CONCEPT_REVISION_ADD {
						seenKey += normalized(revision.Name)
					}
					if seen[seenKey] {
						revision, reason = nil, askDroppedRepeated
					}
					seen[seenKey] = true
				}
				if revision == nil {
					answer.Dropped = append(answer.Dropped, interfaces.DraftAskDropped{Field: r.Field, Reason: reason})
					continue
				}
				answer.Revisions = append(answer.Revisions, interfaces.DraftAskRevision{Field: r.Field, Value: revision,
					Reason: truncateRunes(strings.TrimSpace(r.Reason), askReasonMaxLength)})
			}
			return answer
		}}, stream)
	if err != nil {
		span.SetStatus(codes.Error, "Ask AI failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

// ReviseConcept adopts one revision Ask AI proposed, checked again against the concepts as they
// stand: judging and merging as by hand; renaming, defining and aliases on the item a concept is,
// as editing it does, or as the concept's own definition when it is none; a split keeps the concept
// as its first part and adds the others with its sources.
func (s *draftService) ReviseConcept(ctx context.Context, token string, projectID string,
	req interfaces.DraftConceptRevise, caller interfaces.AccountInfo) (*interfaces.DraftConcepts, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ReviseConcept")
	defer span.End()

	req.Reason = strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(req.Reason) > conceptReasonMaxLength {
		span.SetStatus(codes.Error, "Invalid revision")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidRequestBody)
	}
	value := req.Value
	switch req.Field {
	case interfaces.CONCEPT_REVISION_VERDICT:
		return s.JudgeConcept(ctx, token, projectID, interfaces.DraftConceptJudge{UpdateTime: req.UpdateTime,
			Key: value.Key, Verdict: value.Verdict, Reason: req.Reason}, caller)
	case interfaces.CONCEPT_REVISION_MERGE:
		return s.MergeConcept(ctx, token, projectID, interfaces.DraftConceptMerge{UpdateTime: req.UpdateTime,
			Key: value.Key, Into: value.Into}, caller)
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
	revision, dropped := checkConceptRevision(ctx, &draft.Items, concepts, conceptRevisionOutput{
		Field: req.Field, Key: value.Key, Name: value.Name, Description: value.Description,
		Verdict: value.Verdict, Aliases: value.Aliases, Parts: value.Parts})
	if revision == nil {
		span.SetStatus(codes.Error, "Revision refused")
		status := http.StatusBadRequest
		if dropped == askDroppedUnknownConcept {
			status = http.StatusNotFound
		}
		return nil, httperrors.NewHTTPError(ctx, status, perrors.BknProject_Draft_InvalidRequestBody).
			WithErrorDetails(map[string]any{"reason": dropped})
	}

	decisions := []*interfaces.DraftDecision{}
	var document *interfaces.DraftDocumentModel
	define := func(name, description string) error {
		concept := findConcept(concepts, revision.Key)
		if concept.Item == nil {
			if name != concept.Name || description != concept.Description {
				decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
					ItemID: revision.Key, Action: interfaces.DRAFT_DECISION_DEFINE,
					Before: interfaces.DraftConceptDefinition{Name: concept.Name, Description: concept.Description},
					After:  interfaces.DraftConceptDefinition{Name: name, Description: description}, Reason: req.Reason})
			}
			return nil
		}
		edited, err := applyEdit(ctx, &draft.Items, concept.Item.Kind, concept.Item.ID,
			interfaces.DraftItemEdit{Name: &name, Description: &description})
		decisions = append(decisions, edited...)
		return err
	}
	add := func(part interfaces.DraftConceptPart, from string) {
		decisions = append(decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
			ItemID: addedConceptPrefix + uuid.NewString(), Action: interfaces.DRAFT_DECISION_ADD,
			After: interfaces.DraftConceptDefinition{Name: part.Name, Description: part.Description,
				Verdict: part.Verdict, From: from}, Reason: req.Reason})
	}
	switch req.Field {
	case interfaces.CONCEPT_REVISION_DEFINE:
		err = define(revision.Name, revision.Description)
	case interfaces.CONCEPT_REVISION_ALIASES:
		concept := findConcept(concepts, revision.Key)
		var edited []*interfaces.DraftDecision
		edited, err = applyEdit(ctx, &draft.Items, concept.Item.Kind, concept.Item.ID,
			interfaces.DraftItemEdit{Aliases: &revision.Aliases})
		decisions = append(decisions, edited...)
	case interfaces.CONCEPT_REVISION_SPLIT:
		// What the concept stays is judged in the same write, before it is named, so the name the
		// split gives it stands over the one semantic understanding gave.
		if concept := findConcept(concepts, revision.Key); revision.Parts[0].Verdict != concept.Verdict {
			decisions, document, err = s.judgeDecisions(ctx, &draft.Items, concept, revision.Parts[0].Verdict, req.Reason)
		}
		if err == nil {
			err = define(revision.Parts[0].Name, revision.Parts[0].Description)
		}
		for _, part := range revision.Parts[1:] {
			add(part, revision.Key)
		}
	case interfaces.CONCEPT_REVISION_ADD:
		add(interfaces.DraftConceptPart{Name: revision.Name, Description: revision.Description,
			Verdict: revision.Verdict}, "")
	}
	if err != nil {
		span.SetStatus(codes.Error, "Edit refused")
		return nil, err
	}
	if concept := findConcept(concepts, revision.Key); concept != nil && concept.Item != nil {
		switch concept.Item.Kind {
		case interfaces.DRAFT_KIND_OBJECT_TYPES:
			document = findObjectType(&draft.Items, concept.Item.ID).Document
		case interfaces.DRAFT_KIND_RELATION_TYPES:
			document = findRelationType(&draft.Items, concept.Item.ID).Document
		}
	}
	if len(decisions) == 0 {
		span.SetStatus(codes.Ok, "Unchanged")
		return concepts, nil
	}
	if err := s.commitItem(ctx, draft, decisions, caller, document); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return s.readConcepts(ctx, projectID, draft, caller)
}
