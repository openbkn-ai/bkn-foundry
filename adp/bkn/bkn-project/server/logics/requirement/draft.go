// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// requirementAgentID is the bkn-agent task Agent that drafts specifications.
const requirementAgentID = "bkn-project-requirements"

// requirementRulesVersion names the checks drafting applies to what the Agent proposes; it moves
// whenever they could keep or drop items differently.
const requirementRulesVersion = 1

// maxInputRunes bounds the documents one drafting reads, in characters: they go to the Agent in
// one message.
const maxInputRunes = 120000

// maxMessageTables bounds the tables named to the Agent: they are clues, and a catalog may hold
// thousands. The specification counts those left out, so the owner sees it.
const maxMessageTables = 500

// agentDeadline is how long drafting waits for the Agent: its own time limit, with a minute for
// bkn-agent to record the outcome. pollInterval is how often it asks.
const (
	agentDeadline = 6 * time.Minute
	pollInterval  = 2 * time.Second
)

// taskReadTimeout bounds one read of the Agent's task, so a bkn-agent that takes the connection
// and never answers cannot hold drafting past its deadline.
var taskReadTimeout = 30 * time.Second

// Why drafting failed, stored as codes for the client to word.
const (
	failureInterrupted      = "interrupted"
	failureAgentUnavailable = "agent_unavailable"
	failureAgentRejected    = "agent_rejected"
	failureAgentFailed      = "agent_failed"
	failureAgentTimeout     = "agent_timeout"
	failureOutputInvalid    = "output_invalid"
)

// Kinds of rejected items, and the reason a choice is rejected besides its quotes.
const (
	rejectedScenario      = "scenario"
	rejectedConcept       = "concept"
	rejectedQuestion      = "question"
	rejectedRule          = "rule"
	rejectedConflict      = "conflict"
	rejectedChoice        = "choice"
	rejectedTooFewOptions = "too_few_options"
)

// draftOutput is what the Agent returns in pass draft. Its ids are its own, for items to refer to
// each other within the output; the server gives every item its own id.
type draftOutput struct {
	Scenarios []agentScenario `json:"scenarios"`
	Concepts  []agentConcept  `json:"concepts"`
	Questions []agentQuestion `json:"questions"`
	Rules     []agentRule     `json:"rules"`
	Conflicts []agentConflict `json:"conflicts"`
	Choices   []agentChoice   `json:"choices"`
	DocTypes  []agentDocType  `json:"doc_types"`
}

type agentCitation struct {
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
}

type agentScenario struct {
	ID               string                             `json:"id"`
	Name             string                             `json:"name"`
	Actor            string                             `json:"actor"`
	Trigger          string                             `json:"trigger"`
	Decision         string                             `json:"decision"`
	Knowledge        []string                           `json:"knowledge"`
	Capabilities     []interfaces.RequirementCapability `json:"capabilities"`
	Loop             *interfaces.RequirementLoop        `json:"loop"`
	Citations        []agentCitation                    `json:"citations"`
	SupplementReason string                             `json:"supplement_reason"`
}

type agentConcept struct {
	ID                string                         `json:"id"`
	Term              string                         `json:"term"`
	Category          string                         `json:"category"`
	Definition        string                         `json:"definition"`
	NotOntologyReason string                         `json:"not_ontology_reason"`
	MapsTo            *interfaces.RequirementItemRef `json:"maps_to"`
	Citations         []agentCitation                `json:"citations"`
	SupplementReason  string                         `json:"supplement_reason"`
}

type agentQuestion struct {
	ID               string          `json:"id"`
	Text             string          `json:"text"`
	ScenarioIDs      []string        `json:"scenario_ids"`
	Type             string          `json:"type"`
	Layer            string          `json:"layer"`
	Template         string          `json:"template"`
	AnswerShape      string          `json:"answer_shape"`
	ConceptIDs       []string        `json:"concept_ids"`
	Metrics          []string        `json:"metrics"`
	Functions        []string        `json:"functions"`
	Skills           []string        `json:"skills"`
	Citations        []agentCitation `json:"citations"`
	SupplementReason string          `json:"supplement_reason"`
}

type agentRule struct {
	Statement  string          `json:"statement"`
	CompilesTo string          `json:"compiles_to"`
	Citations  []agentCitation `json:"citations"`
}

type agentConflict struct {
	Topic string              `json:"topic"`
	Kind  string              `json:"kind"`
	Sides []agentConflictSide `json:"sides"`
}

