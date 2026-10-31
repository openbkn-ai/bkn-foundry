// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// Ask AI: a question about one item of the draft, an object type or a relation type, answered by
// a bkn-agent task Agent from the item as the draft holds it and as the editor's unsaved form has
// it. It reads the draft and never writes it: the revisions it proposes are checked by the rules
// of the item's save, and a person saves them, which records the decision.

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
	// askMaxRevisions bounds the revisions of one answer.
	askMaxRevisions = 5
	// askDescriptionMax shortens the descriptions the message carries, so a wide table stays small.
	askDescriptionMax = 200
)

// What the message says it asks about.
const (
	askKindObjectType   = "object_type"
	askKindRelationType = "relation_type"
)

// Why a revision is left out, besides the reasons a relation's save gives.
const (
	askDroppedUnchanged          = "unchanged"
	askDroppedRepeated           = "repeated"
	askDroppedNotEditable        = "not_editable"
	askDroppedInvalidName        = "invalid_name"
	askDroppedInvalidDescription = "invalid_description"
	askDroppedInvalidIdentity    = "invalid_identity"
	askDroppedUnknownProperty    = "unknown_property"
	askDroppedTooLong            = "too_long"
	askDroppedInvalid            = "invalid"
)

// askInput is the message the Agent reads: the question, the language to answer in, the
// conversation so far, and the item asked about with what it joins. It carries no row data.
type askInput struct {
	Question string                    `json:"question"`
	Language string                    `json:"language"`
	History  []interfaces.DraftAskTurn `json:"history,omitempty"`
	Kind     string                    `json:"kind"`
	// ObjectType and Relations, the relation types it is an end of, for an object type.
	ObjectType *askObjectType `json:"object_type,omitempty"`
	Relations  []askRelation  `json:"relation_types,omitempty"`
	// RelationType and Ends, its source and target, for a relation type.
	RelationType *askRelation    `json:"relation_type,omitempty"`
	Ends         []askObjectType `json:"ends,omitempty"`
	// Editable are the fields a revision may change.
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
	ID            string                         `json:"id"`
	Name          string                         `json:"name"`
	Description   string                         `json:"description,omitempty"`
	Source        string                         `json:"source"`
	Target        string                         `json:"target"`
	Cardinality   string                         `json:"cardinality"`
	Mapping       []interfaces.DraftFieldMapping `json:"mapping,omitempty"`
	LinkTable     string                         `json:"link_table,omitempty"`
	SourceMapping []interfaces.DraftFieldMapping `json:"source_mapping,omitempty"`
	TargetMapping []interfaces.DraftFieldMapping `json:"target_mapping,omitempty"`
	Adopted       bool                           `json:"adopted"`
	HitRate       *float64                       `json:"hit_rate,omitempty"`
}

type askOutput struct {
	Answer    string              `json:"answer"`
	Revisions []askRevisionOutput `json:"revisions"`
}

// askRevisionOutput is a revision as the Agent gives it: Value for a name, comment or display key,
// Properties for an identity, Property for a property, Source, Target and Mapping for ends.
type askRevisionOutput struct {
	Field      string                               `json:"field"`
	Value      string                               `json:"value"`
	Properties []string                             `json:"properties"`
	Property   *interfaces.DraftAskPropertyRevision `json:"property"`
	Source     string                               `json:"source_object_type_id"`
	Target     string                               `json:"target_object_type_id"`
	Mapping    []interfaces.DraftFieldMapping       `json:"mapping"`
	Reason     string                               `json:"reason"`
}

