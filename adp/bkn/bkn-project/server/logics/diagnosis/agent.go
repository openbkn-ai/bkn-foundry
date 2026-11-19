// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	// agentID is the Ontology Assistant; its diagnose pass judges the facts.
	agentID   = "bkn-project-ontology-ask"
	agentKind = "diagnose"
	// agentMaxSuggestions bounds what Ask AI suggests, as the prompt does.
	agentMaxSuggestions = 15
	// agentMaxConcepts bounds the requirement concepts the message carries.
	agentMaxConcepts = 40
	// agentMaxReferences bounds the references the message carries, the counted first.
	agentMaxReferences = 120
)

// conceptInput is a requirement concept no data matches yet, with the passages it quotes.
type conceptInput struct {
	ID         string                      `json:"id"`
	Term       string                      `json:"term"`
	Category   string                      `json:"category"`
	Definition string                      `json:"definition"`
	Quotes     []interfaces.DiagnosisQuote `json:"quotes"`
}

// pendingConcepts are the requirement concepts the concepts page shows as matched to nothing.
func (s *diagnosisService) pendingConcepts(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) ([]conceptInput, error) {
	req, exist, err := s.rqa.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	if !exist || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		return []conceptInput{}, nil
	}
	concepts, err := s.drs.GetConcepts(ctx, token, projectID, caller)
	var httpErr *httperrors.HTTPError
	switch {
	case errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound:
		return []conceptInput{}, nil
	case err != nil:
		return nil, err
	}
	unmatched := map[string]bool{}
	for _, rc := range concepts.Requirements {
		if rc.State == interfaces.REQUIREMENT_MATCH_UNMATCHED {
			unmatched[rc.ID] = true
		}
	}
	out := []conceptInput{}
	for _, c := range req.Spec.Concepts {
		if !unmatched[c.ID] {
			continue
		}
		in := conceptInput{ID: c.ID, Term: c.Term, Category: c.Category, Definition: c.Definition,
			Quotes: []interfaces.DiagnosisQuote{}}
		for _, citation := range c.Citations {
			if q := strings.TrimSpace(citation.Quote); q != "" {
				in.Quotes = append(in.Quotes, interfaces.DiagnosisQuote{DocumentID: citation.DocumentID, Text: q})
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// agentObjectType is an object type as the Agent reads it.
type agentObjectType struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Aliases       []string        `json:"aliases,omitempty"`
	Description   string          `json:"description"`
	Table         string          `json:"table"`
	Adopted       bool            `json:"adopted"`
	NeedsIdentity bool            `json:"needs_identity"`
	Identity      []string        `json:"identity"`
	DisplayKey    string          `json:"display_key"`
	Properties    []agentProperty `json:"properties"`
}

type agentProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	MappedField string `json:"mapped_field"`
	Description string `json:"description,omitempty"`
}

type agentRelationType struct {
	ID      string                         `json:"id"`
	Name    string                         `json:"name"`
	Source  string                         `json:"source"`
	Target  string                         `json:"target"`
	Adopted bool                           `json:"adopted"`
	Mapping []interfaces.DraftFieldMapping `json:"mapping"`
	HitRate *float64                       `json:"hit_rate,omitempty"`
}

type agentReference struct {
	Index          int                                 `json:"index"`
	Source         string                              `json:"source"`
	SourceProperty string                              `json:"source_property"`
	Target         string                              `json:"target"`
	TargetProperty string                              `json:"target_property"`
	Condition      *interfaces.DraftReferenceCondition `json:"condition,omitempty"`
	Self           bool                                `json:"self,omitempty"`
	Counted        bool                                `json:"counted"`
	Rows           int64                               `json:"rows"`
	Nulls          int64                               `json:"nulls"`
	Hits           int64                               `json:"hits"`
	Rate           float64                             `json:"rate"`
	RelationTypeID string                              `json:"relation_type_id,omitempty"`
	Adopted        bool                                `json:"adopted,omitempty"`
}

type agentSubtype struct {
	Index int `json:"index"`
	interfaces.DraftSubtype
}

type agentQuestion struct {
	ID          string                           `json:"id"`
	Text        string                           `json:"text"`
	Unconnected []interfaces.ScenarioUnconnected `json:"unconnected"`
}

// agentInput is the message of the diagnose pass.
type agentInput struct {
	Language             string              `json:"language"`
	Kind                 string              `json:"kind"`
	Components           [][]string          `json:"components"`
	ObjectTypes          []agentObjectType   `json:"object_types"`
	RelationTypes        []agentRelationType `json:"relation_types"`
	References           []agentReference    `json:"references"`
	Subtypes             []agentSubtype      `json:"subtypes"`
	NotCounted           int                 `json:"not_counted"`
	UnreachableQuestions []agentQuestion     `json:"unreachable_questions"`
	Concepts             []conceptInput      `json:"concepts"`
}

// agentSuggestion is a suggestion as the Agent gives it; the server checks every field.
type agentSuggestion struct {
	Kind              string                         `json:"kind"`
	Title             string                         `json:"title"`
	Reason            string                         `json:"reason"`
	Questions         []string                       `json:"questions"`
	Reference         *int                           `json:"reference"`
	Subtype           *int                           `json:"subtype"`
	RelationTypeID    string                         `json:"relation_type_id"`
	ObjectTypeID      string                         `json:"object_type_id"`
	Name              string                         `json:"name"`
	Description       string                         `json:"description"`
	Cardinality       string                         `json:"cardinality"`
	ConceptID         string                         `json:"concept_id"`
	Disposition       string                         `json:"disposition"`
	OwnerObjectTypeID string                         `json:"owner_object_type_id"`
	Writeback         *interfaces.DiagnosisWriteback `json:"writeback"`
	Quotes            []interfaces.DiagnosisQuote    `json:"quotes"`
}

type agentOutput struct {
	Suggestions []agentSuggestion `json:"suggestions"`
}

// agentFormat is the JSON Schema of the diagnose pass's result.
func agentFormat() map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	strs := func() map[string]any { return map[string]any{"type": "array", "items": str()} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required,
			"additionalProperties": false}
	}
	kinds := []string{interfaces.DIAGNOSIS_KIND_CHOOSE_IDENTITY, interfaces.DIAGNOSIS_KIND_ADOPT_OBJECT,
		interfaces.DIAGNOSIS_KIND_ADOPT_RELATION, interfaces.DIAGNOSIS_KIND_CREATE_RELATION,
		interfaces.DIAGNOSIS_KIND_EXCLUDE_OBJECT, interfaces.DIAGNOSIS_KIND_WRITEBACK_OBJECT,
		interfaces.DIAGNOSIS_KIND_CONCEPT_DISPOSITION}
	writeback := object(map[string]any{
		"name": str(), "description": str(), "tracked_object_type_id": str(),
		"properties": map[string]any{"type": "array", "items": object(map[string]any{
			"name": str(), "display_name": str(), "type": str(),
			"role": map[string]any{"type": "string", "enum": writebackRoles},
		}, "name", "display_name", "type", "role")},
		"actions": map[string]any{"type": "array", "items": object(map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{interfaces.WRITEBACK_ACTION_ADD,
				interfaces.WRITEBACK_ACTION_MODIFY}},
			"name": str(), "parameters": strs(),
		}, "kind", "name", "parameters")},
	}, "name", "description", "tracked_object_type_id", "properties", "actions")
	return object(map[string]any{
		"suggestions": map[string]any{"type": "array", "maxItems": agentMaxSuggestions, "items": object(map[string]any{
			"kind":  map[string]any{"type": "string", "enum": kinds},
			"title": str(), "reason": str(), "questions": strs(),
			"reference": map[string]any{"type": "integer"}, "subtype": map[string]any{"type": "integer"},
			"relation_type_id": str(), "object_type_id": str(), "name": str(), "description": str(),
			"cardinality": str(), "concept_id": str(), "disposition": str(), "owner_object_type_id": str(),
			"writeback": writeback,
			"quotes": map[string]any{"type": "array", "items": object(map[string]any{
				"document_id": str(), "text": str()}, "document_id", "text")},
		}, "kind", "title", "reason")},
	}, "suggestions")
}

