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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI on an object type left unconnected: the same Agent reads it, the other adopted object
// types and the candidates the data suggests, and says what each candidate means with a clearer
// name, and which relations the business meaning suggests that no candidate covers. A relation it
// proposes is defined as the save would, on a copy, and counted as a saved one is; one the save
// would refuse is left out. A result the Agent gives without writing an answer is still given,
// with no answer. Nothing is written.

const (
	askKindRelationSuggestions = "relation_suggestions"
	// askMaxProposals bounds the relations Ask AI proposes beyond the candidates.
	askMaxProposals = 3
	// askMaxCandidates bounds the candidates the message carries, the likelier first.
	askMaxCandidates = 20
)

type relationSuggestionsInput struct {
	Language   string             `json:"language"`
	Kind       string             `json:"kind"`
	ObjectType askObjectType      `json:"object_type"`
	Others     []askObjectType    `json:"object_types"`
	Candidates []askCandidateLine `json:"candidates"`
}

// askCandidateLine is a candidate as the Agent reads it, by its index in the reply.
type askCandidateLine struct {
	Index   int                            `json:"index"`
	Source  string                         `json:"source"`
	Target  string                         `json:"target"`
	Mapping []interfaces.DraftFieldMapping `json:"mapping"`
	Name    string                         `json:"name"`
	Origin  string                         `json:"origin"`
	Counted bool                           `json:"counted"`
	HitRate *float64                       `json:"hit_rate,omitempty"`
}

