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

// maxSpecScenarios, maxSpecConcepts, maxSpecQuestions and maxRunLogicNeeds bound the new items a
// run keeps of one answer (the concepts run asks once per part of the documents), the first ones
// the Agent gives: its answer is one JSON document, and the longer it is,
// the likelier a model writes it wrong, runs out of time or out of output. The prompt states the
// same bounds; kept items do not count. maxDraftConcepts bounds the concepts scenario drafting adds
// to those extracted.
const (
	maxSpecScenarios = 10
	maxSpecConcepts  = 60
	maxDraftConcepts = 20
	maxSpecQuestions = 30
	maxRunLogicNeeds = 30
)

// agentDeadline is how long drafting waits for the Agent: its own time limit, with a minute for
// bkn-agent to record the outcome. pollInterval is how often it asks. draftDeadline is how long a
// drafting may run: it asks the Agent at most twice.
const (
	agentDeadline = 6 * time.Minute
	pollInterval  = 2 * time.Second
	draftDeadline = 2 * agentDeadline
)

// structuredOutputFailed is how bkn-agent names a failure to give its answer as the schema asks;
// structuredOutputTruncated, in that failure, an answer cut off at the model's output cap.
const (
	structuredOutputFailed    = "StructuredOutputError"
	structuredOutputTruncated = "output truncated"
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
	// failureOutputTooLong is an answer longer than the model writes at once.
	failureOutputTooLong = "output_too_long"
	// failurePartial is the error of a run that wrote what some parts of its documents gave, the
	// others failing: they are its pending parts.
	failurePartial = "partial"
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
	// rejectedUnknownLogicNeed is a kinetic need a question rests on that the specification does not
	// have.
	rejectedUnknownLogicNeed = "unknown_logic_need"
	// rejectedLogicNeed is a kinetic need; rejectedUnknownKind one of a kind there is none of.
	rejectedLogicNeed   = "logic_need"
	rejectedUnknownKind = "unknown_kind"
)

// questionTypeFilter is the type of a question that filters records on their properties.
const questionTypeFilter = "filter"

// draftOutput is what the Agent returns in pass draft. Its ids are its own, for items to refer to
// each other within the output; the server gives every item its own id. Concepts are the ones it
// adds to those extracted, which it names by their ids, as it names kinetic needs; concept_maps map
// extracted concepts to adopted items.
type draftOutput struct {
	Scenarios   []agentScenario   `json:"scenarios"`
	Concepts    []agentConcept    `json:"concepts"`
	ConceptMaps []agentConceptMap `json:"concept_maps"`
	Questions   []agentQuestion   `json:"questions"`
}

type agentConceptMap struct {
	ConceptID string                         `json:"concept_id"`
	MapsTo    *interfaces.RequirementItemRef `json:"maps_to"`
}

type agentCitation struct {
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
}

