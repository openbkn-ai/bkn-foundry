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
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
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

// maxMessageProperties bounds the properties named of one object type, its keys and times first:
// inputs name those, and a wide table would make the message long for little.
const maxMessageProperties = 50

// maxSpecScenarios and maxSpecConcepts bound what a specification keeps, the first ones the Agent
// gives: its answer is one JSON document, and the longer it is, the likelier a model writes it
// wrong or runs out of time. The prompt states the same bounds.
const (
	maxSpecScenarios = 10
	maxSpecConcepts  = 40
)

// agentDeadline is how long drafting waits for the Agent: its own time limit, with a minute for
// bkn-agent to record the outcome. pollInterval is how often it asks. draftDeadline is how long a
// drafting may run: it asks the Agent at most twice.
const (
	agentDeadline = 6 * time.Minute
	pollInterval  = 2 * time.Second
	draftDeadline = 2 * agentDeadline
)

// structuredOutputFailed is how bkn-agent names a failure to give its answer as the schema asks.
const structuredOutputFailed = "StructuredOutputError"

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
	rejectedBasis         = "basis"
	rejectedRule          = "rule"
	rejectedConflict      = "conflict"
	rejectedChoice        = "choice"
	rejectedTooFewOptions = "too_few_options"
	// rejectedTooMany is an item past the most a specification keeps of its kind.
	rejectedTooMany = "too_many"
	// rejectedInputExample is an input's example; rejectedExampleNotInDocuments, one the documents
	// do not state as a whole value.
	rejectedInputExample          = "input_example"
	rejectedExampleNotInDocuments = "example_not_in_documents"
	// rejectedBasisNotInDocuments is a skill a question rests on that no document names.
	rejectedBasisNotInDocuments = "basis_not_in_documents"
	// rejectedFunctionOnFilter is a function a question its own type calls a filter rests on alone.
	rejectedFunctionOnFilter = "function_on_filter"
)

// questionTypeFilter is the type of a question that filters records on their properties.
const questionTypeFilter = "filter"

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
	Inputs           []agentInput    `json:"inputs"`
	RelativeWindow   bool            `json:"relative_window"`
	Citations        []agentCitation `json:"citations"`
	SupplementReason string          `json:"supplement_reason"`
}

