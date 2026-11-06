// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The concepts run reads the requirement documents before any model exists: the concepts they use,
// each judged, the rules they state, where they conflict, the modelling choices they leave open and
// the kinetic needs they state. A model, when there is one, only lets concepts map to its items.

// conceptCategoriesExtracted are the categories the concepts run judges a concept as.
var conceptCategoriesExtracted = []string{"object", "event", "property", "relation", "evidence",
	interfaces.REQUIREMENT_CONCEPT_KINETIC, "not_ontology"}

// conceptsOutput is what the Agent returns in pass concepts. Its ids are its own, for needs to name
// concepts within the output.
type conceptsOutput struct {
	Concepts   []agentConcept   `json:"concepts"`
	Rules      []agentRule      `json:"rules"`
	Conflicts  []agentConflict  `json:"conflicts"`
	Choices    []agentChoice    `json:"choices"`
	DocTypes   []agentDocType   `json:"doc_types"`
	LogicNeeds []agentLogicNeed `json:"logic_needs"`
}

// conceptsResponseFormat is the JSON Schema of pass concepts.
func conceptsResponseFormat() map[string]any {
	citations := citationsSchema()
	return schemaObject(map[string]any{
		"concepts": schemaArray(conceptSchema(conceptCategoriesExtracted...)),
		"rules": schemaArray(schemaObject(map[string]any{
			"statement":   schemaString(),
			"compiles_to": schemaEnum("constraint", "metric_definition", "code_pattern", "enum", "other"),
			"citations":   citations,
		}, "statement", "compiles_to", "citations")),
		"conflicts": schemaArray(schemaObject(map[string]any{
			"topic": schemaString(),
			"kind":  schemaEnum(conflictTerm, conflictRule, conflictGoal, conflictScope, conflictScenario),
			"sides": schemaArray(schemaObject(map[string]any{
				"document_id": schemaString(), "quote": schemaString(), "position": schemaString(),
			}, "document_id", "quote", "position")),
		}, "topic", "kind", "sides")),
		"choices": schemaArray(schemaObject(map[string]any{
			"question": schemaString(),
			"options": schemaArray(schemaObject(map[string]any{
				"id": schemaString(), "label": schemaString(), "consequence": schemaString(),
			}, "id", "label", "consequence")),
			"recommended_option_id": schemaString(), "reason": schemaString(), "citations": citations,
		}, "question", "options", "recommended_option_id", "reason", "citations")),
		"doc_types": schemaArray(schemaObject(map[string]any{
			"document_id": schemaString(),
			"doc_type": schemaEnum(interfaces.REQUIREMENT_DOC_PRD, interfaces.REQUIREMENT_DOC_POLICY,
				interfaces.REQUIREMENT_DOC_STANDARD, interfaces.REQUIREMENT_DOC_OTHER),
		}, "document_id", "doc_type")),
		"logic_needs": schemaArray(logicNeedSchema()),
	}, "concepts", "rules", "conflicts", "choices", "doc_types", "logic_needs")
}