type agentConflictSide struct {
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
	Position   string `json:"position"`
}

type agentChoice struct {
	Question            string          `json:"question"`
	Options             []agentOption   `json:"options"`
	RecommendedOptionID string          `json:"recommended_option_id"`
	Reason              string          `json:"reason"`
	Citations           []agentCitation `json:"citations"`
}

type agentOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

type agentDocType struct {
	DocumentID string `json:"document_id"`
	DocType    string `json:"doc_type"`
}

// JSON Schema builders for the response format.
// An object without required properties leaves "required" out: JSON Schema has no null for it.
func schemaObject(properties map[string]any, required ...string) map[string]any {
	object := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		object["required"] = required
	}
	return object
}

func schemaArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func schemaString() map[string]any { return map[string]any{"type": "string"} }

func schemaEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func schemaStrings() map[string]any { return schemaArray(schemaString()) }

// draftResponseFormat is the JSON Schema of pass draft.
func draftResponseFormat() map[string]any {
	citations := schemaArray(schemaObject(map[string]any{
		"document_id": schemaString(), "quote": schemaString(),
	}, "document_id", "quote"))
	return schemaObject(map[string]any{
		"scenarios": schemaArray(schemaObject(map[string]any{
			"id": schemaString(), "name": schemaString(), "actor": schemaString(), "trigger": schemaString(),
			"decision": schemaString(), "knowledge": schemaStrings(),
			"capabilities": schemaArray(schemaObject(map[string]any{
				"kind": schemaEnum("metric", "function", "skill", "app"), "name": schemaString(),
				"definition": schemaString(),
			}, "kind", "name")),
			"loop": schemaObject(map[string]any{
				"discover": schemaString(), "judge": schemaString(), "suggest": schemaString(),
				"confirm": schemaString(), "execute": schemaString(), "state_change": schemaString(),
				"track": schemaString(),
			}),
			"citations": citations, "supplement_reason": schemaString(),
		}, "id", "name", "citations")),
		"concepts": schemaArray(schemaObject(map[string]any{
			"id": schemaString(), "term": schemaString(),
			"category":   schemaEnum("object", "event", "property", "relation", "evidence", "not_ontology"),
			"definition": schemaString(), "not_ontology_reason": schemaString(),
			"maps_to": schemaObject(map[string]any{
				"kind": schemaEnum(interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, interfaces.REQUIREMENT_ITEM_RELATION_TYPE),
				"id":   schemaString(),
			}, "kind", "id"),
			"citations": citations, "supplement_reason": schemaString(),
		}, "id", "term", "category", "citations")),
		"questions": schemaArray(schemaObject(map[string]any{
			"id": schemaString(), "text": schemaString(), "scenario_ids": schemaStrings(),
			"type": schemaEnum("point_in_time", "provenance", "filter", "compare", "aggregate", "count",
				"disambiguate", "judge", "metric"),
			"layer":    schemaEnum(interfaces.REQUIREMENT_LAYER_KNOWLEDGE, interfaces.REQUIREMENT_LAYER_CAPABILITY),
			"template": schemaString(), "answer_shape": schemaString(), "concept_ids": schemaStrings(),
			"metrics": schemaStrings(), "functions": schemaStrings(), "skills": schemaStrings(),
			"citations": citations, "supplement_reason": schemaString(),
		}, "id", "text", "type", "layer", "citations")),
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
	}, "scenarios", "concepts", "questions", "rules", "conflicts", "choices", "doc_types")
}

// draftMessage is the user message of pass draft: the documents block by block, the tables of
// the project's catalogs by name, and the items already drafted with their ids, as data.
type draftMessage struct {
	Documents     []messageDocument     `json:"documents"`
	Tables        []messageTable        `json:"tables"`
	ObjectTypes   []messageObjectType   `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
}

type messageDocument struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Blocks []messageBlock `json:"blocks"`
}

type messageBlock struct {
	ID      string `json:"id"`
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text"`
}

type messageTable struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type messageObjectType struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

