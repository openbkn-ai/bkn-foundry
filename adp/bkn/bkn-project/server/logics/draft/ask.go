// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI: a question about one item of the draft, an object type or a relation type, answered by
// a bkn-agent chat Agent from the item as the draft holds it and as the editor's unsaved form has
// it. It reads the draft and never writes it: the revisions it proposes are checked by the rules
// of the item's save, and a person saves them, which records the decision.

const (
	// askAgentID is the bkn-agent chat Agent that answers questions about a draft item.
	askAgentID = "bkn-project-ontology-ask"
	// askDeadline bounds an answer: gateways commonly cut a request that is not streamed at 60
	// seconds.
	askDeadline = 45 * time.Second
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
	// Candidates, for a relation type not made yet with an end, the relations the data suggests.
	Candidates []askCandidateLine `json:"candidates,omitempty"`
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

// askOutput is the structured result that follows the answer's text.
type askOutput struct {
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

// askResponseFormat is the JSON Schema of the result that follows the answer: a few revisions of
// the fields given.
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
		"revisions": map[string]any{"type": "array", "maxItems": askMaxRevisions, "items": revision},
	}, "revisions")
}

// askTurn is a question ready to ask: the message the Agent reads, the fields it may revise, and
// how its result becomes the answer.
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

// ask runs the turn's Agent, streaming its text when asked to, and finishes its answer.
func (s *draftService) ask(ctx context.Context, caller interfaces.AccountInfo, turn askTurn,
	stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
	message, err := json.Marshal(turn.input)
	if err != nil {
		return nil, internalError(ctx, "Encode question", err)
	}
	text, raw, code := s.askAgent(ctx, caller, string(message), askResponseFormat(turn.input.Editable), stream, false)
	var out askOutput
	if code == "" && json.Unmarshal(raw, &out) != nil {
		code = perrors.BknProject_Draft_AskUnavailable
	}
	if code != "" {
		status := http.StatusBadGateway
		if code == perrors.BknProject_Draft_AskTimeout {
			status = http.StatusGatewayTimeout
		}
		return nil, httperrors.NewHTTPError(ctx, status, code)
	}
	answer := turn.finish(&out)
	answer.Answer = truncateRunes(text, askAnswerMaxLength)
	return answer, nil
}

func (s *draftService) AskObjectType(ctx context.Context, token string, projectID string, itemID string,
	req interfaces.DraftAskRequest, caller interfaces.AccountInfo, stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
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
	}}, stream)
	if err != nil {
		span.SetStatus(codes.Error, "Ask AI failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

func (s *draftService) AskRelationType(ctx context.Context, token string, projectID string, itemID string,
	req interfaces.DraftRelationAskRequest, caller interfaces.AccountInfo, stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
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
	}}, stream)
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

// askAgent runs the chat Agent as the caller until the deadline, handing the answer's text to
// stream as it is written. It returns the text and the result that follows it, as given, or the
// code of why there is none. A result with no text written fails unless answerOptional.
func (s *draftService) askAgent(ctx context.Context, caller interfaces.AccountInfo, message string,
	format map[string]any, stream *interfaces.DraftAskStream, answerOptional bool) (string, json.RawMessage, string) {
	if s.ba == nil {
		return "", nil, perrors.BknProject_Draft_AskUnavailable
	}
	wait := s.askWait
	if wait == 0 {
		wait = askDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	reply := newAskReply(stream)
	var out json.RawMessage
	var failure string
	started := false
	err := s.ba.Chat(ctx, caller, &interfaces.BknAgentChatRequest{AgentID: askAgentID, Message: message,
		ResponseFormat: format}, func(event interfaces.BknAgentChatEvent) {
		if !started && stream != nil {
			stream.Start()
		}
		started = true
		var data struct {
			Content json.RawMessage `json:"content"`
			Code    string          `json:"code"`
			Detail  string          `json:"detail"`
		}
		_ = json.Unmarshal(event.Data, &data)
		switch event.Name {
		case interfaces.BKN_AGENT_CHAT_TOKEN:
			var token string
			if json.Unmarshal(data.Content, &token) == nil && token != "" {
				reply.add(token)
			}
		case interfaces.BKN_AGENT_CHAT_STRUCTURED:
			if trimmed := strings.TrimSpace(string(data.Content)); strings.HasPrefix(trimmed, "{") {
				out = data.Content
			}
		case interfaces.BKN_AGENT_CHAT_ERROR:
			failure = data.Code
			otellog.LogWarn(ctx, fmt.Sprintf("Ask AI failed: %s %s", data.Code, data.Detail))
		}
	})
	reply.finish()
	answer := reply.answer()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), failure == interfaces.BKN_AGENT_CHAT_TIMEOUT:
		otellog.LogWarn(ctx, "Ask AI did not finish in time")
		return answer, nil, perrors.BknProject_Draft_AskTimeout
	case err != nil:
		otellog.LogWarn(ctx, fmt.Sprintf("Ask AI: %v", err))
		return answer, nil, perrors.BknProject_Draft_AskUnavailable
	case failure != "", out == nil, answer == "" && !answerOptional:
		// The start of the reply says what came instead of an answer, such as JSON alone.
		otellog.LogWarn(ctx, fmt.Sprintf("Ask AI answered what its schema does not allow: result %t, reply %q (%d characters)",
			out != nil, truncateRunes(reply.text, askReasonMaxLength), utf8.RuneCountInString(reply.text)))
		return answer, nil, perrors.BknProject_Draft_AskUnavailable
	}
	return answer, out, ""
}

