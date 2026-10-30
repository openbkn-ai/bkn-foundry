// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// People edit the specification the Agent drafted: they change, add and remove items, say which
// drafted item a concept is, resolve conflicts and decide modelling choices. Every change is
// checked against the specification and the draft as they are, and written only over the
// specification the person read.

// Lengths of what people write: a name or term, and any other text.
const (
	maxItemNameRunes = 255
	maxItemTextRunes = 2000
)

var itemKinds = []string{interfaces.REQUIREMENT_KIND_SCENARIOS, interfaces.REQUIREMENT_KIND_CONCEPTS,
	interfaces.REQUIREMENT_KIND_QUESTIONS, interfaces.REQUIREMENT_KIND_RULES}

var conceptCategories = []string{"object", "event", "property", "relation", "evidence", "not_ontology"}

func invalidItem(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem).
		WithErrorDetails(detail)
}

// change is one edit of the specification, applied to a copy read for it.
type change func(spec *interfaces.RequirementSpec, drafted *draftedItems) error

// update reads the project's ready specification, applies the change and writes it over the
// specification as the caller read it.
func (s *requirementService) update(ctx context.Context, token string, projectID string, updateTime int64,
	caller interfaces.AccountInfo, apply change) (*interfaces.Requirement, error) {
	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		return nil, err
	}
	if updateTime == 0 {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime)
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	switch {
	case ok && req.Status == interfaces.REQUIREMENT_STATUS_DRAFTING:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	case !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY:
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	case req.UpdateTime != updateTime:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	}
	drafted, err := s.draftedItems(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if err := apply(&req.Spec, drafted); err != nil {
		return nil, err
	}
	req.UpdateTime = s.now()
	written, err := s.ra.UpdateRequirement(ctx, req, updateTime)
	if err != nil {
		return nil, internalError(ctx, "Update requirement failed", err)
	}
	if !written {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	}
	return req, nil
}

// draftedItems are the object types and relation types of the project's draft, by reference, and
// the draft itself (nil without one) for what edits check against it.
type draftedItems struct {
	refs  map[interfaces.RequirementItemRef]bool
	draft *interfaces.Draft
}

func (d *draftedItems) has(ref interfaces.RequirementItemRef) bool { return d.refs[ref] }