type messageRelationType struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// buildDraftMessage also returns how many tables it left out.
func buildDraftMessage(language string, docs []*interfaces.RequirementDocument, tables []*interfaces.VegaTable,
	draft *interfaces.Draft) (string, int, error) {
	msg := draftMessage{
		Documents: []messageDocument{}, Tables: []messageTable{},
		ObjectTypes: []messageObjectType{}, RelationTypes: []messageRelationType{},
	}
	for _, d := range docs {
		content := []rune(d.Content)
		md := messageDocument{ID: d.ID, Name: d.Name, Type: d.DocType, Blocks: []messageBlock{}}
		for _, b := range d.Blocks {
			if b.Start < 0 || b.End > len(content) || b.Start >= b.End {
				continue
			}
			md.Blocks = append(md.Blocks, messageBlock{ID: b.ID, Heading: b.Heading, Text: string(content[b.Start:b.End])})
		}
		msg.Documents = append(msg.Documents, md)
	}
	for _, t := range tables[:min(len(tables), maxMessageTables)] {
		msg.Tables = append(msg.Tables, messageTable{Name: t.Name, Description: t.Description})
	}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			msg.ObjectTypes = append(msg.ObjectTypes, messageObjectType{ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases})
		}
		for _, rt := range draft.Items.RelationTypes {
			msg.RelationTypes = append(msg.RelationTypes, messageRelationType{ID: rt.ID, Name: rt.Name,
				Source: rt.Source, Target: rt.Target})
		}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", 0, err
	}
	return "pass draft\nlanguage: " + language + "\n" + string(body), len(tables) - len(msg.Tables), nil
}

// citationsOf turns the Agent's citations into the specification's, still to be located.
func citationsOf(cs []agentCitation) []interfaces.RequirementCitation {
	out := make([]interfaces.RequirementCitation, 0, len(cs))
	for _, c := range cs {
		out = append(out, interfaces.RequirementCitation{DocumentID: c.DocumentID, Quote: c.Quote})
	}
	return out
}