// askResponseFormat is the JSON Schema of an answer: short, with a few revisions of the fields
// given.
func askResponseFormat(fields []string) map[string]any {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required,
			"additionalProperties": false}
	}
	revision := object(map[string]any{
		"field":      map[string]any{"type": "string", "enum": fields},
		"value":      str(askValueMaxLength),
		"properties": map[string]any{"type": "array", "maxItems": askMaxIdentity, "items": map[string]any{"type": "string"}},
		"property": object(map[string]any{
			"name": map[string]any{"type": "string"}, "display_name": str(interfaces.DRAFT_NAME_MAX_LENGTH),
			"comment": str(askValueMaxLength),
		}, "name"),
		"source_object_type_id": map[string]any{"type": "string"},
		"target_object_type_id": map[string]any{"type": "string"},
		"mapping": map[string]any{"type": "array", "items": object(map[string]any{
			"source_property": map[string]any{"type": "string"}, "target_property": map[string]any{"type": "string"},
		}, "source_property", "target_property")},
		"reason": str(askReasonMaxLength),
	}, "field", "reason")
	return object(map[string]any{
		"answer":    str(askAnswerMaxLength),
		"revisions": map[string]any{"type": "array", "maxItems": askMaxRevisions, "items": revision},
	}, "answer", "revisions")
}

// askTurn is a question ready to ask: the message the Agent reads, the fields it may revise, and
// how its output becomes the answer. Asking waits for the task; a streamed ask would read the same
// message and finish the same way.
type askTurn struct {
	input  askInput
	finish func(out *askOutput) *interfaces.DraftAskAnswer
}

