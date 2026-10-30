// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI: a question about one object type of the draft, answered by a bkn-agent task Agent from
// the object type's current state. It reads the draft and never writes it: a suggestion is
// adopted, if at all, by a person through the edit it names, which records the decision.

const (
	// askAgentID is the bkn-agent task Agent that answers questions about a draft item.
	askAgentID = "bkn-project-ontology-ask"
	// askDeadline bounds the wait within the request: gateways commonly cut requests at 60 seconds.
	askDeadline    = 45 * time.Second
	askPoll        = time.Second
	askReadTimeout = 10 * time.Second
	// The answer's bounds, in characters; the reason is short.
	askAnswerMaxLength = 600
	askValueMaxLength  = 255
	askReasonMaxLength = 200
	// askMaxIdentity bounds a suggested identity's properties.
	askMaxIdentity = 5
	// askDescriptionMax shortens the descriptions the message carries, so a wide table stays small.
	askDescriptionMax = 200
	// askNoSuggestion is what the Agent says when it suggests nothing.
	askNoSuggestion = "none"
)

// askInput is the message the Agent reads: the question, the language to answer in, the object
// type as the draft holds it now, and the relation types it is an end of. It carries no row data.
type askInput struct {
	Question   string        `json:"question"`
	Language   string        `json:"language"`
	ObjectType askObjectType `json:"object_type"`
	Relations  []askRelation `json:"relation_types"`
	// Editable are the fields a suggestion may change.
	Editable []string `json:"editable"`
}

type askObjectType struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	Aliases       []string                    `json:"aliases,omitempty"`
	Description   string                      `json:"description"`
	Table         string                      `json:"table"`
	Adopted       bool                        `json:"adopted"`
	NeedsIdentity bool                        `json:"needs_identity"`
	Identity      []string                    `json:"identity"`
	IdentityCount interfaces.DraftKeyEvidence `json:"identity_evidence"`
	DisplayKey    string                      `json:"display_key"`
	Properties    []askProperty               `json:"properties"`
}

type askProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Field       string `json:"field"`
	Description string `json:"description,omitempty"`
}

type askRelation struct {
	ID          string                         `json:"id"`
	Name        string                         `json:"name"`
	Source      string                         `json:"source"`
	Target      string                         `json:"target"`
	Cardinality string                         `json:"cardinality"`
	Mapping     []interfaces.DraftFieldMapping `json:"mapping,omitempty"`
	Adopted     bool                           `json:"adopted"`
	HitRate     *float64                       `json:"hit_rate,omitempty"`
}

type askOutput struct {
	Answer     string `json:"answer"`
	Suggestion struct {
		Field      string   `json:"field"`
		Value      string   `json:"value"`
		Properties []string `json:"properties"`
		Reason     string   `json:"reason"`
	} `json:"suggestion"`
}

// askResponseFormat is the JSON Schema of an answer: short, with one suggestion at most.
func askResponseFormat() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string", "maxLength": askAnswerMaxLength},
			"suggestion": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "enum": []string{askNoSuggestion,
						interfaces.DRAFT_ASK_FIELD_NAME, interfaces.DRAFT_ASK_FIELD_DESCRIPTION, interfaces.DRAFT_ASK_FIELD_IDENTITY}},
					"value":      map[string]any{"type": "string", "maxLength": askValueMaxLength},
					"properties": map[string]any{"type": "array", "maxItems": askMaxIdentity, "items": map[string]any{"type": "string"}},
					"reason":     map[string]any{"type": "string", "maxLength": askReasonMaxLength},
				},
				"required":             []string{"field"},
				"additionalProperties": false,
			},
		},
		"required":             []string{"answer", "suggestion"},
		"additionalProperties": false,
	}
}