// basisOf checks an item's citations: the ones found, or why it is dropped. An item that may
// stand without a basis in the documents says why in its supplement reason; any quote it does
// give must still be found.
func basisOf(cs []agentCitation, supplementReason string, docs map[string]*interfaces.RequirementDocument,
	supplementAllowed bool) ([]interfaces.RequirementCitation, string) {
	kept, reason := checkCitations(citationsOf(cs), docs)
	if reason != "" {
		return nil, reason
	}
	if len(kept) == 0 && !(supplementAllowed && strings.TrimSpace(supplementReason) != "") {
		return nil, rejectedQuoteNotFound
	}
	return kept, ""
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// buildSpec checks what the Agent proposed and builds the specification from what passes, with
// the server's ids; everything dropped is listed in rejected. It also returns the document types
// the Agent suggests, for known documents and types only.
func buildSpec(out *draftOutput, docs map[string]*interfaces.RequirementDocument,
	draft *interfaces.Draft) (*interfaces.RequirementSpec, map[string]string) {
	spec := &interfaces.RequirementSpec{
		Scenarios: []*interfaces.RequirementScenario{}, Concepts: []*interfaces.RequirementConcept{},
		Questions: []*interfaces.RequirementQuestion{}, Rules: []*interfaces.RequirementRule{},
		Conflicts: []*interfaces.RequirementConflict{}, Choices: []*interfaces.RequirementChoice{},
		Rejected: []*interfaces.RequirementRejected{},
	}
	reject := func(kind, text, reason string) {
		spec.Rejected = append(spec.Rejected, &interfaces.RequirementRejected{Kind: kind, Text: text, Reason: reason})
	}
	drafted := map[interfaces.RequirementItemRef]bool{}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = true
		}
		for _, rt := range draft.Items.RelationTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = true
		}
	}

	scenarioIDs := map[string]string{}
	for _, s := range out.Scenarios {
		citations, reason := basisOf(s.Citations, s.SupplementReason, docs, true)
		if reason != "" {
			reject(rejectedScenario, s.Name, reason)
			continue
		}
		id := fmt.Sprintf("s%d", len(spec.Scenarios)+1)
		if s.ID != "" {
			scenarioIDs[s.ID] = id
		}
		capabilities := s.Capabilities
		if capabilities == nil {
			capabilities = []interfaces.RequirementCapability{}
		}
		spec.Scenarios = append(spec.Scenarios, &interfaces.RequirementScenario{
			ID: id, Name: s.Name, Actor: s.Actor, Trigger: s.Trigger, Decision: s.Decision,
			Knowledge: orEmpty(s.Knowledge), Capabilities: capabilities, Loop: s.Loop,
			Citations: citations, SupplementReason: s.SupplementReason,
		})
	}

	concepts := map[string]*interfaces.RequirementConcept{}
	for _, c := range out.Concepts {
		citations, reason := basisOf(c.Citations, c.SupplementReason, docs, true)
		if reason != "" {
			reject(rejectedConcept, c.Term, reason)
			continue
		}
		concept := &interfaces.RequirementConcept{
			ID: fmt.Sprintf("c%d", len(spec.Concepts)+1), Term: c.Term, Category: c.Category,
			Definition: c.Definition, NotOntologyReason: c.NotOntologyReason,
			Citations: citations, SupplementReason: c.SupplementReason,
		}
		// The Agent judges which drafted item a concept is; it may name only one the draft has.
		if c.MapsTo != nil && drafted[*c.MapsTo] {
			ref := *c.MapsTo
			concept.MapsTo = &ref
		}
		if c.ID != "" {
			concepts[c.ID] = concept
		}
		spec.Concepts = append(spec.Concepts, concept)
	}

	for _, q := range out.Questions {
		citations, reason := basisOf(q.Citations, q.SupplementReason, docs, true)
		if reason == "" && len(citations) > 0 && !numbersInQuotes(q.Text, citations) {
			reason = rejectedNumberNotInQuote
		}
		if reason != "" {
			reject(rejectedQuestion, q.Text, reason)
			continue
		}
		// A question that rests on a metric, function or skill needs more than the ontology and its
		// facts, whatever layer the Agent gave it: it is not asked in the knowledge regression.
		layer := interfaces.REQUIREMENT_LAYER_CAPABILITY
		if q.Layer == interfaces.REQUIREMENT_LAYER_KNOWLEDGE && len(q.Metrics)+len(q.Functions)+len(q.Skills) == 0 {
			layer = interfaces.REQUIREMENT_LAYER_KNOWLEDGE
		}
		question := &interfaces.RequirementQuestion{
			ID: fmt.Sprintf("q%d", len(spec.Questions)+1), Text: q.Text, ScenarioIDs: []string{},
			Type: q.Type, Layer: layer, Template: q.Template, AnswerShape: q.AnswerShape,
			Basis: interfaces.RequirementBasis{ConceptIDs: []string{}, Metrics: orEmpty(q.Metrics),
				Functions: orEmpty(q.Functions), Skills: orEmpty(q.Skills)},
			Needs: []interfaces.RequirementItemRef{}, NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
			Citations: citations, SupplementReason: q.SupplementReason,
		}
		for _, sid := range q.ScenarioIDs {
			if id, ok := scenarioIDs[sid]; ok && !slices.Contains(question.ScenarioIDs, id) {
				question.ScenarioIDs = append(question.ScenarioIDs, id)
			}
		}
		for _, cid := range q.ConceptIDs {
			concept, ok := concepts[cid]
			if !ok || slices.Contains(question.Basis.ConceptIDs, concept.ID) {
				continue
			}
			question.Basis.ConceptIDs = append(question.Basis.ConceptIDs, concept.ID)
			if concept.MapsTo != nil && !slices.Contains(question.Needs, *concept.MapsTo) {
				question.Needs = append(question.Needs, *concept.MapsTo)
			}
		}
		spec.Questions = append(spec.Questions, question)
	}

	for _, r := range out.Rules {
		citations, reason := basisOf(r.Citations, "", docs, false)
		if reason == "" && !numbersInQuotes(r.Statement, citations) {
			reason = rejectedNumberNotInQuote
		}
		if reason != "" {
			reject(rejectedRule, r.Statement, reason)
			continue
		}
		spec.Rules = append(spec.Rules, &interfaces.RequirementRule{
			ID: fmt.Sprintf("r%d", len(spec.Rules)+1), Statement: r.Statement, CompilesTo: r.CompilesTo,
			Citations: citations,
		})
	}

	for _, k := range out.Conflicts {
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
			ID: fmt.Sprintf("k%d", len(spec.Conflicts)+1), Topic: k.Topic, Kind: k.Kind, Sides: sides,
			SuggestedDocumentID: suggestedDocument(k.Kind, sides, docs),
		})
	}

	for _, f := range out.Choices {
		citations, reason := basisOf(f.Citations, "", docs, false)
		if reason == "" && len(f.Options) < 2 {
			reason = rejectedTooFewOptions
		}
		if reason != "" {
			reject(rejectedChoice, f.Question, reason)
			continue
		}
		choice := &interfaces.RequirementChoice{
			ID: fmt.Sprintf("f%d", len(spec.Choices)+1), Question: f.Question, Options: []interfaces.RequirementOption{},
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

	types := map[string]string{}
	for _, t := range out.DocTypes {
		if _, ok := docs[t.DocumentID]; ok && interfaces.IsRequirementDocType(t.DocType) {
			types[t.DocumentID] = t.DocType
		}
	}
	return spec, types
}

// startFailure tells why the Agent did not start: agent_rejected when bkn-agent refused the request
// itself (4xx: an Agent missing, a request it does not take, no account), which no retry mends;
// agent_unavailable when it could not be reached or failed in itself (a connection error or 5xx).
func startFailure(err error) string {
	var agentErr *interfaces.BknAgentError
	if errors.As(err, &agentErr) && agentErr.HTTPCode >= 400 && agentErr.HTTPCode < 500 {
		return failureAgentRejected
	}
	return failureAgentUnavailable
}

// requirementInputsOf records what a specification is drafted from, in a stable order.
func requirementInputsOf(docs []*interfaces.RequirementDocument, project *interfaces.Project) *interfaces.RequirementInputs {
	inputs := &interfaces.RequirementInputs{
		RulesVersion:   requirementRulesVersion,
		Documents:      make([]interfaces.RequirementInputDocument, 0, len(docs)),
		CatalogIDs:     append([]string{}, project.CatalogIDs...),
		NamingLanguage: project.NamingLanguage,
	}
	for _, d := range docs {
		inputs.Documents = append(inputs.Documents, interfaces.RequirementInputDocument{ID: d.ID, SHA256: d.SHA256, DocType: d.DocType})
	}
	slices.SortFunc(inputs.Documents, func(a, b interfaces.RequirementInputDocument) int { return cmp.Compare(a.ID, b.ID) })
	slices.Sort(inputs.CatalogIDs)
	return inputs
}

func (s *requirementService) GenerateRequirement(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, discardDecisions bool) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GenerateRequirement")
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
	if len(entry.CatalogIDs) == 0 {
		span.SetStatus(codes.Error, "No catalogs")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_NoCatalogs)
	}
	total := 0
	for _, d := range docs {
		total += utf8.RuneCountInString(d.Content)
	}
	if total > maxInputRunes {
		span.SetStatus(codes.Error, "Inputs too large")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InputsTooLarge)
	}
	previous, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	version := 1
	if ok && previous.Version > 0 {
		version = previous.Version
		// A specification an ontology confirmation was made against is frozen: drafting again
		// starts the next version.
		draft, found, err := s.da.GetDraft(ctx, projectID)
		if err != nil {
			span.SetStatus(codes.Error, "Get draft failed")
			return nil, internalError(ctx, "Get draft failed", err)
		}
		if found && draft.Items.Confirmation != nil && draft.Items.Confirmation.RequirementVersion == previous.Version {
			version++
		}
	}

	now := s.now()
	req := &interfaces.Requirement{
		ProjectID: projectID, Version: version, Status: interfaces.REQUIREMENT_STATUS_DRAFTING,
		Spec: interfaces.RequirementSpec{}, RunID: s.newID(), Drafter: caller, CreateTime: now, UpdateTime: now,
	}
	// A specification still drafting past the Agent's deadline, with a minute to record it, has
	// ended without being recorded; it no longer holds the project.
	staleBefore := now - agentDeadline.Milliseconds() - time.Minute.Milliseconds()
	started, err := s.ra.StartDrafting(ctx, req, staleBefore, discardDecisions)
	switch {
	case errors.Is(err, interfaces.ErrRequirementHasDecisions):
		span.SetStatus(codes.Error, "Has decisions")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_HasDecisions)
	case err != nil:
		span.SetStatus(codes.Error, "Start drafting failed")
		return nil, internalError(ctx, "Start drafting failed", err)
	case !started:
		span.SetStatus(codes.Error, "Drafting")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	}
	project := entry.Project
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.draft(runCtx, req, project, docs, caller) })
	span.SetStatus(codes.Ok, "")
	return req, nil
}