// askAgent asks the diagnose pass, as the caller; an error means no usable result.
func (s *diagnosisService) askAgent(ctx context.Context, caller interfaces.AccountInfo, in agentInput) (*agentOutput, error) {
	message, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.agentWait)
	defer cancel()
	var raw json.RawMessage
	failure := ""
	err = s.ba.Chat(ctx, caller, &interfaces.BknAgentChatRequest{AgentID: agentID, Message: string(message),
		ResponseFormat: agentFormat()}, func(event interfaces.BknAgentChatEvent) {
		var data struct {
			Content json.RawMessage `json:"content"`
			Code    string          `json:"code"`
		}
		_ = json.Unmarshal(event.Data, &data)
		switch event.Name {
		case interfaces.BKN_AGENT_CHAT_STRUCTURED:
			if strings.HasPrefix(strings.TrimSpace(string(data.Content)), "{") {
				raw = data.Content
			}
		case interfaces.BKN_AGENT_CHAT_ERROR:
			failure = data.Code
		}
	})
	switch {
	case err != nil:
		return nil, err
	case failure != "":
		return nil, fmt.Errorf("agent failed: %s", failure)
	case raw == nil:
		return nil, errors.New("agent gave no result")
	}
	var out agentOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("agent result: %w", err)
	}
	return &out, nil
}