type relationSuggestionsOutput struct {
	Candidates []struct {
		Index  int    `json:"index"`
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"candidates"`
	Proposals []struct {
		Source  string                         `json:"source_object_type_id"`
		Target  string                         `json:"target_object_type_id"`
		Mapping []interfaces.DraftFieldMapping `json:"mapping"`
		Name    string                         `json:"name"`
		Reason  string                         `json:"reason"`
	} `json:"proposals"`
}

func relationSuggestionsFormat() map[string]any {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required,
			"additionalProperties": false}
	}
	return object(map[string]any{
		"candidates": map[string]any{"type": "array", "maxItems": askMaxCandidates, "items": object(map[string]any{
			"index": map[string]any{"type": "integer"}, "name": str(interfaces.DRAFT_NAME_MAX_LENGTH),
			"reason": str(askReasonMaxLength),
		}, "index", "reason")},
		"proposals": map[string]any{"type": "array", "maxItems": askMaxProposals, "items": object(map[string]any{
			"source_object_type_id": map[string]any{"type": "string"},
			"target_object_type_id": map[string]any{"type": "string"},
			"mapping": map[string]any{"type": "array", "items": object(map[string]any{
				"source_property": map[string]any{"type": "string"}, "target_property": map[string]any{"type": "string"},
			}, "source_property", "target_property")},
			"name": str(interfaces.DRAFT_NAME_MAX_LENGTH), "reason": str(askReasonMaxLength),
		}, "source_object_type_id", "target_object_type_id", "mapping", "name", "reason")},
	}, "candidates", "proposals")
}

func (s *draftService) AskRelationSuggestions(ctx context.Context, token string, projectID string, objectTypeID string,
	caller interfaces.AccountInfo) (*interfaces.DraftRelationSuggestions, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskRelationSuggestions")
	defer span.End()

	draft, ot, err := s.suggestingFor(ctx, token, projectID, objectTypeID)
	if err != nil {
		span.SetStatus(codes.Error, "No object type to suggest for")
		return nil, err
	}
	suggestions, err := s.suggestionsOf(ctx, draft, ot, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read tables failed")
		return nil, err
	}
	in := relationSuggestionsInput{Language: draftLanguage(draft), Kind: askKindRelationSuggestions,
		ObjectType: askObjectTypeOf(ot), Others: []askObjectType{}, Candidates: []askCandidateLine{}}
	for _, other := range draft.Items.ObjectTypes {
		if other.Adopted && other.ID != ot.ID {
			in.Others = append(in.Others, askObjectTypeOf(other))
		}
	}
	for i, c := range suggestions.Candidates[:min(len(suggestions.Candidates), askMaxCandidates)] {
		line := askCandidateLine{Index: i, Source: c.SourceObjectTypeID, Target: c.TargetObjectTypeID, Name: c.Name,
			Origin: c.Origin, Counted: c.Evidence.Computed}
		for _, m := range c.MappingRules {
			line.Mapping = append(line.Mapping, interfaces.DraftFieldMapping{SourceProperty: m.SourceProperty.Name,
				TargetProperty: m.TargetProperty.Name})
		}
		if c.Evidence.Computed {
			rate := c.Evidence.HitRate
			line.HitRate = &rate
		}
		in.Candidates = append(in.Candidates, line)
	}
	message, err := json.Marshal(in)
	if err != nil {
		return nil, internalError(ctx, "Encode question", err)
	}
	text, raw, code := s.askAgent(ctx, caller, string(message), relationSuggestionsFormat(), nil, true)
	var out relationSuggestionsOutput
	if code == "" && json.Unmarshal(raw, &out) != nil {
		code = perrors.BknProject_Draft_AskUnavailable
	}
	if code != "" {
		span.SetStatus(codes.Error, "Ask AI failed")
		status := http.StatusBadGateway
		if code == perrors.BknProject_Draft_AskTimeout {
			status = http.StatusGatewayTimeout
		}
		return nil, httperrors.NewHTTPError(ctx, status, code)
	}
	suggestions.Answer = truncateRunes(text, askAnswerMaxLength)
	for _, c := range out.Candidates {
		if c.Index < 0 || c.Index >= len(in.Candidates) {
			continue
		}
		candidate := &suggestions.Candidates[c.Index]
		if name := strings.TrimSpace(c.Name); name != "" && utf8.RuneCountInString(name) <= interfaces.DRAFT_NAME_MAX_LENGTH {
			candidate.Name = name
		}
		candidate.Reason = truncateRunes(strings.TrimSpace(c.Reason), askReasonMaxLength)
	}
	for _, p := range out.Proposals[:min(len(out.Proposals), askMaxProposals)] {
		proposal, ok := s.proposedRelation(ctx, &draft.Items, ot, p.Source, p.Target, p.Mapping, caller)
		if !ok || slices.ContainsFunc(suggestions.Candidates, func(c interfaces.DraftRelationCandidate) bool {
			return sameCandidate(c, proposal)
		}) {
			continue
		}
		proposal.Name = strings.TrimSpace(p.Name)
		if proposal.Name == "" || utf8.RuneCountInString(proposal.Name) > interfaces.DRAFT_NAME_MAX_LENGTH {
			continue
		}
		proposal.Reason = truncateRunes(strings.TrimSpace(p.Reason), askReasonMaxLength)
		suggestions.Candidates = append(suggestions.Candidates, proposal)
	}
	span.SetStatus(codes.Ok, "")
	return suggestions, nil
}

// proposedRelation is a relation Ask AI proposes for the object type, defined as the save would
// on a copy and counted as a saved one is; false when the save would refuse it or it leaves the
// object type out.
func (s *draftService) proposedRelation(ctx context.Context, items *interfaces.DraftItems, ot *interfaces.DraftObjectType,
	source string, target string, mapping []interfaces.DraftFieldMapping,
	caller interfaces.AccountInfo) (interfaces.DraftRelationCandidate, bool) {
	ends := map[string]bool{}
	for _, id := range []string{source, target} {
		end := findObjectType(items, id)
		ends[id] = end != nil && end.Adopted
	}
	if (source != ot.ID && target != ot.ID) || !ends[source] || !ends[target] {
		return interfaces.DraftRelationCandidate{}, false
	}
	rules := []interfaces.DraftRelationMapping{}
	for _, m := range mapping {
		rules = append(rules, interfaces.DraftRelationMapping{SourceProperty: interfaces.DraftMappedField{Name: m.SourceProperty},
			TargetProperty: interfaces.DraftMappedField{Name: m.TargetProperty}})
	}
	body, err := json.Marshal(rules)
	if err != nil {
		return interfaces.DraftRelationCandidate{}, false
	}
	defined := &interfaces.DraftRelationType{}
	if s.defineRelation(ctx, items, defined, interfaces.DraftRelationTypeSave{SourceObjectTypeID: source,
		TargetObjectTypeID: target, Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: body}, caller) != nil {
		return interfaces.DraftRelationCandidate{}, false
	}
	return relationCandidateOf(defined, interfaces.DRAFT_RELATION_CANDIDATE_AI, "", "", s.countJoin(ctx, items, defined, caller)), true
}

// sameCandidate tells whether two candidates join the same ends by the same properties.
func sameCandidate(a interfaces.DraftRelationCandidate, b interfaces.DraftRelationCandidate) bool {
	return a.SourceObjectTypeID == b.SourceObjectTypeID && a.TargetObjectTypeID == b.TargetObjectTypeID &&
		slices.Equal(a.MappingRules, b.MappingRules)
}