// draft runs the Agent and records the checked specification, or why drafting failed.
func (s *requirementService) draft(ctx context.Context, req *interfaces.Requirement, project *interfaces.Project,
	docs []*interfaces.RequirementDocument, caller interfaces.AccountInfo) {
	finish := func(status, failure string, spec *interfaces.RequirementSpec, inputs *interfaces.RequirementInputs) {
		done := *req
		done.Status, done.Error, done.Spec, done.Inputs, done.UpdateTime = status, failure, *spec, inputs, s.now()
		if _, err := s.ra.FinishDrafting(ctx, &done); err != nil {
			logger.Errorf("Record requirement drafting of project %s failed: %v", req.ProjectID, err)
		}
	}
	fail := func(failure string, err error) {
		logger.Errorf("Requirement drafting of project %s failed (%s): %v", req.ProjectID, failure, err)
		finish(interfaces.REQUIREMENT_STATUS_FAILED, failure, &interfaces.RequirementSpec{}, nil)
	}

	// Tables and drafted items are clues; drafting goes on without those it cannot read.
	tables, unlisted := []*interfaces.VegaTable{}, []string{}
	for _, catalogID := range project.CatalogIDs {
		listed, err := s.va.ListTables(ctx, caller, catalogID)
		if err != nil {
			logger.Warnf("List tables of catalog %s for requirement drafting failed: %v", catalogID, err)
			unlisted = append(unlisted, catalogID)
			continue
		}
		tables = append(tables, listed...)
	}
	draft, ok, err := s.da.GetDraft(ctx, req.ProjectID)
	if err != nil || !ok {
		draft = nil
	}
	message, omitted, err := buildDraftMessage(project.NamingLanguage, docs, tables, draft)
	if err != nil {
		fail(failureAgentUnavailable, err)
		return
	}
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: requirementAgentID, Message: message, ResponseFormat: draftResponseFormat(),
	})
	if err != nil {
		fail(startFailure(err), err)
		return
	}
	req.AgentTaskID = task.TaskID
	deadline := s.now() + agentDeadline.Milliseconds()
	for !task.Terminal() {
		if s.now() >= deadline {
			fail(failureAgentTimeout, fmt.Errorf("task %s did not finish", task.TaskID))
			return
		}
		s.sleep(pollInterval)
		// A failed or slow read is retried until the deadline: the task goes on in bkn-agent.
		readCtx, cancel := context.WithTimeout(ctx, taskReadTimeout)
		read, err := s.ba.GetTask(readCtx, caller, task.TaskID)
		cancel()
		if err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		fail(failureAgentFailed, errors.New(task.FailureDetail))
		return
	}
	var out draftOutput
	if err := json.Unmarshal([]byte(task.Output), &out); err != nil {
		fail(failureOutputInvalid, err)
		return
	}

	byID := documentsByID(docs)
	spec, types := buildSpec(&out, byID, draft)
	spec.TablesOmitted = omitted
	if len(unlisted) > 0 {
		spec.CatalogsUnlisted = unlisted
	}
	// The Agent's type of a document is taken only while no one has typed it.
	for _, d := range docs {
		suggested, ok := types[d.ID]
		if !ok || d.DocType != interfaces.REQUIREMENT_DOC_OTHER || suggested == d.DocType {
			continue
		}
		// Written only while the document is still typed other: a type the owner set while drafting
		// ran stays, and the specification then reads as drafted from other inputs.
		typed, err := s.ra.SuggestDocumentType(ctx, req.ProjectID, d.ID, suggested, s.now())
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
	finish(interfaces.REQUIREMENT_STATUS_READY, "", spec, requirementInputsOf(docs, project))
}