func (s *requirementService) draftedItems(ctx context.Context, projectID string) (*draftedItems, error) {
	drafted := &draftedItems{refs: map[interfaces.RequirementItemRef]bool{}}
	draft, ok, err := s.da.GetDraft(ctx, projectID)
	if err != nil || !ok {
		return drafted, err
	}
	drafted.draft = draft
	for _, ot := range draft.Items.ObjectTypes {
		drafted.refs[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = true
	}
	for _, rt := range draft.Items.RelationTypes {
		drafted.refs[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = true
	}
	return drafted, nil
}

// followConcepts sets the needs of every question that follows its concepts from their mappings.
func followConcepts(spec *interfaces.RequirementSpec) {
	mapped := map[string]*interfaces.RequirementItemRef{}
	for _, c := range spec.Concepts {
		mapped[c.ID] = c.MapsTo
	}
	for _, q := range spec.Questions {
		if q.NeedsSource == interfaces.REQUIREMENT_NEEDS_FROM_PERSON {
			continue
		}
		q.NeedsSource = interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS
		q.Needs = []interfaces.RequirementItemRef{}
		for _, cid := range q.Basis.ConceptIDs {
			if ref := mapped[cid]; ref != nil && !slices.Contains(q.Needs, *ref) {
				q.Needs = append(q.Needs, *ref)
			}
		}
	}
}

// text checks a field a person writes: required ones must not be blank.
func text(ctx context.Context, field string, value *string, limit int, required bool) (string, error) {
	v := strings.TrimSpace(*value)
	if required && v == "" {
		return "", invalidItem(ctx, field+" is empty")
	}
	if utf8.RuneCountInString(v) > limit {
		return "", invalidItem(ctx, fmt.Sprintf("%s is longer than %d characters", field, limit))
	}
	return v, nil
}

// setText applies a field the edit sets, checked.
func setText(ctx context.Context, field string, value *string, limit int, required bool, target *string) error {
	if value == nil {
		return nil
	}
	v, err := text(ctx, field, value, limit, required)
	if err == nil {
		*target = v
	}
	return err
}

// onlyFields refuses an edit that sets fields the kind does not have, or none at all.
func onlyFields(ctx context.Context, edit interfaces.RequirementItemEdit, kind string) error {
	set := map[string]bool{
		"name": edit.Name != nil, "actor": edit.Actor != nil, "trigger": edit.Trigger != nil,
		"decision": edit.Decision != nil, "knowledge": edit.Knowledge != nil, "term": edit.Term != nil,
		"category": edit.Category != nil, "definition": edit.Definition != nil, "text": edit.Text != nil,
		"layer": edit.Layer != nil, "template": edit.Template != nil, "answer_shape": edit.AnswerShape != nil,
		"scenario_ids": edit.ScenarioIDs != nil, "needs": edit.Needs != nil, "statement": edit.Statement != nil,
		"follow_concepts": edit.FollowConcepts != nil, "inputs": edit.Inputs != nil,
		"relative_window": edit.RelativeWindow != nil,
	}
	allowed := map[string][]string{
		interfaces.REQUIREMENT_KIND_SCENARIOS: {"name", "actor", "trigger", "decision", "knowledge"},
		interfaces.REQUIREMENT_KIND_CONCEPTS:  {"term", "category", "definition"},
		interfaces.REQUIREMENT_KIND_QUESTIONS: {"text", "layer", "template", "answer_shape", "scenario_ids", "needs",
			"follow_concepts", "inputs", "relative_window"},
		interfaces.REQUIREMENT_KIND_RULES: {"statement"},
	}[kind]
	setSome := false
	for field, isSet := range set {
		if !isSet {
			continue
		}
		if !slices.Contains(allowed, field) {
			return invalidItem(ctx, field+" is not a field of "+kind)
		}
		setSome = true
	}
	if !setSome {
		return invalidItem(ctx, "the edit sets no field")
	}
	return nil
}

func applyScenario(ctx context.Context, s *interfaces.RequirementScenario, edit interfaces.RequirementItemEdit) error {
	for _, f := range []struct {
		name   string
		value  *string
		limit  int
		req    bool
		target *string
	}{
		{"name", edit.Name, maxItemNameRunes, true, &s.Name},
		{"actor", edit.Actor, maxItemNameRunes, false, &s.Actor},
		{"trigger", edit.Trigger, maxItemTextRunes, false, &s.Trigger},
		{"decision", edit.Decision, maxItemTextRunes, false, &s.Decision},
	} {
		if err := setText(ctx, f.name, f.value, f.limit, f.req, f.target); err != nil {
			return err
		}
	}
	if edit.Knowledge != nil {
		knowledge := []string{}
		for _, k := range *edit.Knowledge {
			v, err := text(ctx, "knowledge", &k, maxItemTextRunes, true)
			if err != nil {
				return err
			}
			knowledge = append(knowledge, v)
		}
		s.Knowledge = knowledge
	}
	return nil
}

func applyConcept(ctx context.Context, c *interfaces.RequirementConcept, edit interfaces.RequirementItemEdit) error {
	if err := setText(ctx, "term", edit.Term, maxItemNameRunes, true, &c.Term); err != nil {
		return err
	}
	if err := setText(ctx, "definition", edit.Definition, maxItemTextRunes, false, &c.Definition); err != nil {
		return err
	}
	if edit.Category != nil {
		if !slices.Contains(conceptCategories, *edit.Category) {
			return invalidItem(ctx, "category "+*edit.Category+" is unknown")
		}
		c.Category = *edit.Category
	}
	return nil
}

func applyQuestion(ctx context.Context, q *interfaces.RequirementQuestion, edit interfaces.RequirementItemEdit,
	spec *interfaces.RequirementSpec, drafted *draftedItems) error {
	for _, f := range []struct {
		name   string
		value  *string
		req    bool
		target *string
	}{
		{"text", edit.Text, true, &q.Text},
		{"template", edit.Template, false, &q.Template},
		{"answer_shape", edit.AnswerShape, false, &q.AnswerShape},
	} {
		if err := setText(ctx, f.name, f.value, maxItemTextRunes, f.req, f.target); err != nil {
			return err
		}
	}
	if edit.Layer != nil {
		if *edit.Layer != interfaces.REQUIREMENT_LAYER_KNOWLEDGE && *edit.Layer != interfaces.REQUIREMENT_LAYER_CAPABILITY {
			return invalidItem(ctx, "layer "+*edit.Layer+" is unknown")
		}
		q.Layer = *edit.Layer
	}
	if edit.ScenarioIDs != nil {
		ids := []string{}
		for _, id := range *edit.ScenarioIDs {
			if !slices.ContainsFunc(spec.Scenarios, func(s *interfaces.RequirementScenario) bool { return s.ID == id }) {
				return invalidItem(ctx, "scenario "+id+" does not exist")
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		q.ScenarioIDs = ids
	}
	if edit.FollowConcepts != nil {
		if !*edit.FollowConcepts || edit.Needs != nil {
			return invalidItem(ctx, "follow_concepts is true, and set without needs")
		}
		q.NeedsSource = interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS
		followConcepts(spec)
	}
	if edit.Needs != nil {
		needs := []interfaces.RequirementItemRef{}
		for _, need := range *edit.Needs {
			if !drafted.has(need) {
				return invalidItem(ctx, need.Kind+" "+need.ID+" is not in the draft")
			}
			if !slices.Contains(needs, need) {
				needs = append(needs, need)
			}
		}
		q.Needs, q.NeedsSource = needs, interfaces.REQUIREMENT_NEEDS_FROM_PERSON
	}
	if edit.Inputs != nil {
		inputs := slices.Clone(*edit.Inputs)
		if inputs == nil {
			inputs = []interfaces.RequirementInput{}
		}
		q.Inputs = &inputs
	}
	if edit.RelativeWindow != nil {
		q.RelativeWindow = *edit.RelativeWindow
	}
	if edit.Inputs != nil || edit.Template != nil || edit.RelativeWindow != nil {
		return checkInputs(ctx, q, spec, drafted.draft)
	}
	return nil
}

// checkInputs refuses a question whose inputs, after an edit of them or of the template, do not
// stand as drafting would keep them.
func checkInputs(ctx context.Context, q *interfaces.RequirementQuestion, spec *interfaces.RequirementSpec,
	draft *interfaces.Draft) error {
	var inputs []interfaces.RequirementInput
	if q.Inputs != nil {
		inputs = *q.Inputs
	}
	names := map[string]bool{}
	for _, in := range inputs {
		if names[in.Name] {
			return invalidItem(ctx, fmt.Sprintf("input %s: %s", in.Name, inputDuplicateName))
		}
		names[in.Name] = true
		if problem := inputProblem(in, spec, draft, q.Template); problem != "" {
			return invalidItem(ctx, fmt.Sprintf("input %s: %s", in.Name, problem))
		}
	}
	if q.Inputs != nil && needsReferenceTime(q.RelativeWindow, inputs) {
		return invalidItem(ctx, rejectedWindowWithoutRefTime)
	}
	return nil
}

func applyRule(ctx context.Context, r *interfaces.RequirementRule, edit interfaces.RequirementItemEdit) error {
	return setText(ctx, "statement", edit.Statement, maxItemTextRunes, true, &r.Statement)
}

// nextID is the next id of a kind: one past the highest ever given, recorded in the specification,
// so a removed item's id, the highest one included, is not given to another.
func nextID[T any](spec *interfaces.RequirementSpec, prefix string, items []T, idOf func(T) string) string {
	highest := spec.LastIDs[prefix]
	for _, item := range items {
		if n, err := strconv.Atoi(strings.TrimPrefix(idOf(item), prefix)); err == nil && n > highest {
			highest = n
		}
	}
	if spec.LastIDs == nil {
		spec.LastIDs = map[string]int{}
	}
	spec.LastIDs[prefix] = highest + 1
	return fmt.Sprintf("%s%d", prefix, highest+1)
}

// recordIDs notes the ids items have before one is removed, so removing the highest does not
// free it.
func recordIDs[T any](spec *interfaces.RequirementSpec, prefix string, items []T, idOf func(T) string) {
	for _, item := range items {
		if n, err := strconv.Atoi(strings.TrimPrefix(idOf(item), prefix)); err == nil && n > spec.LastIDs[prefix] {
			if spec.LastIDs == nil {
				spec.LastIDs = map[string]int{}
			}
			spec.LastIDs[prefix] = n
		}
	}
}

func find[T any](items []T, id string, idOf func(T) string) (int, bool) {
	i := slices.IndexFunc(items, func(item T) bool { return idOf(item) == id })
	return i, i >= 0
}

func scenarioID(s *interfaces.RequirementScenario) string { return s.ID }
func conceptID(c *interfaces.RequirementConcept) string   { return c.ID }
func questionID(q *interfaces.RequirementQuestion) string { return q.ID }
func ruleID(r *interfaces.RequirementRule) string         { return r.ID }

func notFound(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
}

func (s *requirementService) EditItem(ctx context.Context, token string, projectID string, kind string,
	itemID string, edit interfaces.RequirementItemEdit, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "EditRequirementItem")
	defer span.End()

	req, err := s.update(ctx, token, projectID, edit.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
			switch kind {
			case interfaces.REQUIREMENT_KIND_SCENARIOS:
				i, ok := find(spec.Scenarios, itemID, scenarioID)
				if !ok {
					return notFound(ctx)
				}
				if err := onlyFields(ctx, edit, kind); err != nil {
					return err
				}
				spec.Scenarios[i].Edited = true
				return applyScenario(ctx, spec.Scenarios[i], edit)
			case interfaces.REQUIREMENT_KIND_CONCEPTS:
				i, ok := find(spec.Concepts, itemID, conceptID)
				if !ok {
					return notFound(ctx)
				}
				if err := onlyFields(ctx, edit, kind); err != nil {
					return err
				}
				spec.Concepts[i].Edited = true
				return applyConcept(ctx, spec.Concepts[i], edit)
			case interfaces.REQUIREMENT_KIND_QUESTIONS:
				i, ok := find(spec.Questions, itemID, questionID)
				if !ok {
					return notFound(ctx)
				}
				if err := onlyFields(ctx, edit, kind); err != nil {
					return err
				}
				spec.Questions[i].Edited = true
				return applyQuestion(ctx, spec.Questions[i], edit, spec, drafted)
			case interfaces.REQUIREMENT_KIND_RULES:
				i, ok := find(spec.Rules, itemID, ruleID)
				if !ok {
					return notFound(ctx)
				}
				if err := onlyFields(ctx, edit, kind); err != nil {
					return err
				}
				spec.Rules[i].Edited = true
				return applyRule(ctx, spec.Rules[i], edit)
			}
			return notFound(ctx)
		})
	if err != nil {
		span.SetStatus(codes.Error, "Edit item failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) AddItem(ctx context.Context, token string, projectID string, kind string,
	edit interfaces.RequirementItemEdit, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AddRequirementItem")
	defer span.End()

	empty := []interfaces.RequirementCitation{}
	req, err := s.update(ctx, token, projectID, edit.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
			if !slices.Contains(itemKinds, kind) {
				return notFound(ctx)
			}
			if err := onlyFields(ctx, edit, kind); err != nil {
				return err
			}
			switch kind {
			case interfaces.REQUIREMENT_KIND_SCENARIOS:
				if edit.Name == nil {
					return invalidItem(ctx, "name is empty")
				}
				s := &interfaces.RequirementScenario{ID: nextID(spec, "s", spec.Scenarios, scenarioID), Knowledge: []string{},
					Capabilities: []interfaces.RequirementCapability{}, Citations: empty, Edited: true}
				if err := applyScenario(ctx, s, edit); err != nil {
					return err
				}
				spec.Scenarios = append(spec.Scenarios, s)
			case interfaces.REQUIREMENT_KIND_CONCEPTS:
				if edit.Term == nil {
					return invalidItem(ctx, "term is empty")
				}
				c := &interfaces.RequirementConcept{ID: nextID(spec, "c", spec.Concepts, conceptID), Category: "object",
					Citations: empty, Edited: true}
				if err := applyConcept(ctx, c, edit); err != nil {
					return err
				}
				spec.Concepts = append(spec.Concepts, c)
			case interfaces.REQUIREMENT_KIND_QUESTIONS:
				if edit.Text == nil {
					return invalidItem(ctx, "text is empty")
				}
				q := &interfaces.RequirementQuestion{ID: nextID(spec, "q", spec.Questions, questionID), ScenarioIDs: []string{},
					Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
					Basis: interfaces.RequirementBasis{ConceptIDs: []string{}, Metrics: []string{}, Functions: []string{},
						Skills: []string{}},
					Needs: []interfaces.RequirementItemRef{}, NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
					Citations: empty, Edited: true}
				if err := applyQuestion(ctx, q, edit, spec, drafted); err != nil {
					return err
				}
				spec.Questions = append(spec.Questions, q)
			case interfaces.REQUIREMENT_KIND_RULES:
				if edit.Statement == nil {
					return invalidItem(ctx, "statement is empty")
				}
				r := &interfaces.RequirementRule{ID: nextID(spec, "r", spec.Rules, ruleID), CompilesTo: "other",
					Citations: empty, Edited: true}
				if err := applyRule(ctx, r, edit); err != nil {
					return err
				}
				spec.Rules = append(spec.Rules, r)
			default:
				return notFound(ctx)
			}
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Add item failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) DeleteItem(ctx context.Context, token string, projectID string, kind string,
	itemID string, updateTime int64, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteRequirementItem")
	defer span.End()

	req, err := s.update(ctx, token, projectID, updateTime, caller,
		func(spec *interfaces.RequirementSpec, _ *draftedItems) error {
			switch kind {
			case interfaces.REQUIREMENT_KIND_SCENARIOS:
				i, ok := find(spec.Scenarios, itemID, scenarioID)
				if !ok {
					return notFound(ctx)
				}
				recordIDs(spec, "s", spec.Scenarios, scenarioID)
				spec.Scenarios = slices.Delete(spec.Scenarios, i, i+1)
				for _, q := range spec.Questions {
					q.ScenarioIDs = slices.DeleteFunc(q.ScenarioIDs, func(id string) bool { return id == itemID })
				}
			case interfaces.REQUIREMENT_KIND_CONCEPTS:
				i, ok := find(spec.Concepts, itemID, conceptID)
				if !ok {
					return notFound(ctx)
				}
				recordIDs(spec, "c", spec.Concepts, conceptID)
				spec.Concepts = slices.Delete(spec.Concepts, i, i+1)
				for _, q := range spec.Questions {
					q.Basis.ConceptIDs = slices.DeleteFunc(q.Basis.ConceptIDs, func(id string) bool { return id == itemID })
					// A question that loses an input is no longer known to be asked about
					// only the rest: its inputs are unknown until drafted again or set.
					if q.Inputs != nil && slices.ContainsFunc(*q.Inputs,
						func(in interfaces.RequirementInput) bool { return in.ConceptID == itemID }) {
						q.Inputs = nil
					}
				}
				followConcepts(spec)
			case interfaces.REQUIREMENT_KIND_QUESTIONS:
				i, ok := find(spec.Questions, itemID, questionID)
				if !ok {
					return notFound(ctx)
				}
				recordIDs(spec, "q", spec.Questions, questionID)
				spec.Questions = slices.Delete(spec.Questions, i, i+1)
			case interfaces.REQUIREMENT_KIND_RULES:
				i, ok := find(spec.Rules, itemID, ruleID)
				if !ok {
					return notFound(ctx)
				}
				recordIDs(spec, "r", spec.Rules, ruleID)
				spec.Rules = slices.Delete(spec.Rules, i, i+1)
			default:
				return notFound(ctx)
			}
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Delete item failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) MapConcept(ctx context.Context, token string, projectID string, conceptID string,
	mapping interfaces.RequirementMapping, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "MapRequirementConcept")
	defer span.End()

	req, err := s.update(ctx, token, projectID, mapping.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
			i, ok := find(spec.Concepts, conceptID, func(c *interfaces.RequirementConcept) string { return c.ID })
			if !ok {
				return notFound(ctx)
			}
			if mapping.MapsTo != nil && !drafted.has(*mapping.MapsTo) {
				return invalidItem(ctx, mapping.MapsTo.Kind+" "+mapping.MapsTo.ID+" is not in the draft")
			}
			spec.Concepts[i].MapsTo, spec.Concepts[i].Edited = mapping.MapsTo, true
			followConcepts(spec)
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Map concept failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) ResolveConflict(ctx context.Context, token string, projectID string, conflictID string,
	resolve interfaces.RequirementResolve, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResolveRequirementConflict")
	defer span.End()

	invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidResolution)
	req, err := s.update(ctx, token, projectID, resolve.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, _ *draftedItems) error {
			i, ok := find(spec.Conflicts, conflictID, func(k *interfaces.RequirementConflict) string { return k.ID })
			if !ok {
				return notFound(ctx)
			}
			conflict := spec.Conflicts[i]
			reason, textValue := strings.TrimSpace(resolve.Reason), strings.TrimSpace(resolve.Text)
			if reason == "" || utf8.RuneCountInString(reason) > maxItemTextRunes || utf8.RuneCountInString(textValue) > maxItemTextRunes {
				return invalid
			}
			if resolve.DocumentID != "" && !slices.ContainsFunc(conflict.Sides, func(side interfaces.RequirementConflictSide) bool {
				return side.DocumentID == resolve.DocumentID
			}) {
				return invalid
			}
			// Handed to the business, a conflict is left unresolved: no side or conclusion with it.
			if resolve.Deferred && (resolve.DocumentID != "" || textValue != "") {
				return invalid
			}
			if !resolve.Deferred && resolve.DocumentID == "" && textValue == "" {
				return invalid
			}
			conflict.Resolution = &interfaces.RequirementResolution{DocumentID: resolve.DocumentID, Text: textValue,
				Reason: reason, By: caller, Time: s.now()}
			conflict.Deferred = resolve.Deferred
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Resolve conflict failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

func (s *requirementService) DecideChoice(ctx context.Context, token string, projectID string, choiceID string,
	decide interfaces.RequirementDecide, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DecideRequirementChoice")
	defer span.End()

	req, err := s.update(ctx, token, projectID, decide.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, _ *draftedItems) error {
			i, ok := find(spec.Choices, choiceID, func(f *interfaces.RequirementChoice) string { return f.ID })
			if !ok {
				return notFound(ctx)
			}
			choice := spec.Choices[i]
			if !slices.ContainsFunc(choice.Options, func(o interfaces.RequirementOption) bool { return o.ID == decide.OptionID }) {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidOption)
			}
			basis := strings.TrimSpace(decide.Basis)
			if utf8.RuneCountInString(basis) > maxItemTextRunes {
				return invalidItem(ctx, fmt.Sprintf("basis is longer than %d characters", maxItemTextRunes))
			}
			choice.Decision = &interfaces.RequirementChoiceDecision{OptionID: decide.OptionID, Basis: basis,
				By: caller, Time: s.now()}
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Decide choice failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}
