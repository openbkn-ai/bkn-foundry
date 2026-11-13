// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI on the kinetic modeling and business scenarios pages: a person asks about the list, the
// requirements Agent answers in pass ask from what the analysis reads (the documents, the network's
// background, the confirmed model, the specification and the supplement note) and proposes changes
// of the list. Each is checked here against the specification as asked; nothing changes until a
// person adopts it. A turn is answered in the background, as runs are, and the conversation keeps
// it.

const (
	// askTitleMax bounds a conversation's title, its first question.
	askTitleMax = 60
	// askHistoryMax is how many earlier turns of the conversation go with a question.
	askHistoryMax = 6
	// askAnswerMax bounds an answer, askHistoryAnswerMax one the history carries.
	askAnswerMax        = 2000
	askHistoryAnswerMax = 600
	// askMaxProposals bounds the proposals of one answer; askReasonMax the reason of one.
	askMaxProposals = 10
	askReasonMax    = 200
	// askWriteTries bounds the tries to write a turn over a conversation changed meanwhile.
	askWriteTries = 5
)

// askKinds are the kinds of items each page proposes changes of, with the changes each takes.
var askKinds = map[string]map[string][]string{
	interfaces.REQUIREMENT_PAGE_KINETIC: {
		interfaces.REQUIREMENT_KIND_LOGIC_NEEDS: {interfaces.REQUIREMENT_PROPOSAL_ADD, interfaces.REQUIREMENT_PROPOSAL_MODIFY,
			interfaces.REQUIREMENT_PROPOSAL_MERGE, interfaces.REQUIREMENT_PROPOSAL_DELETE, interfaces.REQUIREMENT_PROPOSAL_LINK},
	},
	interfaces.REQUIREMENT_PAGE_SCENARIOS: {
		interfaces.REQUIREMENT_KIND_SCENARIOS: {interfaces.REQUIREMENT_PROPOSAL_ADD, interfaces.REQUIREMENT_PROPOSAL_MODIFY,
			interfaces.REQUIREMENT_PROPOSAL_MERGE, interfaces.REQUIREMENT_PROPOSAL_DELETE, interfaces.REQUIREMENT_PROPOSAL_SPLIT},
		interfaces.REQUIREMENT_KIND_QUESTIONS: {interfaces.REQUIREMENT_PROPOSAL_ADD, interfaces.REQUIREMENT_PROPOSAL_MODIFY,
			interfaces.REQUIREMENT_PROPOSAL_MERGE, interfaces.REQUIREMENT_PROPOSAL_DELETE},
		interfaces.REQUIREMENT_KIND_RULES: {interfaces.REQUIREMENT_PROPOSAL_ADD, interfaces.REQUIREMENT_PROPOSAL_MODIFY,
			interfaces.REQUIREMENT_PROPOSAL_DELETE},
	},
}

// agentProposal is a proposal as the Agent gives it.
type agentProposal struct {
	Op            string                               `json:"op"`
	Kind          string                               `json:"kind"`
	TargetID      string                               `json:"target_id"`
	IntoID        string                               `json:"into_id"`
	Item          *interfaces.RequirementProposalItem  `json:"item"`
	Parts         []interfaces.RequirementProposalItem `json:"parts"`
	Moves         []interfaces.RequirementProposalMove `json:"moves"`
	NetworkItemID string                               `json:"network_item_id"`
	Reason        string                               `json:"reason"`
}

// askOutput is what the Agent returns in pass ask: the answer, then the proposals.
type askOutput struct {
	Answer    string          `json:"answer"`
	Proposals []agentProposal `json:"proposals"`
}