// askJSONStart starts the JSON an Agent may write after its answer despite the prompt: a code
// fence, or an object of a result's keys or of an answer, however it is spaced.
var askJSONStart = regexp.MustCompile("```|\\{\\s*\"(?:revisions|candidates|proposals|answer)\"")

// askJSONKeys are the keys that make an object the Agent's JSON.
var askJSONKeys = []string{`"revisions"`, `"candidates"`, `"proposals"`, `"answer"`}

// askReply is the answer's text as it streams. It is forwarded up to the first JSON marker, a
// tail that may begin one held until the next piece shows it does not; a reply that is JSON from
// its start is not forwarded at all.
type askReply struct {
	text   string
	sent   int
	cut    int
	stream *interfaces.DraftAskStream
}

func newAskReply(stream *interfaces.DraftAskStream) *askReply {
	return &askReply{cut: -1, stream: stream}
}

func (r *askReply) add(piece string) {
	r.text += piece
	if r.cut >= 0 {
		return
	}
	if start := len(r.text) - len(strings.TrimLeftFunc(r.text, unicode.IsSpace)); strings.HasPrefix(r.text[start:], "{") {
		r.cut = start
		return
	}
	if at := askJSONStart.FindStringIndex(r.text); at != nil {
		r.cut = at[0]
		r.forward(r.cut)
		return
	}
	r.forward(len(r.text) - r.held())
}

// held is how much of the text's end may begin the JSON: backticks, or a brace followed by spaces
// and the start of a key; text of spaces alone is held whole.
func (r *askReply) held() int {
	if strings.TrimSpace(r.text) == "" {
		return len(r.text)
	}
	longest := len(r.text) - len(strings.TrimRight(r.text, "`"))
	if i := strings.LastIndex(r.text, "{"); i >= 0 {
		rest := strings.TrimLeftFunc(r.text[i+1:], unicode.IsSpace)
		if slices.ContainsFunc(askJSONKeys, func(key string) bool { return strings.HasPrefix(key, rest) }) {
			longest = len(r.text) - i
		}
	}
	return longest
}

// forward forwards the text up to to; what is held never begins a marker, so to is never behind.
func (r *askReply) forward(to int) {
	if to > r.sent && r.stream != nil {
		r.stream.Token(r.text[r.sent:to])
	}
	r.sent = to
}

// finish forwards what was held, unless it is JSON or spaces.
func (r *askReply) finish() {
	if r.cut < 0 && strings.TrimSpace(r.text) != "" {
		r.forward(len(r.text))
	}
}

// answer is the text before the JSON, or the answer a reply written wholly as JSON holds.
func (r *askReply) answer() string {
	if r.cut < 0 {
		return strings.TrimSpace(r.text)
	}
	if before := strings.TrimSpace(r.text[:r.cut]); before != "" {
		return before
	}
	// The first object, past a fence and whatever follows it.
	start := strings.Index(r.text, "{")
	if start < 0 {
		return ""
	}
	var wrapped struct {
		Answer string `json:"answer"`
	}
	if json.NewDecoder(strings.NewReader(escapeLineBreaks(r.text[start:]))).Decode(&wrapped) != nil {
		return ""
	}
	return strings.TrimSpace(wrapped.Answer)
}

// escapeLineBreaks escapes the line breaks and tabs an Agent writes as they are inside JSON strings.
func escapeLineBreaks(text string) string {
	var b strings.Builder
	quoted, escaped := false, false
	for _, c := range text {
		switch {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case quoted && c == '\n':
			b.WriteString(`\n`)
			continue
		case quoted && c == '\r':
			b.WriteString(`\r`)
			continue
		case quoted && c == '\t':
			b.WriteString(`\t`)
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