// readyToAsk checks the question, reads a ready draft and keeps the last turns of the conversation.
func (s *draftService) readyToAsk(ctx context.Context, token string, projectID string, question string,
	history []interfaces.DraftAskTurn) (*interfaces.Draft, askInput, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > interfaces.DRAFT_ASK_QUESTION_MAX_LENGTH {
		return nil, askInput{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
	}
	draft, err := s.GetDraft(ctx, token, projectID)
	if err != nil {
		return nil, askInput{}, err
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil, askInput{}, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	in := askInput{Question: question, Language: draftLanguage(draft)}
	for _, turn := range history[max(0, len(history)-interfaces.DRAFT_ASK_HISTORY_MAX):] {
		q, a := strings.TrimSpace(turn.Question), strings.TrimSpace(turn.Answer)
		if q == "" {
			continue
		}
		in.History = append(in.History, interfaces.DraftAskTurn{
			Question: truncateRunes(q, interfaces.DRAFT_ASK_QUESTION_MAX_LENGTH), Answer: truncateRunes(a, askAnswerMaxLength)})
	}
	return draft, in, nil
}

// ask runs the turn's Agent and finishes its answer.
func (s *draftService) ask(ctx context.Context, caller interfaces.AccountInfo, turn askTurn) (*interfaces.DraftAskAnswer, error) {
	message, err := json.Marshal(turn.input)
	if err != nil {
		return nil, internalError(ctx, "Encode question", err)
	}
	out, code := s.askAgent(ctx, caller, string(message), askResponseFormat(turn.input.Editable))
	if code != "" {
		status := http.StatusBadGateway
		if code == perrors.BknProject_Draft_AskTimeout {
			status = http.StatusGatewayTimeout
		}
		return nil, httperrors.NewHTTPError(ctx, status, code)
	}
	answer := turn.finish(out)
	answer.Answer = truncateRunes(strings.TrimSpace(out.Answer), askAnswerMaxLength)
	return answer, nil
}

func (s *draftService) AskObjectType(ctx context.Context, token string, projectID string, itemID string,
	req interfaces.DraftAskRequest, caller interfaces.AccountInfo) (*interfaces.DraftAskAnswer, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskObjectType")
	defer span.End()

	draft, in, err := s.readyToAsk(ctx, token, projectID, req.Question, req.History)
	if err != nil {
		span.SetStatus(codes.Error, "Not askable")
		return nil, err
	}
	objectType := findObjectType(&draft.Items, itemID)
	if objectType == nil {
		span.SetStatus(codes.Error, "Object type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	base := workingObjectType(objectType, req.Working)
	in.Kind = askKindObjectType
	asked := askObjectTypeOf(base)
	in.ObjectType, in.Relations = &asked, []askRelation{}
	for _, r := range draft.Items.RelationTypes {
		if r.Source == objectType.ID || r.Target == objectType.ID {
			in.Relations = append(in.Relations, askRelationOf(r))
		}
	}
	in.Editable = []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT,
		interfaces.DRAFT_ASK_REVISION_PRIMARY_KEYS, interfaces.DRAFT_ASK_REVISION_DISPLAY_KEY,
		interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY}

	answer, err := s.ask(ctx, caller, askTurn{input: in, finish: func(out *askOutput) *interfaces.DraftAskAnswer {
		answer := checkRevisions(out.Revisions, in.Editable, func(r askRevisionOutput) (*interfaces.DraftAskRevision, string) {
			return objectTypeRevision(ctx, base, r)
		})
		answer.Suggestion = suggestionOf(answer.Revisions)
		return answer
	}})
	if err != nil {
		span.SetStatus(codes.Error, "Ask AI failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

func (s *draftService) AskRelationType(ctx context.Context, token string, projectID string, itemID string,
	req interfaces.DraftRelationAskRequest, caller interfaces.AccountInfo) (*interfaces.DraftAskAnswer, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskRelationType")
	defer span.End()

	draft, in, err := s.readyToAsk(ctx, token, projectID, req.Question, req.History)
	if err != nil {
		span.SetStatus(codes.Error, "Not askable")
		return nil, err
	}
	relationType := findRelationType(&draft.Items, itemID)
	if relationType == nil {
		span.SetStatus(codes.Error, "Relation type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	base := s.workingRelationType(ctx, &draft.Items, relationType, req.Working, caller)
	in.Kind = askKindRelationType
	asked := askRelationOf(base)
	in.RelationType, in.Ends = &asked, []askObjectType{}
	for _, id := range []string{base.Source, base.Target} {
		if ot := findObjectType(&draft.Items, id); ot != nil {
			in.Ends = append(in.Ends, askObjectTypeOf(ot))
		}
	}
	in.Editable = []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT,
		interfaces.DRAFT_ASK_REVISION_ENDS}

	answer, err := s.ask(ctx, caller, askTurn{input: in, finish: func(out *askOutput) *interfaces.DraftAskAnswer {
		return checkRevisions(out.Revisions, in.Editable, func(r askRevisionOutput) (*interfaces.DraftAskRevision, string) {
			return s.relationTypeRevision(ctx, &draft.Items, base, r, caller)
		})
	}})
	if err != nil {
		span.SetStatus(codes.Error, "Ask AI failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

func askObjectTypeOf(objectType *interfaces.DraftObjectType) askObjectType {
	asked := askObjectType{
		ID: objectType.ID, Name: objectType.Name, Aliases: objectType.Aliases,
		Description: truncateRunes(objectType.Description, askDescriptionMax), Table: objectType.Table.Name,
		Adopted: objectType.Adopted, NeedsIdentity: objectType.NeedsIdentity,
		Identity: append([]string{}, objectType.PrimaryKeys...), IdentityCount: objectType.Key,
		DisplayKey: objectType.DisplayKey, Properties: []askProperty{},
	}
	for _, p := range objectType.Properties {
		asked.Properties = append(asked.Properties, askProperty{
			Name: p.Name, DisplayName: p.DisplayName, Type: p.Type, Field: p.MappedField,
			Description: truncateRunes(p.Description, askDescriptionMax),
		})
	}
	return asked
}

func askRelationOf(r *interfaces.DraftRelationType) askRelation {
	relation := askRelation{ID: r.ID, Name: r.Name, Description: truncateRunes(r.Description, askDescriptionMax),
		Source: r.Source, Target: r.Target, Cardinality: r.Cardinality, Mapping: r.Mapping,
		SourceMapping: r.SourceMapping, TargetMapping: r.TargetMapping, Adopted: r.Adopted}
	if r.LinkTable != nil {
		relation.LinkTable = r.LinkTable.Name
	}
	if r.Evidence.Computed {
		rate := r.Evidence.HitRate
		relation.HitRate = &rate
	}
	return relation
}

// askAgent runs the Agent as the caller and waits for it until the deadline. It returns the
// answer, or the code of why there is none.
func (s *draftService) askAgent(ctx context.Context, caller interfaces.AccountInfo, message string,
	format map[string]any) (*askOutput, string) {
	if s.ba == nil {
		return nil, perrors.BknProject_Draft_AskUnavailable
	}
	deadline := s.clock().Add(askDeadline)
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: askAgentID, Message: message, ResponseFormat: format,
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