func (s *requirementService) GetRequirement(ctx context.Context, token string,
	projectID string) (*interfaces.RequirementView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRequirement")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Requirement not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	view := &interfaces.RequirementView{Requirement: req}
	for _, k := range req.Spec.Conflicts {
		if k.Resolution == nil || k.Deferred {
			view.UnresolvedConflicts++
		}
	}
	for _, f := range req.Spec.Choices {
		if f.Decision == nil {
			view.UndecidedChoices++
		}
	}
	if req.Status == interfaces.REQUIREMENT_STATUS_READY {
		docs, err := s.ra.ListDocuments(ctx, projectID)
		if err != nil {
			span.SetStatus(codes.Error, "List documents failed")
			return nil, internalError(ctx, "List documents failed", err)
		}
		view.Stale = !reflect.DeepEqual(req.Inputs, requirementInputsOf(docs, entry.Project))
	}
	span.SetStatus(codes.Ok, "")
	return view, nil
}

func (s *requirementService) FailInterrupted(ctx context.Context) error {
	now := s.now()
	n, err := s.ra.FailDrafting(ctx, failureInterrupted, now)
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Infof("Failed %d requirement draftings a previous run left going", n)
	}
	if n, err = s.ra.FailGenerating(ctx, failureInterrupted, now); err != nil {
		return err
	}
	if n > 0 {
		logger.Infof("Failed %d question guidance generations a previous run left going", n)
	}
	return nil
}