// agentInput is an input of a question as the Agent writes it: its concept by the Agent's own id.
type agentInput struct {
	Name      string `json:"name"`
	ConceptID string `json:"concept_id"`
	Property  string `json:"property"`
	Role      string `json:"role"`
	Example   string `json:"example"`
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
			"type": schemaEnum("point_in_time", "provenance", questionTypeFilter, "compare", "aggregate", "count",
				"disambiguate", "judge", "metric"),
			"layer":    schemaEnum(interfaces.REQUIREMENT_LAYER_KNOWLEDGE, interfaces.REQUIREMENT_LAYER_CAPABILITY),
			"template": schemaString(), "answer_shape": schemaString(), "concept_ids": schemaStrings(),
			"metrics": schemaStrings(), "functions": schemaStrings(), "skills": schemaStrings(),
			"inputs": schemaArray(schemaObject(map[string]any{
				"name": schemaString(), "concept_id": schemaString(), "property": schemaString(),
				"role": schemaEnum(interfaces.REQUIREMENT_INPUT_RECORD, interfaces.REQUIREMENT_INPUT_REFERENCE_TIME),
				"example": map[string]any{"type": "string", "description": "A value of this input the documents " +
					"state, such as a record they give as an example, copied as written; empty when they state none."},
			}, "name", "concept_id", "property", "role", "example")),
			"relative_window": map[string]any{"type": "boolean"},
			"citations":       citations, "supplement_reason": schemaString(),
		}, "id", "text", "type", "layer", "inputs", "relative_window", "citations")),
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
// the project's catalogs by name, the adopted items with their ids (an object type with its
// properties and primary keys), and the items a person kept, as data.
type draftMessage struct {
	Documents     []messageDocument     `json:"documents"`
	Tables        []messageTable        `json:"tables"`
	ObjectTypes   []messageObjectType   `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
	Kept          *messageKept          `json:"kept,omitempty"`
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
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Aliases     []string          `json:"aliases,omitempty"`
	PrimaryKeys []string          `json:"primary_keys,omitempty"`
	Properties  []messageProperty `json:"properties"`
	// PropertiesOmitted counts its properties past maxMessageProperties.
	PropertiesOmitted int `json:"properties_omitted,omitempty"`
}

type messageRelationType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Target      string `json:"target"`
}

// questionNotes are what a person should see about a question, computed when it is read.
func questionNotes(q *interfaces.RequirementQuestion) []string {
	var notes []string
	if b := q.Basis; q.Layer == interfaces.REQUIREMENT_LAYER_CAPABILITY &&
		len(b.Metrics)+len(b.Functions)+len(b.Skills) == 0 {
		notes = append(notes, interfaces.QUESTION_NOTE_CAPABILITY_WITHOUT_BASIS)
	}
	var inputs []interfaces.RequirementInput
	if q.Inputs != nil {
		inputs = *q.Inputs
	}
	if needsReferenceTime(q.RelativeWindow, inputs) {
		notes = append(notes, interfaces.QUESTION_NOTE_REFERENCE_TIME_UNBOUND)
	}
	if slices.ContainsFunc(inputs, func(in interfaces.RequirementInput) bool { return in.PropertyFromKey }) {
		notes = append(notes, interfaces.QUESTION_NOTE_INPUT_PROPERTY_FROM_KEY)
	}
	return notes
}

// buildDraftMessage names the adopted items only: candidates no one adopted are not the ontology
// scenarios rest on. It also returns how many tables, and how many properties of object types, it
// left out.
func buildDraftMessage(language string, docs []*interfaces.RequirementDocument, tables []*interfaces.VegaTable,
	draft *interfaces.Draft, kept *interfaces.RequirementSpec) (string, int, int, error) {
	propertiesOmitted := 0
	msg := draftMessage{
		Documents: []messageDocument{}, Tables: []messageTable{},
		ObjectTypes: []messageObjectType{}, RelationTypes: []messageRelationType{}, Kept: messageKeptOf(kept),
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
			if !ot.Adopted {
				continue
			}
			mot := messageObjectType{ID: ot.ID, Name: ot.Name, Description: ot.Description, Aliases: ot.Aliases,
				PrimaryKeys: ot.PrimaryKeys, Properties: []messageProperty{}}
			// Keys name a record and times a moment, as inputs do: they go first.
			rank := func(p *interfaces.DraftProperty) int {
				switch {
				case slices.Contains(ot.PrimaryKeys, p.Name):
					return 0
				case timeTypes[p.Type]:
					return 1
				}
				return 2
			}
			props := slices.Clone(ot.Properties)
			slices.SortStableFunc(props, func(a, b *interfaces.DraftProperty) int { return rank(a) - rank(b) })
			for _, p := range props[:min(len(props), maxMessageProperties)] {
				mp := messageProperty{Name: p.Name, Type: p.Type}
				if p.DisplayName != p.Name {
					mp.DisplayName = p.DisplayName
				}
				mot.Properties = append(mot.Properties, mp)
			}
			mot.PropertiesOmitted = len(props) - len(mot.Properties)
			propertiesOmitted += mot.PropertiesOmitted
			msg.ObjectTypes = append(msg.ObjectTypes, mot)
		}
		for _, rt := range draft.Items.RelationTypes {
			if !rt.Adopted {
				continue
			}
			msg.RelationTypes = append(msg.RelationTypes, messageRelationType{ID: rt.ID, Name: rt.Name,
				Description: rt.Description, Source: rt.Source, Target: rt.Target})
		}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", 0, 0, err
	}
	return "pass draft\nlanguage: " + language + "\n" + string(body), len(tables) - len(msg.Tables), propertiesOmitted, nil
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

// recordValuesInText says the numbers of a question's text its quotes do not show are values of
// the records it names: the text is its template with each {name} slot filled, the template shows
// only numbers its quotes show, and every number standing where a slot is filled is one the
// documents give, as an example of a record. A number outside the slots is a condition, which its
// quotes must show.
func recordValuesInText(q agentQuestion, citations []interfaces.RequirementCitation,
	docs map[string]*interfaces.RequirementDocument) bool {
	template := normalize([]rune(q.Template)).text
	literals := templateSlot.Split(template, -1)
	// Without a slot, the text is the template, whose numbers are its quotes' or not.
	if !numbersInQuotes(template, citations) {
		return false
	}
	pattern := "^"
	for i, l := range literals {
		if i > 0 {
			pattern += "(.+?)"
		}
		pattern += regexp.QuoteMeta(l)
	}
	filled := regexp.MustCompile(pattern + "$").FindStringSubmatch(normalize([]rune(q.Text)).text)
	if filled == nil {
		return false
	}
	given := map[string]bool{}
	for _, d := range docs {
		for _, n := range numbers(d.Content) {
			given[n] = true
		}
	}
	for _, value := range filled[1:] {
		for _, n := range numbers(value) {
			if !given[n] {
				return false
			}
		}
	}
	return true
}

// templateSlot is a {name} slot of a question's template.
var templateSlot = regexp.MustCompile(`\{[^{}]+\}`)

// namedInDocuments says a name stands in a block of the documents, compared without case,
// whitespace, underscores or hyphens, as names are written in prose and as identifiers.
func namedInDocuments(name string, docs map[string]*interfaces.RequirementDocument) bool {
	squeeze := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '_' || r == '-' || r == '\u3000' || unicode.IsSpace(r) {
				return -1
			}
			return r
		}, strings.ToLower(normalize([]rune(s)).text))
	}
	n := squeeze(name)
	if n == "" {
		return false
	}
	for _, d := range docs {
		content := []rune(d.Content)
		for _, b := range d.Blocks {
			if b.Start >= 0 && b.End <= len(content) && b.Start < b.End && strings.Contains(squeeze(string(content[b.Start:b.End])), n) {
				return true
			}
		}
	}
	return false
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// named keeps the names that are not blank, trimmed.
func named(names []string) []string {
	kept := []string{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			kept = append(kept, n)
		}
	}
	return kept
}

// buildSpec checks what the Agent proposed and builds the specification from what passes, with
// the server's ids; everything dropped is listed in rejected. It also returns the document types
// the Agent suggests, for known documents and types only.
func buildSpec(out *draftOutput, docs map[string]*interfaces.RequirementDocument,
	draft *interfaces.Draft, kept *interfaces.RequirementSpec) (*interfaces.RequirementSpec, map[string]string) {
	// What is kept stays as it is; drafted items repeating it are merged into it, and new ones take
	// ids no item had.
	spec := startingSpec(kept)
	keptSpec := *startingSpec(kept)
	reject := func(kind, text, reason string) {
		spec.Rejected = append(spec.Rejected, &interfaces.RequirementRejected{Kind: kind, Text: text, Reason: reason})
	}
	// A concept maps to an adopted item, the ones the Agent was given.
	drafted := map[interfaces.RequirementItemRef]bool{}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = ot.Adopted
		}
		for _, rt := range draft.Items.RelationTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = rt.Adopted
		}
	}

	scenarioIDs := map[string]string{}
	for _, s := range spec.Scenarios {
		scenarioIDs[keptPrefix+s.ID] = s.ID
	}
	added := 0
	for _, s := range out.Scenarios {
		if same := keptScenario(&keptSpec, s.Name); same != nil {
			if s.ID != "" {
				scenarioIDs[s.ID] = same.ID
			}
			continue
		}
		citations, reason := basisOf(s.Citations, s.SupplementReason, docs, true)
		if reason == "" && added == maxSpecScenarios {
			reason = rejectedTooMany
		}
		if reason != "" {
			reject(rejectedScenario, s.Name, reason)
			continue
		}
		added++
		id := nextID(spec, "s", spec.Scenarios, scenarioID)
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
	for _, c := range spec.Concepts {
		concepts[keptPrefix+c.ID] = c
	}
	added = 0
	for _, c := range out.Concepts {
		if i := slices.IndexFunc(keptSpec.Concepts, func(k *interfaces.RequirementConcept) bool { return sameText(k.Term, c.Term) }); i >= 0 {
			if c.ID != "" {
				concepts[c.ID] = keptSpec.Concepts[i]
			}
			continue
		}
		citations, reason := basisOf(c.Citations, c.SupplementReason, docs, true)
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
		if slices.ContainsFunc(keptSpec.Questions, func(k *interfaces.RequirementQuestion) bool { return sameText(k.Text, q.Text) }) {
			continue
		}
		citations, reason := basisOf(q.Citations, q.SupplementReason, docs, true)
		if reason == "" && len(citations) > 0 && !numbersInQuotes(q.Text, citations) && !recordValuesInText(q, citations, docs) {
			reason = rejectedNumberNotInQuote
		}
		if reason != "" {
			reject(rejectedQuestion, q.Text, reason)
			continue
		}
		// A question that rests on a metric, function or skill needs more than the ontology and its
		// facts, whatever layer the Agent gave it: it is not asked in the knowledge regression.
		// Blank names are none: an Agent's placeholder neither moves the question nor stays in its basis.
		metrics, functions := named(q.Metrics), named(q.Functions)
		// A skill is something the documents ask for by name; one the Agent designed to serve the
		// answer is not what the question rests on, and does not make a lookup a capability. Metrics
		// and functions are named by the Agent from the rules, and stand as given.
		skills := []string{}
		for _, s := range named(q.Skills) {
			if namedInDocuments(s, docs) {
				skills = append(skills, s)
			} else {
				reject(rejectedBasis, s+" ("+q.Text+")", rejectedBasisNotInDocuments)
			}
		}
		// A filter on properties names no function: a question typed a filter that rests on functions
		// alone, with no metric or skill, filters all the same, as its type says.
		if q.Type == questionTypeFilter && len(metrics)+len(skills) == 0 {
			for _, f := range functions {
				reject(rejectedBasis, f+" ("+q.Text+")", rejectedFunctionOnFilter)
			}
			functions = []string{}
		}
		// Said capability for nothing that stands, it is a lookup: knowledge.
		dropped := len(named(q.Metrics))+len(named(q.Functions))+len(named(q.Skills)) > 0
		layer := interfaces.REQUIREMENT_LAYER_CAPABILITY
		if (q.Layer == interfaces.REQUIREMENT_LAYER_KNOWLEDGE || dropped) && len(metrics)+len(functions)+len(skills) == 0 {
			layer = interfaces.REQUIREMENT_LAYER_KNOWLEDGE
		}
		question := &interfaces.RequirementQuestion{
			ID: nextID(spec, "q", spec.Questions, questionID), Text: q.Text, ScenarioIDs: []string{},
			Type: q.Type, Layer: layer, Template: q.Template, AnswerShape: q.AnswerShape,
			Basis: interfaces.RequirementBasis{ConceptIDs: []string{}, Metrics: metrics,
				Functions: functions, Skills: skills},
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
		// What it is asked about, kept only when it stands; one that does not is listed rejected.
		inputs := []interfaces.RequirementInput{}
		for _, in := range q.Inputs {
			input := interfaces.RequirementInput{Name: strings.TrimSpace(in.Name), Property: in.Property, Role: in.Role}
			if concept, ok := concepts[in.ConceptID]; ok {
				input.ConceptID = concept.ID
			}
			given := input.Name
			if input.Property != "" {
				given += "." + input.Property
			}
			keyed(&input, spec, draft)
			reason := inputProblem(input, spec, draft, question.Template)
			if reason == "" && slices.ContainsFunc(inputs, func(kept interfaces.RequirementInput) bool { return kept.Name == input.Name }) {
				reason = inputDuplicateName
			}
			if reason != "" {
				reject(rejectedInput, given+" ("+q.Text+")", reason)
				continue
			}
			if input.Example = strings.TrimSpace(in.Example); input.Example != "" {
				cited, ok := exampleCitation(input.Example, docs)
				switch {
				case input.Role != interfaces.REQUIREMENT_INPUT_RECORD:
					// A moment is taken from the data.
					input.Example = ""
				case !ok:
					reject(rejectedInputExample, input.Name+"="+input.Example+" ("+q.Text+")", rejectedExampleNotInDocuments)
					input.Example = ""
				default:
					question.Citations = citeExample(question.Citations, input.Example, cited)
				}
			}
			inputs = append(inputs, input)
		}
		// A period counted back from a time that is not stated depends on when it is asked. The
		// question stays, noted when read; a reference time given but rejected is listed already.
		if q.RelativeWindow && !slices.ContainsFunc(q.Inputs, func(in agentInput) bool {
			return in.Role == interfaces.REQUIREMENT_INPUT_REFERENCE_TIME
		}) {
			reject(rejectedInput, interfaces.REQUIREMENT_INPUT_REFERENCE_TIME+" ("+q.Text+")", rejectedWindowWithoutRefTime)
		}
		question.RelativeWindow = q.RelativeWindow
		// None given is no input value; given but none standing is not known, as before inputs.
		if len(q.Inputs) == 0 || len(inputs) > 0 {
			question.Inputs = &inputs
		}
		spec.Questions = append(spec.Questions, question)
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
			SuggestedDocumentID: suggestedDocument(k.Kind, sides, docs),
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
	// Scenarios are drafted from the adopted ontology; documents only add to it.
	draft, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !found || draft.Status != interfaces.DRAFT_STATUS_READY ||
		!slices.ContainsFunc(draft.Items.ObjectTypes, func(ot *interfaces.DraftObjectType) bool { return ot.Adopted }) {
		span.SetStatus(codes.Error, "No adopted ontology")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_NoAdoptedOntology)
	}
	previous, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	// Drafting again keeps what people made theirs, unless they start over.
	var kept *interfaces.RequirementSpec
	if ok && !discardDecisions {
		kept = keptOf(previous)
	}
	version := 1
	if ok && previous.Version > 0 {
		version = previous.Version
		// A specification an ontology confirmation was made against is frozen: drafting again
		// starts the next version.
		if draft.Items.Confirmation != nil && draft.Items.Confirmation.RequirementVersion == previous.Version {
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
	staleBefore := now - draftDeadline.Milliseconds() - time.Minute.Milliseconds()
	// The decisions made are kept with the rest, so none is discarded unasked.
	started, err := s.ra.StartDrafting(ctx, req, staleBefore, true)
	switch {
	case err != nil:
		span.SetStatus(codes.Error, "Start drafting failed")
		return nil, internalError(ctx, "Start drafting failed", err)
	case !started:
		span.SetStatus(codes.Error, "Drafting")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	}
	project := entry.Project
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.draft(runCtx, req, project, docs, kept, caller) })
	span.SetStatus(codes.Ok, "")
	return req, nil
}

// askDraft runs the Agent once for the specification and waits for it up to agentDeadline: its
// answer, or the failure code with why.
func (s *requirementService) askDraft(ctx context.Context, req *interfaces.Requirement, caller interfaces.AccountInfo,
	message string) (*draftOutput, string, error) {
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: requirementAgentID, Message: message, ResponseFormat: draftResponseFormat(),
	})
	if err != nil {
		return nil, startFailure(err), err
	}
	req.AgentTaskID = task.TaskID
	deadline := s.now() + agentDeadline.Milliseconds()
	for !task.Terminal() {
		if s.now() >= deadline {
			return nil, failureAgentTimeout, fmt.Errorf("task %s did not finish", task.TaskID)
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
		return nil, failureAgentFailed, errors.New(task.FailureDetail)
	}
	var out draftOutput
	if err := json.Unmarshal([]byte(task.Output), &out); err != nil {
		return nil, failureOutputInvalid, err
	}
	return &out, "", nil
}

// draft runs the Agent and records the checked specification, or why drafting failed.
// What is kept stays when drafting fails too.
func (s *requirementService) draft(ctx context.Context, req *interfaces.Requirement, project *interfaces.Project,
	docs []*interfaces.RequirementDocument, kept *interfaces.RequirementSpec, caller interfaces.AccountInfo) {
	finish := func(status, failure string, spec *interfaces.RequirementSpec, inputs *interfaces.RequirementInputs) {
		done := *req
		done.Status, done.Error, done.Spec, done.Inputs, done.UpdateTime = status, failure, *spec, inputs, s.now()
		if _, err := s.ra.FinishDrafting(ctx, &done); err != nil {
			logger.Errorf("Record requirement drafting of project %s failed: %v", req.ProjectID, err)
		}
	}
	fail := func(failure string, err error) {
		logger.Errorf("Requirement drafting of project %s failed (%s): %v", req.ProjectID, failure, err)
		finish(interfaces.REQUIREMENT_STATUS_FAILED, failure, startingSpec(kept), nil)
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
	message, omitted, propertiesOmitted, err := buildDraftMessage(project.NamingLanguage, docs, tables, draft, kept)
	if err != nil {
		fail(failureAgentUnavailable, err)
		return
	}
	out, failure, err := s.askDraft(ctx, req, caller, message)
	// A model without structured output sometimes writes JSON with a field missing or broken, and
	// asked again, writes it whole: it is asked once more. A run that ran out of time is not.
	retried := failure == failureOutputInvalid ||
		failure == failureAgentFailed && strings.Contains(err.Error(), structuredOutputFailed)
	if retried {
		logger.Warnf("Requirement drafting of project %s asks the Agent again (%s): %v", req.ProjectID, failure, err)
		out, failure, err = s.askDraft(ctx, req, caller, message)
	}
	if failure != "" {
		logger.Errorf("Requirement drafting of project %s failed (%s, asked again: %t): %v", req.ProjectID, failure, retried, err)
		failed := startingSpec(kept)
		failed.DraftRetried = retried
		finish(interfaces.REQUIREMENT_STATUS_FAILED, failure, failed, nil)
		return
	}

	byID := documentsByID(docs)
	spec, types := buildSpec(out, byID, draft, kept)
	for _, q := range spec.Questions {
		if q.DraftTime == 0 {
			q.DraftTime = req.CreateTime
		}
	}
	signQuestions(spec.Questions, draft)
	spec.TablesOmitted, spec.PropertiesOmitted, spec.DraftRetried = omitted, propertiesOmitted, retried
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
	for _, q := range req.Spec.Questions {
		q.Notes = questionNotes(q)
	}
	// The ontology the scenarios rest on goes on changing; a draft that cannot be read leaves
	// nothing marked.
	draft, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil || !found {
		draft = nil
	}
	view.OntologyUncovered = markOntologyChanges(&req.Spec, draft)
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