func askResponseFormat() map[string]any {
	item := schemaObject(map[string]any{
		"logic_kind": schemaEnum(append([]string{""}, interfaces.LogicNeedKinds...)...), "name": schemaString(),
		"definition": schemaString(), "object_type_ids": schemaStrings(), "actor": schemaString(),
		"trigger": schemaString(), "decision": schemaString(), "knowledge": schemaStrings(), "text": schemaString(),
		"scenario_ids": schemaStrings(), "logic_need_ids": schemaStrings(), "statement": schemaString(),
	})
	ops := []string{interfaces.REQUIREMENT_PROPOSAL_ADD, interfaces.REQUIREMENT_PROPOSAL_MODIFY,
		interfaces.REQUIREMENT_PROPOSAL_MERGE, interfaces.REQUIREMENT_PROPOSAL_DELETE, interfaces.REQUIREMENT_PROPOSAL_LINK,
		interfaces.REQUIREMENT_PROPOSAL_SPLIT}
	return schemaObject(map[string]any{
		"answer": schemaString(),
		"proposals": schemaArray(schemaObject(map[string]any{
			"op": schemaEnum(ops...),
			"kind": schemaEnum(interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, interfaces.REQUIREMENT_KIND_SCENARIOS,
				interfaces.REQUIREMENT_KIND_QUESTIONS, interfaces.REQUIREMENT_KIND_RULES),
			"target_id": schemaString(), "into_id": schemaString(), "item": item, "parts": schemaArray(item),
			"moves": schemaArray(schemaObject(map[string]any{
				"question_id": schemaString(), "part": map[string]any{"type": "integer"},
			}, "question_id", "part")),
			"network_item_id": schemaString(), "reason": schemaString(),
		}, "op", "kind", "reason")),
	}, "answer", "proposals")
}

// askMessage is the user message of pass ask.
type askMessage struct {
	Page       string                `json:"page"`
	Question   string                `json:"question"`
	History    []askHistoryTurn      `json:"history"`
	Refs       []string              `json:"refs"`
	Supplement string                `json:"supplement,omitempty"`
	Documents  []messageDocument     `json:"documents"`
	ObjectType []messageObjectType   `json:"object_types"`
	Relations  []messageRelationType `json:"relation_types"`
	Concepts   []messageConcept      `json:"concepts"`
	Rules      []askRule             `json:"rules"`
	LogicNeeds []messageLogicNeed    `json:"logic_needs"`
	// NetworkItems are what the network already has, by kept-network- ids.
	NetworkItems []messageLogicNeed `json:"network_items"`
	Scenarios    []askScenario      `json:"scenarios,omitempty"`
	Questions    []askQuestion      `json:"questions,omitempty"`
	// Editable are the kinds of items the page proposes changes of, with the changes each takes.
	Editable map[string][]string `json:"editable"`
}

type askHistoryTurn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type askRule struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type askScenario struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Actor     string   `json:"actor,omitempty"`
	Trigger   string   `json:"trigger,omitempty"`
	Decision  string   `json:"decision,omitempty"`
	Knowledge []string `json:"knowledge,omitempty"`
}

type askQuestion struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	ScenarioIDs  []string `json:"scenario_ids"`
	LogicNeedIDs []string `json:"logic_need_ids"`
}

// askInputs is what a turn reads, gathered as the caller before it goes to the background.
type askInputs struct {
	language   string
	supplement string
	docs       []*interfaces.RequirementDocument
	draft      *interfaces.Draft
	spec       *interfaces.RequirementSpec
	items      []*interfaces.LogicNetworkItem
}