// conceptsMessage is the user message of pass concepts: the documents block by block, the tables
// of the project's catalogs by name, the adopted items when there are any, and the concepts, rules
// and kinetic needs a person kept, not to be repeated.
type conceptsMessage struct {
	Documents     []messageDocument     `json:"documents"`
	Tables        []messageTable        `json:"tables"`
	ObjectTypes   []messageObjectType   `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
	Kept          messageKeptConcepts   `json:"kept"`
}

type messageKeptConcepts struct {
	Concepts   []messageConcept   `json:"concepts"`
	Rules      []messageRule      `json:"rules"`
	LogicNeeds []messageLogicNeed `json:"logic_needs"`
}

// buildConceptsMessage is the message of pass concepts over spec, as what it keeps leaves it.
func buildConceptsMessage(language string, docs []*interfaces.RequirementDocument, tables []*interfaces.VegaTable,
	draft *interfaces.Draft, spec *interfaces.RequirementSpec) (string, error) {
	msg := conceptsMessage{Documents: messageDocumentsOf(docs)}
	msg.Tables, _ = messageTablesOf(tables)
	msg.ObjectTypes, msg.RelationTypes, _ = messageOntologyOf(draft, false)
	msg.Kept = messageKeptConcepts{Concepts: []messageConcept{}, Rules: messageRulesOf(spec.Rules),
		LogicNeeds: []messageLogicNeed{}}
	for _, c := range messageConceptsOf(spec.Concepts) {
		c.ID = keptPrefix + c.ID
		msg.Kept.Concepts = append(msg.Kept.Concepts, c)
	}
	for _, n := range messageLogicNeedsOf(spec.LogicNeeds) {
		n.ID = keptPrefix + n.ID
		msg.Kept.LogicNeeds = append(msg.Kept.LogicNeeds, n)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return "pass concepts\nlanguage: " + language + "\n" + string(body), nil
}

// keepForConcepts drops from spec what the concepts run extracts anew: the concepts, rules, conflicts,
// choices and kinetic needs it extracted before that no one made theirs and nothing rests on. With
// discard set, only what questions rest on stays of those. What the other runs wrote stays.
func keepForConcepts(spec *interfaces.RequirementSpec, discard bool) {
	// No id once given is given again, of an item dropped here too.
	withLists(spec)
	recordAllIDs(spec, spec)
	concepts, needs := questionRefs(spec.Questions)
	spec.LogicNeeds = slices.DeleteFunc(spec.LogicNeeds, func(n *interfaces.RequirementLogicNeed) bool {
		return n.Source == interfaces.LOGIC_NEED_FROM_DOCUMENTS && !needs[n.ID] && (discard || !n.Accepted && !n.Edited)
	})
	for _, n := range spec.LogicNeeds {
		for _, id := range n.ConceptIDs {
			concepts[id] = true
		}
	}
	spec.Concepts = slices.DeleteFunc(spec.Concepts, func(c *interfaces.RequirementConcept) bool {
		extracted := c.Source == "" || c.Source == interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS
		return extracted && !concepts[c.ID] && (discard || !c.Edited)
	})
	spec.Rules = slices.DeleteFunc(spec.Rules, func(r *interfaces.RequirementRule) bool { return discard || !r.Edited })
	spec.Conflicts = slices.DeleteFunc(spec.Conflicts, func(k *interfaces.RequirementConflict) bool {
		return discard || k.Resolution == nil && !k.Deferred
	})
	spec.Choices = slices.DeleteFunc(spec.Choices, func(f *interfaces.RequirementChoice) bool {
		return discard || f.Decision == nil
	})
}

// buildConcepts checks what the Agent proposed in pass concepts and adds what passes to spec, kept
// as keepForConcepts left it, with the server's ids; everything dropped is listed in rejected. It
// returns the document types the Agent suggests, for known documents and types only.
func buildConcepts(out *conceptsOutput, docs map[string]*interfaces.RequirementDocument, draft *interfaces.Draft,
	spec *interfaces.RequirementSpec) map[string]string {
	const run = interfaces.REQUIREMENT_RUN_CONCEPTS
	spec.Rejected = rejectedOf(spec.Rejected, run)
	reject := func(kind, text, reason string) {
		spec.Rejected = append(spec.Rejected, &interfaces.RequirementRejected{Kind: kind, Text: text, Reason: reason, Run: run})
	}
	keptSpec := *spec
	adopted := adoptedRefs(draft)

	concepts := map[string]string{}
	for _, c := range spec.Concepts {
		concepts[keptPrefix+c.ID] = c.ID
	}
	added := 0
	for _, c := range out.Concepts {
		if i := slices.IndexFunc(keptSpec.Concepts, func(k *interfaces.RequirementConcept) bool { return sameText(k.Term, c.Term) }); i >= 0 {
			if c.ID != "" {
				concepts[c.ID] = keptSpec.Concepts[i].ID
			}
			continue
		}
		// Extracted from the documents, a concept quotes them.
		citations, reason := basisOf(c.Citations, "", docs, false)
		if reason == "" && !slices.Contains(conceptCategoriesExtracted, c.Category) {
			reason = rejectedUnknownKind
		}
		if reason == "" && added == maxSpecConcepts {
			reason = rejectedTooMany
		}
		if reason != "" {
			reject(rejectedConcept, c.Term, reason)
			continue
		}
		added++
		concept := &interfaces.RequirementConcept{
			ID: nextID(spec, "c", spec.Concepts, conceptID), Term: c.Term, Category: c.Category,
			Definition: c.Definition, NotOntologyReason: c.NotOntologyReason, Citations: citations,
			Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS,
		}
		if c.MapsTo != nil && adopted[*c.MapsTo] {
			ref := *c.MapsTo
			concept.MapsTo = &ref
		}
		if c.ID != "" {
			concepts[c.ID] = concept.ID
		}
		spec.Concepts = append(spec.Concepts, concept)
	}

	for _, r := range out.Rules {
		if slices.ContainsFunc(keptSpec.Rules, func(k *interfaces.RequirementRule) bool { return sameText(k.Statement, r.Statement) }) {
			continue
		}
		citations, reason := basisOf(r.Citations, "", docs, false)
		if reason == "" && !numbersInQuotes(r.Statement, citations) {
			reason = rejectedNumberNotInQuote
		}
		if reason != "" {
			reject(rejectedRule, r.Statement, reason)
			continue
		}
		spec.Rules = append(spec.Rules, &interfaces.RequirementRule{
			ID: nextID(spec, "r", spec.Rules, ruleID), Statement: r.Statement, CompilesTo: r.CompilesTo,
			Citations: citations,
		})
	}

	for _, k := range out.Conflicts {
		if slices.ContainsFunc(keptSpec.Conflicts, func(c *interfaces.RequirementConflict) bool { return sameText(c.Topic, k.Topic) }) {
			continue
		}
		sides := []interfaces.RequirementConflictSide{}
		documents := map[string]bool{}
		for _, side := range k.Sides {
			d, ok := docs[side.DocumentID]
			if !ok {
				continue
			}
			if located, ok := locateQuote(d, side.Quote); ok {
				sides = append(sides, interfaces.RequirementConflictSide{RequirementCitation: located, Position: side.Position})
				documents[d.ID] = true
			}
		}
		// A conflict is between documents: with fewer than two left, there is none to resolve.
		if len(documents) < 2 {
			reject(rejectedConflict, k.Topic, rejectedQuoteNotFound)
			continue
		}
		spec.Conflicts = append(spec.Conflicts, &interfaces.RequirementConflict{
			ID: nextID(spec, "k", spec.Conflicts, conflictID), Topic: k.Topic, Kind: k.Kind, Sides: sides,
		})
	}

	for _, f := range out.Choices {
		if slices.ContainsFunc(keptSpec.Choices, func(c *interfaces.RequirementChoice) bool { return sameText(c.Question, f.Question) }) {
			continue
		}
		citations, reason := basisOf(f.Citations, "", docs, false)
		if reason == "" && len(f.Options) < 2 {
			reason = rejectedTooFewOptions
		}
		if reason != "" {
			reject(rejectedChoice, f.Question, reason)
			continue
		}
		choice := &interfaces.RequirementChoice{
			ID: nextID(spec, "f", spec.Choices, choiceID), Question: f.Question, Options: []interfaces.RequirementOption{},
			Reason: f.Reason, Citations: citations,
		}
		for i, o := range f.Options {
			id := fmt.Sprintf("o%d", i+1)
			if o.ID == f.RecommendedOptionID && choice.RecommendedOptionID == "" {
				choice.RecommendedOptionID = id
			}
			choice.Options = append(choice.Options, interfaces.RequirementOption{ID: id, Label: o.Label,
				Consequence: o.Consequence})
		}
		spec.Choices = append(spec.Choices, choice)
	}

	addLogicNeeds(spec, out.LogicNeeds, interfaces.LOGIC_NEED_FROM_DOCUMENTS, docs, draft, concepts, reject)
	mapConcepts(spec, draft)

	types := map[string]string{}
	for _, t := range out.DocTypes {
		if _, ok := docs[t.DocumentID]; ok && interfaces.IsRequirementDocType(t.DocType) {
			types[t.DocumentID] = t.DocType
		}
	}
	return types
}

// ExtractConcepts starts the concepts run of a project; it needs requirement documents, not a model.
func (s *requirementService) ExtractConcepts(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, discardDecisions bool) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ExtractRequirementConcepts")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	docs, err := s.ra.GetDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get documents failed")
		return nil, internalError(ctx, "Get documents failed", err)
	}
	if len(docs) == 0 {
		span.SetStatus(codes.Error, "No documents")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_NoDocuments)
	}
	if err := checkInputsSize(ctx, docs); err != nil {
		span.SetStatus(codes.Error, "Inputs too large")
		return nil, err
	}
	// A model, when there is one, only gives concepts items to map to.
	draft, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !found || draft.Status != interfaces.DRAFT_STATUS_READY {
		draft = nil
	}
	previous, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	spec, version, read := emptySpec(), 1, (*interfaces.Requirement)(nil)
	if ok {
		spec, version, read = withLists(cloneSpec(&previous.Spec)), nextVersion(previous.Version, draft), previous
	}
	keepForConcepts(spec, discardDecisions)
	run, err := s.startRun(ctx, projectID, interfaces.REQUIREMENT_RUN_CONCEPTS, caller, read)
	if err != nil {
		span.SetStatus(codes.Error, "Start run failed")
		return nil, err
	}
	project := entry.Project
	byID := documentsByID(docs)
	p := &partRun{
		kind: interfaces.REQUIREMENT_RUN_CONCEPTS, projectID: projectID, run: run, caller: caller,
		format: conceptsResponseFormat(), newOut: func() any { return &conceptsOutput{} },
	}
	// Tables are clues: they are listed in the background, as the caller.
	p.prepareMessage = func(ctx context.Context) (string, error) {
		tables, _ := s.listTables(ctx, project, caller)
		return buildConceptsMessage(project.NamingLanguage, docs, tables, draft, spec)
	}
	var types map[string]string
	p.check = func(ctx context.Context, out any) {
		checked := cloneSpec(spec)
		types = buildConcepts(out.(*conceptsOutput), byID, draft, checked)
		spec = checked
		// The Agent's type of a document is taken only while no one has typed it.
		for _, d := range docs {
			suggested, ok := types[d.ID]
			if !ok || d.DocType != interfaces.REQUIREMENT_DOC_OTHER || suggested == d.DocType {
				continue
			}
			typed, err := s.ra.SuggestDocumentType(ctx, projectID, d.ID, suggested, s.now())
			if err != nil {
				logger.Warnf("Set the type of requirement document %s failed: %v", d.ID, err)
				continue
			}
			if typed {
				d.DocType = suggested
			}
		}
		// Conflicts are suggested by the types the documents have now.
		for _, k := range spec.Conflicts {
			k.SuggestedDocumentID = suggestedDocument(k.Kind, k.Sides, byID)
		}
		p.inputs = requirementInputsOf(docs, project)
	}
	p.write = func(req *interfaces.Requirement, _ any) {
		runs := req.Spec.Runs
		req.Spec = *spec
		req.Spec.Runs = runs
		req.Version = max(req.Version, version)
	}
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.execute(runCtx, p) })
	span.SetStatus(codes.Ok, "")
	return s.startedRun(ctx, projectID)
}