func (s *draftService) AskObjectType(ctx context.Context, token string, projectID string, itemID string,
	req interfaces.DraftAskRequest, caller interfaces.AccountInfo) (*interfaces.DraftAskAnswer, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskObjectType")
	defer span.End()

	question := strings.TrimSpace(req.Question)
	if question == "" || utf8.RuneCountInString(question) > interfaces.DRAFT_ASK_QUESTION_MAX_LENGTH {
		span.SetStatus(codes.Error, "Invalid question")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
	}
	draft, err := s.GetDraft(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, err
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		span.SetStatus(codes.Error, "Draft not ready")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	objectType := findObjectType(&draft.Items, itemID)
	if objectType == nil {
		span.SetStatus(codes.Error, "Object type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}

	message, err := json.Marshal(askInputOf(draft, objectType, question))
	if err != nil {
		return nil, internalError(ctx, "Encode question", err)
	}
	out, code := s.askAgent(ctx, caller, string(message))
	if code != "" {
		span.SetStatus(codes.Error, "Ask AI failed")
		status := http.StatusBadGateway
		if code == perrors.BknProject_Draft_AskTimeout {
			status = http.StatusGatewayTimeout
		}
		return nil, httperrors.NewHTTPError(ctx, status, code)
	}
	answer := &interfaces.DraftAskAnswer{
		Answer: truncateRunes(strings.TrimSpace(out.Answer), askAnswerMaxLength),
		// A suggestion the edits would refuse, or one that changes nothing, is left out; the
		// answer stays.
		Suggestion: validSuggestion(objectType, out),
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

// askInputOf is what the Agent reads of the draft: the object type and its relation types.
func askInputOf(draft *interfaces.Draft, objectType *interfaces.DraftObjectType, question string) askInput {
	in := askInput{
		Question: question,
		Language: draftLanguage(draft),
		ObjectType: askObjectType{
			ID: objectType.ID, Name: objectType.Name, Aliases: objectType.Aliases,
			Description: truncateRunes(objectType.Description, askDescriptionMax), Table: objectType.Table.Name,
			Adopted: objectType.Adopted, NeedsIdentity: objectType.NeedsIdentity,
			Identity: append([]string{}, objectType.PrimaryKeys...), IdentityCount: objectType.Key,
			DisplayKey: objectType.DisplayKey, Properties: []askProperty{},
		},
		Relations: []askRelation{},
		Editable: []string{interfaces.DRAFT_ASK_FIELD_NAME, interfaces.DRAFT_ASK_FIELD_DESCRIPTION,
			interfaces.DRAFT_ASK_FIELD_IDENTITY},
	}
	for _, p := range objectType.Properties {
		in.ObjectType.Properties = append(in.ObjectType.Properties, askProperty{
			Name: p.Name, DisplayName: p.DisplayName, Type: p.Type, Field: p.MappedField,
			Description: truncateRunes(p.Description, askDescriptionMax),
		})
	}
	for _, r := range draft.Items.RelationTypes {
		if r.Source != objectType.ID && r.Target != objectType.ID {
			continue
		}
		relation := askRelation{ID: r.ID, Name: r.Name, Source: r.Source, Target: r.Target, Cardinality: r.Cardinality,
			Mapping: r.Mapping, Adopted: r.Adopted}
		if r.Evidence.Computed {
			rate := r.Evidence.HitRate
			relation.HitRate = &rate
		}
		in.Relations = append(in.Relations, relation)
	}
	return in
}

// askAgent runs the Agent as the caller and waits for it until the deadline. It returns the
// answer, or the code of why there is none.
func (s *draftService) askAgent(ctx context.Context, caller interfaces.AccountInfo, message string) (*askOutput, string) {
	if s.ba == nil {
		return nil, perrors.BknProject_Draft_AskUnavailable
	}
	deadline := s.clock().Add(askDeadline)
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: askAgentID, Message: message, ResponseFormat: askResponseFormat(),
	})
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Run Ask AI: %v", err))
		return nil, perrors.BknProject_Draft_AskUnavailable
	}
	for !task.Terminal() {
		// A request the client or the gateway cut has no one to answer: stop waiting.
		if ctx.Err() != nil {
			return nil, perrors.BknProject_Draft_AskUnavailable
		}
		if !s.clock().Before(deadline) {
			otellog.LogWarn(ctx, fmt.Sprintf("Ask AI task %s did not finish", task.TaskID))
			return nil, perrors.BknProject_Draft_AskTimeout
		}
		s.wait(askPoll)
		// A failed read is tried again until the deadline: the task goes on in bkn-agent.
		readCtx, cancel := context.WithTimeout(ctx, askReadTimeout)
		read, err := s.ba.GetTask(readCtx, caller, task.TaskID)
		cancel()
		if err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		otellog.LogWarn(ctx, fmt.Sprintf("Ask AI task %s failed: %s", task.TaskID, task.FailureDetail))
		return nil, perrors.BknProject_Draft_AskUnavailable
	}
	var out askOutput
	if err := json.Unmarshal([]byte(task.Output), &out); err != nil || strings.TrimSpace(out.Answer) == "" {
		otellog.LogWarn(ctx, fmt.Sprintf("Ask AI task %s answered what its schema does not allow", task.TaskID))
		return nil, perrors.BknProject_Draft_AskUnavailable
	}
	return &out, ""
}

// validSuggestion is the Agent's suggestion when the edit it names would take it and it changes
// something, else nil.
func validSuggestion(objectType *interfaces.DraftObjectType, out *askOutput) *interfaces.DraftAskSuggestion {
	sug := out.Suggestion
	reason := truncateRunes(strings.TrimSpace(sug.Reason), askReasonMaxLength)
	value := strings.TrimSpace(sug.Value)
	switch sug.Field {
	case interfaces.DRAFT_ASK_FIELD_NAME:
		if value == "" || utf8.RuneCountInString(value) > interfaces.DRAFT_NAME_MAX_LENGTH || value == objectType.Name {
			return nil
		}
		return &interfaces.DraftAskSuggestion{Field: sug.Field, Value: value, Reason: reason}
	case interfaces.DRAFT_ASK_FIELD_DESCRIPTION:
		if value == "" || utf8.RuneCountInString(value) > askValueMaxLength || value == objectType.Description {
			return nil
		}
		return &interfaces.DraftAskSuggestion{Field: sug.Field, Value: value, Reason: reason}
	case interfaces.DRAFT_ASK_FIELD_IDENTITY:
		names := sug.Properties
		if len(names) == 0 || len(names) > askMaxIdentity ||
			(!objectType.NeedsIdentity && slices.Equal(names, objectType.PrimaryKeys)) {
			return nil
		}
		for i, name := range names {
			// As ChooseIdentity takes them: distinct properties of a key type.
			p := findProperty(objectType, name)
			if p == nil || !keyTypes[p.Type] || slices.Contains(names[:i], name) {
				return nil
			}
		}
		return &interfaces.DraftAskSuggestion{Field: sug.Field, Properties: append([]string{}, names...), Reason: reason}
	}
	return nil
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func (s *draftService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *draftService) wait(d time.Duration) {
	if s.sleep != nil {
		s.sleep(d)
		return
	}
	time.Sleep(d)
}