func buildAskMessage(page string, question string, refs []interfaces.RequirementAskRef, history []askHistoryTurn,
	in *askInputs) (string, error) {
	msg := askMessage{Page: page, Question: question, History: history, Refs: []string{}, Supplement: in.supplement,
		Documents: messageDocumentsOf(in.docs), Concepts: messageConceptsOf(in.spec.Concepts), Rules: []askRule{},
		LogicNeeds: messageLogicNeedsOf(in.spec.LogicNeeds), Editable: map[string][]string{}}
	for _, ref := range refs {
		msg.Refs = append(msg.Refs, ref.ID)
	}
	msg.ObjectType, msg.Relations, _ = messageOntologyOf(in.draft, true)
	msg.NetworkItems, _ = messageNetworkItemsOf(in.items, modelNames(msg.ObjectType))
	for _, r := range in.spec.Rules {
		msg.Rules = append(msg.Rules, askRule{ID: r.ID, Statement: r.Statement})
	}
	if page == interfaces.REQUIREMENT_PAGE_SCENARIOS {
		msg.Scenarios, msg.Questions = []askScenario{}, []askQuestion{}
		for _, sc := range in.spec.Scenarios {
			msg.Scenarios = append(msg.Scenarios, askScenario{ID: sc.ID, Name: sc.Name, Actor: sc.Actor,
				Trigger: sc.Trigger, Decision: sc.Decision, Knowledge: sc.Knowledge})
		}
		for _, q := range in.spec.Questions {
			msg.Questions = append(msg.Questions, askQuestion{ID: q.ID, Text: q.Text, ScenarioIDs: orEmpty(q.ScenarioIDs),
				LogicNeedIDs: orEmpty(q.LogicNeedIDs)})
		}
	}
	for kind, ops := range askKinds[page] {
		msg.Editable[kind] = ops
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return "pass ask\nlanguage: " + in.language + "\n" + string(body), nil
}

// specItem is the item of kind with the id in spec, nil when it has none.
func specItem(spec *interfaces.RequirementSpec, kind string, id string) any {
	switch kind {
	case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
		if i, ok := find(spec.LogicNeeds, id, logicNeedID); ok {
			return spec.LogicNeeds[i]
		}
	case interfaces.REQUIREMENT_KIND_SCENARIOS:
		if i, ok := find(spec.Scenarios, id, scenarioID); ok {
			return spec.Scenarios[i]
		}
	case interfaces.REQUIREMENT_KIND_QUESTIONS:
		if i, ok := find(spec.Questions, id, questionID); ok {
			return spec.Questions[i]
		}
	case interfaces.REQUIREMENT_KIND_RULES:
		if i, ok := find(spec.Rules, id, ruleID); ok {
			return spec.Rules[i]
		}
	}
	return nil
}

// specItemName is how an item is named to a person: its name, text or statement.
func specItemName(item any) string {
	switch v := item.(type) {
	case *interfaces.RequirementLogicNeed:
		return v.Name
	case *interfaces.RequirementScenario:
		return v.Name
	case *interfaces.RequirementQuestion:
		return v.Text
	case *interfaces.RequirementRule:
		return v.Statement
	}
	return ""
}

// fingerprint signs the items a proposal changes as they are in spec; a missing one signs as such.
func fingerprint(spec *interfaces.RequirementSpec, kind string, ids ...string) string {
	h := sha256.New()
	for _, id := range ids {
		if id == "" {
			continue
		}
		raw, _ := json.Marshal(specItem(spec, kind, id))
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cleanText trims a text and cuts it to limit characters.
func cleanText(s string, limit int) string {
	return truncateRunes(strings.TrimSpace(s), limit)
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// cleanItem keeps of a proposed item the fields its kind has, trimmed and cut to the lengths a
// person may write, with only the ids spec and the adopted model have; nil when nothing is left.
func cleanItem(kind string, item *interfaces.RequirementProposalItem, spec *interfaces.RequirementSpec,
	adopted map[string]bool) *interfaces.RequirementProposalItem {
	if item == nil {
		return nil
	}
	out := &interfaces.RequirementProposalItem{}
	ids := func(in []string, keep func(string) bool) []string {
		var kept []string
		for _, id := range in {
			if id = strings.TrimSpace(id); keep(id) && !slices.Contains(kept, id) {
				kept = append(kept, id)
			}
		}
		return kept
	}
	switch kind {
	case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
		if interfaces.IsLogicNeedKind(item.LogicKind) {
			out.LogicKind = item.LogicKind
		}
		out.Name, out.Definition = cleanText(item.Name, maxItemNameRunes), cleanText(item.Definition, maxItemTextRunes)
		out.ObjectTypeIDs = ids(item.ObjectTypeIDs, func(id string) bool { return adopted[id] })
	case interfaces.REQUIREMENT_KIND_SCENARIOS:
		out.Name, out.Actor = cleanText(item.Name, maxItemNameRunes), cleanText(item.Actor, maxItemNameRunes)
		out.Trigger, out.Decision = cleanText(item.Trigger, maxItemTextRunes), cleanText(item.Decision, maxItemTextRunes)
		for _, k := range item.Knowledge {
			if k = cleanText(k, maxItemTextRunes); k != "" {
				out.Knowledge = append(out.Knowledge, k)
			}
		}
	case interfaces.REQUIREMENT_KIND_QUESTIONS:
		out.Text = cleanText(item.Text, maxItemTextRunes)
		out.ScenarioIDs = ids(item.ScenarioIDs, func(id string) bool { return specItem(spec, interfaces.REQUIREMENT_KIND_SCENARIOS, id) != nil })
		out.LogicNeedIDs = ids(item.LogicNeedIDs, func(id string) bool {
			return specItem(spec, interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, id) != nil
		})
	case interfaces.REQUIREMENT_KIND_RULES:
		out.Statement = cleanText(item.Statement, maxItemTextRunes)
	}
	if editOf(kind, out) == (interfaces.RequirementItemEdit{}) {
		return nil
	}
	return out
}

// editOf is the edit a proposed item makes: the fields it sets, for its kind.
func editOf(kind string, item *interfaces.RequirementProposalItem) interfaces.RequirementItemEdit {
	edit := interfaces.RequirementItemEdit{}
	if item == nil {
		return edit
	}
	str := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	list := func(v []string) *[]string {
		if len(v) == 0 {
			return nil
		}
		return &v
	}
	switch kind {
	case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
		edit.LogicKind, edit.Name, edit.Definition = str(item.LogicKind), str(item.Name), str(item.Definition)
		edit.ObjectTypeIDs = list(item.ObjectTypeIDs)
	case interfaces.REQUIREMENT_KIND_SCENARIOS:
		edit.Name, edit.Actor, edit.Trigger, edit.Decision = str(item.Name), str(item.Actor), str(item.Trigger), str(item.Decision)
		edit.Knowledge = list(item.Knowledge)
	case interfaces.REQUIREMENT_KIND_QUESTIONS:
		edit.Text, edit.ScenarioIDs, edit.LogicNeedIDs = str(item.Text), list(item.ScenarioIDs), list(item.LogicNeedIDs)
	case interfaces.REQUIREMENT_KIND_RULES:
		edit.Statement = str(item.Statement)
	}
	return edit
}

// changes tells whether a proposed item changes the item it modifies.
func changes(kind string, item *interfaces.RequirementProposalItem, target any) bool {
	differs := func(proposed string, current string) bool { return proposed != "" && proposed != current }
	differList := func(proposed []string, current []string) bool {
		return len(proposed) > 0 && !slices.Equal(proposed, current)
	}
	switch t := target.(type) {
	case *interfaces.RequirementLogicNeed:
		return differs(item.LogicKind, t.Kind) || differs(item.Name, t.Name) || differs(item.Definition, t.Definition) ||
			differList(item.ObjectTypeIDs, t.ObjectTypeIDs)
	case *interfaces.RequirementScenario:
		return differs(item.Name, t.Name) || differs(item.Actor, t.Actor) || differs(item.Trigger, t.Trigger) ||
			differs(item.Decision, t.Decision) || differList(item.Knowledge, t.Knowledge)
	case *interfaces.RequirementQuestion:
		return differs(item.Text, t.Text) || differList(item.ScenarioIDs, t.ScenarioIDs) ||
			differList(item.LogicNeedIDs, t.LogicNeedIDs)
	case *interfaces.RequirementRule:
		return differs(item.Statement, t.Statement)
	}
	return false
}

// repeats tells whether an item to add repeats one spec has: a kinetic need of its kind and name,
// a scenario of its name, a question of its text or a rule of its statement.
func repeats(spec *interfaces.RequirementSpec, kind string, item *interfaces.RequirementProposalItem) bool {
	switch kind {
	case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
		return slices.ContainsFunc(spec.LogicNeeds, func(n *interfaces.RequirementLogicNeed) bool {
			return n.Kind == item.LogicKind && sameText(n.Name, item.Name)
		})
	case interfaces.REQUIREMENT_KIND_SCENARIOS:
		return slices.ContainsFunc(spec.Scenarios, func(s *interfaces.RequirementScenario) bool { return sameText(s.Name, item.Name) })
	case interfaces.REQUIREMENT_KIND_QUESTIONS:
		return slices.ContainsFunc(spec.Questions, func(q *interfaces.RequirementQuestion) bool { return sameText(q.Text, item.Text) })
	case interfaces.REQUIREMENT_KIND_RULES:
		return slices.ContainsFunc(spec.Rules, func(r *interfaces.RequirementRule) bool { return sameText(r.Statement, item.Statement) })
	}
	return false
}

// complete tells whether an item to add has what its kind needs to be added.
func complete(kind string, item *interfaces.RequirementProposalItem) bool {
	switch kind {
	case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
		return item.LogicKind != "" && item.Name != ""
	case interfaces.REQUIREMENT_KIND_SCENARIOS:
		return item.Name != ""
	case interfaces.REQUIREMENT_KIND_QUESTIONS:
		return item.Text != ""
	case interfaces.REQUIREMENT_KIND_RULES:
		return item.Statement != ""
	}
	return false
}

// checkProposals checks the proposals of an answer against spec as it was asked, and gives those
// that stand, as a person adopts them, with how many it left out: of a kind or change the page does
// not take, naming items spec does not have, changing nothing, repeating an item, or changing an
// item another proposal of the answer changes already.
func checkProposals(page string, proposed []agentProposal, spec *interfaces.RequirementSpec, draft *interfaces.Draft,
	items []*interfaces.LogicNetworkItem) ([]*interfaces.RequirementProposal, int) {
	adopted := map[string]bool{}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			adopted[ot.ID] = ot.Adopted
		}
	}
	kept, dropped := []*interfaces.RequirementProposal{}, 0
	touched := map[string]bool{}
	for _, p := range proposed {
		if len(kept) == askMaxProposals {
			dropped++
			continue
		}
		proposal := checkProposal(page, p, spec, adopted, items, touched)
		if proposal == nil {
			dropped++
			continue
		}
		kept = append(kept, proposal)
	}
	return kept, dropped
}

func checkProposal(page string, p agentProposal, spec *interfaces.RequirementSpec, adopted map[string]bool,
	items []*interfaces.LogicNetworkItem, touched map[string]bool) *interfaces.RequirementProposal {
	if !slices.Contains(askKinds[page][p.Kind], p.Op) {
		return nil
	}
	proposal := &interfaces.RequirementProposal{Op: p.Op, Kind: p.Kind, Reason: cleanText(p.Reason, askReasonMax)}
	target := specItem(spec, p.Kind, strings.TrimSpace(p.TargetID))
	if p.Op != interfaces.REQUIREMENT_PROPOSAL_ADD {
		if target == nil || touched[p.Kind+"/"+strings.TrimSpace(p.TargetID)] {
			return nil
		}
		proposal.TargetID, proposal.Before = strings.TrimSpace(p.TargetID), target
	}
	switch p.Op {
	case interfaces.REQUIREMENT_PROPOSAL_ADD:
		item := cleanItem(p.Kind, p.Item, spec, adopted)
		if item == nil || !complete(p.Kind, item) || repeats(spec, p.Kind, item) || touched[p.Kind+"+"+normalizedName(item)] {
			return nil
		}
		touched[p.Kind+"+"+normalizedName(item)] = true
		proposal.Item = item
	case interfaces.REQUIREMENT_PROPOSAL_MODIFY:
		item := cleanItem(p.Kind, p.Item, spec, adopted)
		if item == nil || !changes(p.Kind, item, target) {
			return nil
		}
		proposal.Item = item
	case interfaces.REQUIREMENT_PROPOSAL_MERGE:
		into := specItem(spec, p.Kind, strings.TrimSpace(p.IntoID))
		if into == nil || strings.TrimSpace(p.IntoID) == proposal.TargetID || touched[p.Kind+"/"+strings.TrimSpace(p.IntoID)] {
			return nil
		}
		proposal.IntoID, proposal.IntoBefore = strings.TrimSpace(p.IntoID), into
		if item := cleanItem(p.Kind, p.Item, spec, adopted); item != nil && changes(p.Kind, item, into) {
			proposal.Item = item
		}
		touched[p.Kind+"/"+proposal.IntoID] = true
	case interfaces.REQUIREMENT_PROPOSAL_DELETE:
	case interfaces.REQUIREMENT_PROPOSAL_LINK:
		need := target.(*interfaces.RequirementLogicNeed)
		item := networkItemOf(items, p.NetworkItemID)
		if item == nil || interfaces.LogicLinkTypeOf(need.Kind) != item.Type || item.Implements(need.Link) {
			return nil
		}
		proposal.NetworkItem = &interfaces.RequirementProposalNetwork{Type: item.Type, ID: item.ID, BoxID: item.BoxID,
			Name: item.Name, Kind: item.Kind}
	case interfaces.REQUIREMENT_PROPOSAL_SPLIT:
		if len(p.Parts) != 2 {
			return nil
		}
		for i := range p.Parts {
			part := cleanItem(p.Kind, &p.Parts[i], spec, adopted)
			if part == nil || part.Name == "" {
				return nil
			}
			proposal.Parts = append(proposal.Parts, *part)
		}
		if sameText(proposal.Parts[0].Name, proposal.Parts[1].Name) ||
			repeats(spec, p.Kind, &proposal.Parts[1]) {
			return nil
		}
		for _, m := range p.Moves {
			q := specItem(spec, interfaces.REQUIREMENT_KIND_QUESTIONS, strings.TrimSpace(m.QuestionID))
			if q == nil || m.Part < 0 || m.Part > 1 || !slices.Contains(q.(*interfaces.RequirementQuestion).ScenarioIDs, proposal.TargetID) {
				continue
			}
			proposal.Moves = append(proposal.Moves, interfaces.RequirementProposalMove{QuestionID: strings.TrimSpace(m.QuestionID), Part: m.Part})
		}
	}
	if proposal.TargetID != "" {
		touched[p.Kind+"/"+proposal.TargetID] = true
	}
	proposal.Fingerprint = fingerprint(spec, p.Kind, proposal.TargetID, proposal.IntoID)
	return proposal
}

// normalizedName is what tells two items to add apart: their name, text or statement.
func normalizedName(item *interfaces.RequirementProposalItem) string {
	return strings.ToLower(strings.TrimSpace(item.LogicKind + "/" + item.Name + item.Text + item.Statement))
}

// readyTurn is a turn as a reader sees it: one pending past the run deadline is failed.
func (s *requirementService) readyTurn(turn *interfaces.RequirementConversationTurn) {
	if turn.Status == interfaces.REQUIREMENT_TURN_PENDING && turn.CreateTime < s.staleRunBefore() {
		turn.Status, turn.Error = interfaces.REQUIREMENT_TURN_FAILED, failureInterrupted
	}
}

func (s *requirementService) readConversation(c *interfaces.RequirementConversation) *interfaces.RequirementConversation {
	for _, t := range c.Turns {
		s.readyTurn(t)
		if t.Refs == nil {
			t.Refs = []interfaces.RequirementAskRef{}
		}
		if t.Proposals == nil {
			t.Proposals = []*interfaces.RequirementProposal{}
		}
	}
	return c
}

func (s *requirementService) ListConversations(ctx context.Context, token string, projectID string,
	page string) ([]*interfaces.RequirementConversation, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListRequirementConversations")
	defer span.End()

	if askKinds[page] == nil {
		span.SetStatus(codes.Error, "Unknown page")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidRequestBody)
	}
	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	list, err := s.ra.ListConversations(ctx, projectID, page)
	if err != nil {
		span.SetStatus(codes.Error, "List conversations failed")
		return nil, internalError(ctx, "List conversations failed", err)
	}
	for _, c := range list {
		s.readConversation(c)
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

func (s *requirementService) GetConversation(ctx context.Context, token string, projectID string,
	conversationID string) (*interfaces.RequirementConversation, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRequirementConversation")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	c, err := s.conversation(ctx, projectID, conversationID)
	if err != nil {
		span.SetStatus(codes.Error, "Get conversation failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return c, nil
}

// conversation reads a conversation of the project as readers see it.
func (s *requirementService) conversation(ctx context.Context, projectID string,
	conversationID string) (*interfaces.RequirementConversation, error) {
	c, ok, err := s.ra.GetConversation(ctx, projectID, conversationID)
	if err != nil {
		return nil, internalError(ctx, "Get conversation failed", err)
	}
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_ConversationNotFound)
	}
	return s.readConversation(c), nil
}

func (s *requirementService) DeleteConversation(ctx context.Context, token string, projectID string,
	conversationID string, caller interfaces.AccountInfo) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteRequirementConversation")
	defer span.End()

	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return err
	}
	deleted, err := s.ra.DeleteConversation(ctx, projectID, conversationID)
	if err != nil {
		span.SetStatus(codes.Error, "Delete conversation failed")
		return internalError(ctx, "Delete conversation failed", err)
	}
	if !deleted {
		span.SetStatus(codes.Error, "Conversation not found")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_ConversationNotFound)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// invalidQuestion refuses a question with why.
func invalidQuestion(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidQuestion).
		WithErrorDetails(detail)
}

func (s *requirementService) Ask(ctx context.Context, token string, projectID string, conversationID string,
	req interfaces.RequirementAskRequest, caller interfaces.AccountInfo) (*interfaces.RequirementConversation, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskRequirement")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	question := strings.TrimSpace(req.Question)
	if question == "" || utf8.RuneCountInString(question) > interfaces.REQUIREMENT_QUESTION_MAX_LENGTH {
		span.SetStatus(codes.Error, "Invalid question")
		return nil, invalidQuestion(ctx, "question is empty or too long")
	}
	var c *interfaces.RequirementConversation
	if conversationID != "" {
		if c, err = s.conversation(ctx, projectID, conversationID); err != nil {
			span.SetStatus(codes.Error, "Get conversation failed")
			return nil, err
		}
		if n := len(c.Turns); n > 0 && c.Turns[n-1].Status == interfaces.REQUIREMENT_TURN_PENDING {
			span.SetStatus(codes.Error, "Turn pending")
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_AskPending)
		}
	} else if askKinds[req.Page] == nil {
		span.SetStatus(codes.Error, "Unknown page")
		return nil, invalidQuestion(ctx, "page "+req.Page+" is unknown")
	}
	page := req.Page
	if c != nil {
		page = c.Page
	}

	in := &askInputs{language: entry.Project.NamingLanguage, supplement: s.supplementOf(ctx, projectID)}
	stored, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	in.spec = emptySpec()
	if ok {
		in.spec = withLists(cloneSpec(&stored.Spec))
	}
	if len(req.Refs) > interfaces.REQUIREMENT_ASK_REFS_MAX {
		span.SetStatus(codes.Error, "Too many refs")
		return nil, invalidItem(ctx, "too many refs")
	}
	refs := []interfaces.RequirementAskRef{}
	for _, ref := range req.Refs {
		item := specItem(in.spec, ref.Kind, ref.ID)
		if askKinds[page][ref.Kind] == nil || item == nil {
			span.SetStatus(codes.Error, "Unknown ref")
			return nil, invalidItem(ctx, "ref "+ref.Kind+"/"+ref.ID+" is not in the list")
		}
		refs = append(refs, interfaces.RequirementAskRef{Kind: ref.Kind, ID: ref.ID, Name: specItemName(item)})
	}

	// What the turn reads is read now, as the caller.
	docs, err := s.ra.GetDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get documents failed")
		return nil, internalError(ctx, "Get documents failed", err)
	}
	in.docs = s.withBackground(ctx, token, entry, docs)
	if draft, found, err := s.da.GetDraft(ctx, projectID); err == nil && found {
		in.draft = draft
	}
	if items, err := s.kineticNetworkItems(ctx, token, entry.KNID); err == nil {
		in.items = items
	}

	now := s.now()
	history := []askHistoryTurn{}
	if c != nil {
		for _, t := range c.Turns {
			if t.Status == interfaces.REQUIREMENT_TURN_READY {
				history = append(history, askHistoryTurn{Question: t.Question, Answer: truncateRunes(t.Answer, askHistoryAnswerMax)})
			}
		}
		history = history[max(0, len(history)-askHistoryMax):]
	}
	turn := &interfaces.RequirementConversationTurn{ID: s.newID(), Question: question, Refs: refs,
		Status: interfaces.REQUIREMENT_TURN_PENDING, Proposals: []*interfaces.RequirementProposal{}, RunID: s.newID(),
		By: caller, CreateTime: now, UpdateTime: now}
	if c == nil {
		c = &interfaces.RequirementConversation{ID: s.newID(), ProjectID: projectID, Page: page,
			Title: truncateRunes(question, askTitleMax), Turns: []*interfaces.RequirementConversationTurn{turn},
			Creator: caller, CreateTime: now, UpdateTime: now}
		if err := s.ra.CreateConversation(ctx, c); err != nil {
			span.SetStatus(codes.Error, "Create conversation failed")
			return nil, internalError(ctx, "Create conversation failed", err)
		}
	} else {
		// The version moves on even within the millisecond it was read, so a concurrent ask cannot
		// write over the turn appended here.
		read := c.UpdateTime
		c.Turns, c.UpdateTime = append(c.Turns, turn), max(now, read+1)
		written, err := s.ra.UpdateConversation(ctx, c, read)
		if err != nil {
			span.SetStatus(codes.Error, "Update conversation failed")
			return nil, internalError(ctx, "Update conversation failed", err)
		}
		if !written {
			span.SetStatus(codes.Error, "Conversation changed")
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_AskPending)
		}
	}

	runCtx := context.WithoutCancel(ctx)
	conversation, turnID, runID := c.ID, turn.ID, turn.RunID
	s.run(func() {
		s.answer(runCtx, projectID, conversation, turnID, runID, page, question, refs, history, in, caller)
	})
	span.SetStatus(codes.Ok, "")
	return c, nil
}