type agentScenario struct {
	ID               string                      `json:"id"`
	Name             string                      `json:"name"`
	Actor            string                      `json:"actor"`
	Trigger          string                      `json:"trigger"`
	Decision         string                      `json:"decision"`
	Knowledge        []string                    `json:"knowledge"`
	Loop             *interfaces.RequirementLoop `json:"loop"`
	Citations        []agentCitation             `json:"citations"`
	SupplementReason string                      `json:"supplement_reason"`
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
	LogicNeedIDs     []string        `json:"logic_need_ids"`
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

// citationsSchema is the schema of an item's citations.
func citationsSchema() map[string]any {
	return schemaArray(schemaObject(map[string]any{
		"document_id": schemaString(), "quote": schemaString(),
	}, "document_id", "quote"))
}

// itemRefSchema is the schema of an adopted item a concept maps to.
func itemRefSchema() map[string]any {
	return schemaObject(map[string]any{
		"kind": schemaEnum(interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, interfaces.REQUIREMENT_ITEM_RELATION_TYPE),
		"id":   schemaString(),
	}, "kind", "id")
}

// conceptSchema is the schema of a concept; categories are those the pass gives.
func conceptSchema(categories ...string) map[string]any {
	return schemaObject(map[string]any{
		"id": schemaString(), "term": schemaString(), "category": schemaEnum(categories...),
		"definition": schemaString(), "not_ontology_reason": schemaString(), "maps_to": itemRefSchema(),
		"citations": citationsSchema(), "supplement_reason": schemaString(),
	}, "id", "term", "category", "citations")
}

// draftResponseFormat is the JSON Schema of pass draft.
func draftResponseFormat() map[string]any {
	citations := citationsSchema()
	return schemaObject(map[string]any{
		"scenarios": schemaArray(schemaObject(map[string]any{
			"id": schemaString(), "name": schemaString(), "actor": schemaString(), "trigger": schemaString(),
			"decision": schemaString(), "knowledge": schemaStrings(),
			"loop": schemaObject(map[string]any{
				"discover": schemaString(), "judge": schemaString(), "suggest": schemaString(),
				"confirm": schemaString(), "execute": schemaString(), "state_change": schemaString(),
				"track": schemaString(),
			}),
			"citations": citations, "supplement_reason": schemaString(),
		}, "id", "name", "citations")),
		"concepts": schemaArray(conceptSchema("object", "event", "property", "relation", "evidence", "not_ontology")),
		"concept_maps": schemaArray(schemaObject(map[string]any{
			"concept_id": schemaString(), "maps_to": itemRefSchema(),
		}, "concept_id", "maps_to")),
		"questions": schemaArray(schemaObject(map[string]any{
			"id": schemaString(), "text": schemaString(), "scenario_ids": schemaStrings(),
			"type": schemaEnum("point_in_time", "provenance", questionTypeFilter, "compare", "aggregate", "count",
				"disambiguate", "judge", "metric"),
			"layer":    schemaEnum(interfaces.REQUIREMENT_LAYER_KNOWLEDGE, interfaces.REQUIREMENT_LAYER_CAPABILITY),
			"template": schemaString(), "answer_shape": schemaString(), "concept_ids": schemaStrings(),
			"logic_need_ids": schemaStrings(),
			"inputs": schemaArray(schemaObject(map[string]any{
				"name": schemaString(), "concept_id": schemaString(), "property": schemaString(),
				"role": schemaEnum(interfaces.REQUIREMENT_INPUT_RECORD, interfaces.REQUIREMENT_INPUT_REFERENCE_TIME),
				"example": map[string]any{"type": "string", "description": "A value of this input the documents " +
					"state, such as a record they give as an example, copied as written; empty when they state none."},
			}, "name", "concept_id", "property", "role", "example")),
			"relative_window": map[string]any{"type": "boolean"},
			"citations":       citations, "supplement_reason": schemaString(),
		}, "id", "text", "type", "layer", "inputs", "relative_window", "citations")),
	}, "scenarios", "concepts", "concept_maps", "questions")
}

// draftMessage is the user message of pass draft: the documents block by block, the tables of
// the project's catalogs by name, the adopted items with their ids (an object type with its
// properties and primary keys), the concepts, rules and kinetic needs of the specification, and the
// scenarios and questions a person kept, as data.
type draftMessage struct {
	Documents     []messageDocument     `json:"documents"`
	Tables        []messageTable        `json:"tables"`
	ObjectTypes   []messageObjectType   `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
	Concepts      []messageConcept      `json:"concepts"`
	Rules         []messageRule         `json:"rules"`
	LogicNeeds    []messageLogicNeed    `json:"logic_needs"`
	Kept          *messageKept          `json:"kept,omitempty"`
	// Supplement is the owner's supplement note, absent when none was written.
	Supplement string `json:"supplement,omitempty"`
}

// messageConcept is a concept of the specification, by its own id.
type messageConcept struct {
	ID         string                         `json:"id"`
	Term       string                         `json:"term"`
	Category   string                         `json:"category"`
	Definition string                         `json:"definition,omitempty"`
	MapsTo     *interfaces.RequirementItemRef `json:"maps_to,omitempty"`
}

type messageRule struct {
	Statement  string `json:"statement"`
	CompilesTo string `json:"compiles_to"`
}

// messageLogicNeed is a kinetic need of the specification, by its own id.
type messageLogicNeed struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Definition    string   `json:"definition,omitempty"`
	ObjectTypeIDs []string `json:"object_type_ids,omitempty"`
	// OwnerObjectType names, for an item of the network, the object type it belongs to.
	OwnerObjectType string `json:"owner_object_type,omitempty"`
}

func messageConceptsOf(concepts []*interfaces.RequirementConcept) []messageConcept {
	out := []messageConcept{}
	for _, c := range concepts {
		out = append(out, messageConcept{ID: c.ID, Term: c.Term, Category: c.Category, Definition: c.Definition, MapsTo: c.MapsTo})
	}
	return out
}

func messageRulesOf(rules []*interfaces.RequirementRule) []messageRule {
	out := []messageRule{}
	for _, r := range rules {
		out = append(out, messageRule{Statement: r.Statement, CompilesTo: r.CompilesTo})
	}
	return out
}

func messageLogicNeedsOf(needs []*interfaces.RequirementLogicNeed) []messageLogicNeed {
	out := []messageLogicNeed{}
	for _, n := range needs {
		out = append(out, messageLogicNeed{ID: n.ID, Kind: n.Kind, Name: n.Name, Definition: n.Definition,
			ObjectTypeIDs: n.ObjectTypeIDs})
	}
	return out
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
	if q.Layer == interfaces.REQUIREMENT_LAYER_CAPABILITY && len(q.LogicNeedIDs) == 0 {
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

// messageDocumentsOf is the documents block by block.
func messageDocumentsOf(docs []*interfaces.RequirementDocument) []messageDocument {
	out := []messageDocument{}
	for _, d := range docs {
		content := []rune(d.Content)
		md := messageDocument{ID: d.ID, Name: d.Name, Type: d.DocType, Blocks: []messageBlock{}}
		for _, b := range d.Blocks {
			if b.Start < 0 || b.End > len(content) || b.Start >= b.End {
				continue
			}
			md.Blocks = append(md.Blocks, messageBlock{ID: b.ID, Heading: b.Heading, Text: string(content[b.Start:b.End])})
		}
		out = append(out, md)
	}
	return out
}

// messageTablesOf names at most maxMessageTables tables, and counts those it left out.
func messageTablesOf(tables []*interfaces.VegaTable) ([]messageTable, int) {
	out := []messageTable{}
	for _, t := range tables[:min(len(tables), maxMessageTables)] {
		out = append(out, messageTable{Name: t.Name, Description: t.Description})
	}
	return out, len(tables) - len(out)
}

// messageOntologyOf names the adopted items only: candidates no one adopted are not the ontology
// the specification rests on. An object type comes with its properties, its keys and times first,
// unless withProperties is false; it also counts the properties it left out.
func messageOntologyOf(draft *interfaces.Draft, withProperties bool) ([]messageObjectType, []messageRelationType, int) {
	objectTypes, relationTypes, propertiesOmitted := []messageObjectType{}, []messageRelationType{}, 0
	if draft == nil {
		return objectTypes, relationTypes, 0
	}
	// Evidence types and the technical columns of document objects are not published for questions
	// to be asked about; the Agent is not shown them.
	evidence := map[string]bool{}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted && ot.Document != nil && strings.HasPrefix(ot.Document.Role, "evidence_") {
			evidence[ot.ID] = true
		}
	}
	for _, ot := range draft.Items.ObjectTypes {
		if !ot.Adopted || evidence[ot.ID] {
			continue
		}
		keys := ot.PrimaryKeys
		if ot.Document != nil {
			keys = slices.DeleteFunc(slices.Clone(keys), func(key string) bool { return key == "_id" })
		}
		mot := messageObjectType{ID: ot.ID, Name: ot.Name, Description: ot.Description, Aliases: ot.Aliases,
			PrimaryKeys: keys, Properties: []messageProperty{}}
		if withProperties {
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
			props := slices.DeleteFunc(slices.Clone(ot.Properties), func(p *interfaces.DraftProperty) bool {
				return ot.Document != nil && (p.Name == "_id" || interfaces.DocumentHiddenColumn(ot.Document.Role, p.Name))
			})
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
		}
		objectTypes = append(objectTypes, mot)
	}
	for _, rt := range draft.Items.RelationTypes {
		if !rt.Adopted || evidence[rt.Source] || evidence[rt.Target] {
			continue
		}
		relationTypes = append(relationTypes, messageRelationType{ID: rt.ID, Name: rt.Name,
			Description: rt.Description, Source: rt.Source, Target: rt.Target})
	}
	return objectTypes, relationTypes, propertiesOmitted
}

// buildDraftMessage is the message of pass draft over base, the specification drafting adds to,
// with the kinetic items the network already has that no need of base stands for among its kinetic
// needs. It also returns how many tables, and how many properties of object types, it left out.
func buildDraftMessage(language string, docs []*interfaces.RequirementDocument, tables []*interfaces.VegaTable,
	draft *interfaces.Draft, base *interfaces.RequirementSpec, kept *interfaces.RequirementSpec,
	networkItems []*interfaces.LogicNetworkItem, supplement string) (string, int, int, error) {
	msg := draftMessage{Documents: messageDocumentsOf(docs), Concepts: messageConceptsOf(base.Concepts),
		Rules: messageRulesOf(base.Rules), LogicNeeds: messageLogicNeedsOf(base.LogicNeeds), Kept: messageKeptOf(kept),
		Supplement: supplement}
	var tablesOmitted, propertiesOmitted int
	msg.Tables, tablesOmitted = messageTablesOf(tables)
	msg.ObjectTypes, msg.RelationTypes, propertiesOmitted = messageOntologyOf(draft, true)
	fromNetwork, _ := messageNetworkItemsOf(networkItems, modelNames(msg.ObjectTypes))
	msg.LogicNeeds = append(msg.LogicNeeds, fromNetwork...)
	body, err := json.Marshal(msg)
	if err != nil {
		return "", 0, 0, err
	}
	return "pass draft\nlanguage: " + language + "\n" + string(body), tablesOmitted, propertiesOmitted, nil
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

// adoptedRefs are the adopted items of the draft, the ones the Agent is given.
func adoptedRefs(draft *interfaces.Draft) map[interfaces.RequirementItemRef]bool {
	adopted := map[interfaces.RequirementItemRef]bool{}
	if draft == nil {
		return adopted
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.Adopted {
			adopted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = true
		}
	}
	for _, rt := range draft.Items.RelationTypes {
		if rt.Adopted {
			adopted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = true
		}
	}
	return adopted
}

// buildSpec checks what the Agent proposed in pass draft and adds what passes to spec, the base
// drafting adds to, with the server's ids; everything dropped is listed in rejected.
func buildSpec(out *draftOutput, docs map[string]*interfaces.RequirementDocument, draft *interfaces.Draft,
	spec *interfaces.RequirementSpec) {
	// What is kept stays as it is; drafted items repeating it are merged into it, and new ones take
	// ids no item had.
	keptSpec := *spec
	reject := func(kind, text, reason string) {
		spec.Rejected = append(spec.Rejected, &interfaces.RequirementRejected{Kind: kind, Text: text, Reason: reason})
	}
	adopted := adoptedRefs(draft)

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
		spec.Scenarios = append(spec.Scenarios, &interfaces.RequirementScenario{
			ID: id, Name: s.Name, Actor: s.Actor, Trigger: s.Trigger, Decision: s.Decision,
			Knowledge: orEmpty(s.Knowledge), Loop: s.Loop, Citations: citations, SupplementReason: s.SupplementReason,
		})
	}

	// The concepts extracted are named by their ids, as kept ones were before.
	concepts := map[string]*interfaces.RequirementConcept{}
	for _, c := range spec.Concepts {
		concepts[c.ID], concepts[keptPrefix+c.ID] = c, c
	}
	// The Agent maps a concept no one mapped to an adopted item; it may name only one it was given.
	for _, m := range out.ConceptMaps {
		if c := concepts[m.ConceptID]; c != nil && c.MapsTo == nil && !c.Edited && m.MapsTo != nil && adopted[*m.MapsTo] {
			ref := *m.MapsTo
			c.MapsTo = &ref
		}
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
		if reason == "" && added == maxDraftConcepts {
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
			Citations: citations, SupplementReason: c.SupplementReason, Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS,
		}
		// The Agent judges which drafted item a concept is; it may name only one it was given.
		if c.MapsTo != nil && adopted[*c.MapsTo] {
			ref := *c.MapsTo
			concept.MapsTo = &ref
		}
		if c.ID != "" {
			concepts[c.ID] = concept
		}
		spec.Concepts = append(spec.Concepts, concept)
	}

	needs := map[string]*interfaces.RequirementLogicNeed{}
	for _, n := range spec.LogicNeeds {
		needs[n.ID], needs[keptPrefix+n.ID] = n, n
	}
	added = 0
	for _, q := range out.Questions {
		if slices.ContainsFunc(keptSpec.Questions, func(k *interfaces.RequirementQuestion) bool { return sameText(k.Text, q.Text) }) {
			continue
		}
		citations, reason := basisOf(q.Citations, q.SupplementReason, docs, true)
		if reason == "" && len(citations) > 0 && !numbersInQuotes(q.Text, citations) && !recordValuesInText(q, citations, docs) {
			reason = rejectedNumberNotInQuote
		}
		if reason == "" && added == maxSpecQuestions {
			reason = rejectedTooMany
		}
		if reason != "" {
			reject(rejectedQuestion, q.Text, reason)
			continue
		}
		// A question that rests on a kinetic need needs more than the ontology and its facts, whatever
		// layer the Agent gave it: it is not asked in the knowledge regression. Only needs the
		// specification has stand.
		logicNeedIDs, dropped := []string{}, false
		for _, id := range q.LogicNeedIDs {
			n := needs[strings.TrimSpace(id)]
			if n == nil {
				if strings.TrimSpace(id) != "" {
					reject(rejectedBasis, id+" ("+q.Text+")", rejectedUnknownLogicNeed)
					dropped = true
				}
				continue
			}
			if !slices.Contains(logicNeedIDs, n.ID) {
				logicNeedIDs = append(logicNeedIDs, n.ID)
			}
		}
		// A filter on properties needs no function: a question typed a filter that rests on functions
		// alone filters all the same, as its type says.
		if q.Type == questionTypeFilter && len(logicNeedIDs) > 0 && !slices.ContainsFunc(logicNeedIDs, func(id string) bool {
			return needs[id].Kind != interfaces.LOGIC_NEED_FUNCTION
		}) {
			for _, id := range logicNeedIDs {
				reject(rejectedBasis, needs[id].Name+" ("+q.Text+")", rejectedFunctionOnFilter)
			}
			logicNeedIDs, dropped = []string{}, true
		}
		// Said capability for nothing that stands, it is a lookup: knowledge.
		layer := interfaces.REQUIREMENT_LAYER_CAPABILITY
		if (q.Layer == interfaces.REQUIREMENT_LAYER_KNOWLEDGE || dropped) && len(logicNeedIDs) == 0 {
			layer = interfaces.REQUIREMENT_LAYER_KNOWLEDGE
		}
		question := &interfaces.RequirementQuestion{
			ID: nextID(spec, "q", spec.Questions, questionID), Text: q.Text, ScenarioIDs: []string{},
			Type: q.Type, Layer: layer, Template: q.Template, AnswerShape: q.AnswerShape,
			Basis: interfaces.RequirementBasis{ConceptIDs: []string{}}, LogicNeedIDs: logicNeedIDs,
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
		added++
		spec.Questions = append(spec.Questions, question)
	}
	needScenarios(spec)
}

// needScenarios sets the scenarios each kinetic need serves: those of the questions resting on it.
func needScenarios(spec *interfaces.RequirementSpec) {
	for _, n := range spec.LogicNeeds {
		n.ScenarioIDs = []string{}
		for _, q := range spec.Questions {
			if !slices.Contains(q.LogicNeedIDs, n.ID) {
				continue
			}
			for _, sid := range q.ScenarioIDs {
				if !slices.Contains(n.ScenarioIDs, sid) {
					n.ScenarioIDs = append(n.ScenarioIDs, sid)
				}
			}
		}
	}
}

// mapConcepts maps each concept no one mapped, of an object, event or relation, to the adopted item
// of the same name or alias, when exactly one has it: concepts are mapped before scenarios are
// drafted, so questions rest on items. It follows the questions' needs.
func mapConcepts(spec *interfaces.RequirementSpec, draft *interfaces.Draft) {
	if draft == nil {
		return
	}
	for _, c := range spec.Concepts {
		if c.MapsTo != nil || c.Edited {
			continue
		}
		var matches []interfaces.RequirementItemRef
		switch c.Category {
		case "object", "event":
			for _, ot := range draft.Items.ObjectTypes {
				if ot.Adopted && (sameText(ot.Name, c.Term) || slices.ContainsFunc(ot.Aliases, func(a string) bool { return sameText(a, c.Term) })) {
					matches = append(matches, interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID})
				}
			}
		case "relation":
			for _, rt := range draft.Items.RelationTypes {
				if rt.Adopted && sameText(rt.Name, c.Term) {
					matches = append(matches, interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID})
				}
			}
		}
		if len(matches) == 1 {
			c.MapsTo = &matches[0]
		}
	}
	followConcepts(spec)
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
	// Tables are read from the catalogs the project reads; the inputs record those it keeps.
	catalogIDs := s.catalogsOf(ctx, token, entry, caller)
	if len(catalogIDs) == 0 {
		span.SetStatus(codes.Error, "No catalogs")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_NoCatalogs)
	}
	if err := checkInputsSize(ctx, docs); err != nil {
		span.SetStatus(codes.Error, "Inputs too large")
		return nil, err
	}
	// Scenarios are drafted from the confirmed model, its concepts and kinetic needs.
	draft, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !found || !modelConfirmed(draft) {
		span.SetStatus(codes.Error, "Model not confirmed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_ModelNotConfirmed)
	}
	previous, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	// Concepts extracted from the documents are not needed: without them pass draft drafts from the
	// confirmed model, the documents and the kinetic needs, adding the concepts its questions need,
	// so a concepts run that failed never blocks the first draft of the scenarios.
	// Drafting again keeps what people made theirs, unless they start over, and what the other runs
	// wrote; concepts are mapped to the model before drafting reads them.
	var prev *interfaces.Requirement
	version, readUpdateTime := 1, int64(0)
	if ok {
		prev, version, readUpdateTime = previous, nextVersion(previous.Version, draft), previous.UpdateTime
	}
	base := draftBase(prev, discardDecisions)
	mapConcepts(base, draft)

	now := s.now()
	req := &interfaces.Requirement{
		ProjectID: projectID, Version: version, Status: interfaces.REQUIREMENT_STATUS_DRAFTING,
		Spec: *base, RunID: s.newID(), Drafter: caller, CreateTime: now, UpdateTime: now,
	}
	// The row holds the base while drafting runs, so a drafting cut short keeps it too, and is
	// written only over the specification it was read from.
	started, err := s.ra.StartDrafting(ctx, req, s.staleRunBefore(), readUpdateTime)
	switch {
	case errors.Is(err, interfaces.ErrRequirementChanged):
		span.SetStatus(codes.Error, "Changed meanwhile")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	case err != nil:
		span.SetStatus(codes.Error, "Start drafting failed")
		return nil, internalError(ctx, "Start drafting failed", err)
	case !started:
		span.SetStatus(codes.Error, "Drafting")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	}
	project := entry.Project
	// Scenarios may cite the network's background too; it is read now, with the caller's token.
	cited := s.withBackground(ctx, token, entry, docs)
	// Scenarios rest on what the network already does too: its kinetic items are read now, as the caller.
	networkItems := s.scenarioNetworkItems(ctx, token, entry.KNID, base)
	supplement := s.supplementOf(ctx, projectID)
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.draft(runCtx, req, project, catalogIDs, docs, cited, networkItems, supplement, caller) })
	span.SetStatus(codes.Ok, "")
	return req, nil
}

// checkInputsSize refuses documents longer together than one run reads: they go to the Agent in
// one message.
func checkInputsSize(ctx context.Context, docs []*interfaces.RequirementDocument) error {
	total := 0
	for _, d := range docs {
		total += utf8.RuneCountInString(d.Content)
	}
	if total > maxInputRunes {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InputsTooLarge)
	}
	return nil
}

// catalogsOf are the catalogs the project reads, as the caller sees them: on an existing network
// those its object types read too. The entry keeps the catalogs stored.
func (s *requirementService) catalogsOf(ctx context.Context, token string, entry *interfaces.ProjectEntry,
	caller interfaces.AccountInfo) []string {
	project := *entry.Project
	read := &interfaces.ProjectEntry{Project: &project}
	s.pc.Resolve(ctx, token, caller, read)
	return read.CatalogIDs
}

// listTables lists the tables of the catalogs as the caller; they are clues, and a run goes on
// without those it cannot read, which it returns.
func (s *requirementService) listTables(ctx context.Context, catalogIDs []string,
	caller interfaces.AccountInfo) ([]*interfaces.VegaTable, []string) {
	tables, unlisted := []*interfaces.VegaTable{}, []string{}
	for _, catalogID := range catalogIDs {
		listed, err := s.va.ListTables(ctx, caller, catalogID)
		if err != nil {
			logger.Warnf("List tables of catalog %s for a requirement run failed: %v", catalogID, err)
			unlisted = append(unlisted, catalogID)
			continue
		}
		tables = append(tables, listed...)
	}
	return tables, unlisted
}

// draft runs the Agent and records the checked specification, or why drafting failed.
// What drafting started from stays when it fails. The Agent reads and cites cited, the documents
// and the network's background; the run records the documents as its inputs.
func (s *requirementService) draft(ctx context.Context, req *interfaces.Requirement, project *interfaces.Project,
	catalogIDs []string, docs []*interfaces.RequirementDocument, cited []*interfaces.RequirementDocument,
	networkItems []*interfaces.LogicNetworkItem, supplement string, caller interfaces.AccountInfo) {
	finish := func(status, failure string, spec *interfaces.RequirementSpec, inputs *interfaces.RequirementInputs) {
		done := *req
		done.Status, done.Error, done.Spec, done.Inputs, done.UpdateTime = status, failure, *spec, inputs, s.now()
		if _, err := s.ra.FinishDrafting(ctx, &done); err != nil {
			logger.Errorf("Record requirement drafting of project %s failed: %v", req.ProjectID, err)
		}
	}
	base := req.Spec
	fail := func(failure string, err error) {
		logger.Errorf("Requirement drafting of project %s failed (%s): %v", req.ProjectID, failure, err)
		finish(interfaces.REQUIREMENT_STATUS_FAILED, failure, &base, nil)
	}

	tables, unlisted := s.listTables(ctx, catalogIDs, caller)
	// Scenarios rest on the adopted ontology: drafting does not run without it, nor leaves its
	// questions unsigned.
	draft, ok, err := s.da.GetDraft(ctx, req.ProjectID)
	if err != nil || !ok {
		fail(failureInterrupted, fmt.Errorf("read draft: %v", err))
		return
	}
	message, omitted, propertiesOmitted, err := buildDraftMessage(project.NamingLanguage, cited, tables, draft, &base, &base,
		networkItems, supplement)
	if err != nil {
		fail(failureAgentUnavailable, err)
		return
	}
	answer, retried, failure, err := s.askTwice(ctx, req.ProjectID, caller, message, draftResponseFormat(),
		func() any { return &draftOutput{} }, &req.AgentTaskID)
	if failure != "" {
		logger.Errorf("Requirement drafting of project %s failed (%s, asked again: %t): %v", req.ProjectID, failure, retried, err)
		failed := base
		failed.DraftRetried = retried
		finish(interfaces.REQUIREMENT_STATUS_FAILED, failure, &failed, nil)
		return
	}

	spec := cloneSpec(&base)
	out := answer.(*draftOutput)
	fromNetwork := needsOfNetworkItems(out, spec, networkItems, draft, caller, s.now())
	buildSpec(out, documentsByID(cited), draft, spec)
	leaveUnasked(spec, fromNetwork)
	for _, q := range spec.Questions {
		if q.DraftTime == 0 {
			q.DraftTime = req.CreateTime
			q.Text = plainText(q, spec, draft, project.NamingLanguage)
		}
	}
	signQuestions(spec.Questions, draft)
	spec.TablesOmitted, spec.PropertiesOmitted, spec.DraftRetried = omitted, propertiesOmitted, retried
	spec.CatalogsUnlisted = nil
	if len(unlisted) > 0 {
		spec.CatalogsUnlisted = unlisted
	}
	finish(interfaces.REQUIREMENT_STATUS_READY, "", spec, requirementInputsOf(docs, project))
}

// cloneSpec copies a specification's lists, so adding to the copy leaves the original as it was.
// The items are shared.
func cloneSpec(spec *interfaces.RequirementSpec) *interfaces.RequirementSpec {
	out := *spec
	out.Scenarios, out.Concepts = slices.Clone(spec.Scenarios), slices.Clone(spec.Concepts)
	out.Questions, out.Rules = slices.Clone(spec.Questions), slices.Clone(spec.Rules)
	out.Conflicts, out.Choices = slices.Clone(spec.Conflicts), slices.Clone(spec.Choices)
	out.LogicNeeds, out.Rejected = slices.Clone(spec.LogicNeeds), slices.Clone(spec.Rejected)
	out.LastIDs = map[string]int{}
	for prefix, n := range spec.LastIDs {
		out.LastIDs[prefix] = n
	}
	return withLists(&out)
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
		current := requirementInputsOf(docs, entry.Project)
		// Scenarios not drafted yet have nothing to be out of date.
		view.Stale = req.Inputs != nil && !reflect.DeepEqual(req.Inputs, current)
		for _, run := range req.Spec.Runs {
			run.Stale = run.Inputs != nil && !reflect.DeepEqual(run.Inputs.Documents, current.Documents)
		}
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
	runs, err := s.failInterruptedRuns(ctx)
	if err != nil {
		return err
	}
	if runs > 0 {
		logger.Infof("Failed %d requirement concepts and kinetic runs a previous run left going", runs)
	}
	if n, err = s.ra.FailGenerating(ctx, failureInterrupted, now); err != nil {
		return err
	}
	if n > 0 {
		logger.Infof("Failed %d question guidance generations a previous run left going", n)
	}
	return nil
}