// answer asks the Agent a turn's question in the background and records its answer, or why it has
// none, on the turn as the run that asked it.
func (s *requirementService) answer(ctx context.Context, projectID string, conversationID string, turnID string,
	runID string, page string, question string, refs []interfaces.RequirementAskRef, history []askHistoryTurn,
	in *askInputs, caller interfaces.AccountInfo) {
	var taskID string
	finish := func(t *interfaces.RequirementConversationTurn) {}
	message, err := buildAskMessage(page, question, refs, history, in)
	failure := ""
	if err != nil {
		failure = failureAgentUnavailable
	} else {
		var out any
		var retried bool
		out, retried, failure, err = s.askTwice(ctx, projectID, caller, message, askResponseFormat(),
			func() any { return &askOutput{} }, &taskID)
		if failure == "" {
			answer := out.(*askOutput)
			proposals, dropped := checkProposals(page, answer.Proposals, in.spec, in.draft, in.items)
			text := cleanText(answer.Answer, askAnswerMax)
			finish = func(t *interfaces.RequirementConversationTurn) {
				t.Status, t.Answer, t.Proposals, t.Dropped = interfaces.REQUIREMENT_TURN_READY, text, proposals, dropped
			}
		} else {
			logger.Errorf("Ask AI of project %s failed (%s, asked again: %t): %v", projectID, failure, retried, err)
		}
	}
	if failure != "" {
		finish = func(t *interfaces.RequirementConversationTurn) {
			t.Status, t.Error = interfaces.REQUIREMENT_TURN_FAILED, failure
		}
	}
	if err := s.writeTurn(ctx, projectID, conversationID, turnID, func(t *interfaces.RequirementConversationTurn) bool {
		if t.RunID != runID || t.Status != interfaces.REQUIREMENT_TURN_PENDING {
			return false
		}
		finish(t)
		return true
	}); err != nil {
		logger.Errorf("Record Ask AI answer of project %s failed: %v", projectID, err)
	}
}

// writeTurn applies change to a turn of a conversation and writes it, reading it again when it
// changed meanwhile; change returns false to write nothing. A conversation deleted meanwhile is
// left gone.
func (s *requirementService) writeTurn(ctx context.Context, projectID string, conversationID string, turnID string,
	change func(t *interfaces.RequirementConversationTurn) bool) error {
	for range askWriteTries {
		c, ok, err := s.ra.GetConversation(ctx, projectID, conversationID)
		if err != nil || !ok {
			return err
		}
		i := slices.IndexFunc(c.Turns, func(t *interfaces.RequirementConversationTurn) bool { return t.ID == turnID })
		if i < 0 || !change(c.Turns[i]) {
			return nil
		}
		read := c.UpdateTime
		c.UpdateTime = max(s.now(), read+1)
		c.Turns[i].UpdateTime = c.UpdateTime
		written, err := s.ra.UpdateConversation(ctx, c, read)
		if err != nil || written {
			return err
		}
	}
	return errConversationBusy
}
