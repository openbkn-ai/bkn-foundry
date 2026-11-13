// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// Adopting a proposal of Ask AI writes it into the specification as the item edits do, checked by
// their rules, and makes what it adds or changes accepted: running again keeps it. An item it adds
// records Ask AI as its source. A proposal whose items changed since it was asked is refused; it is
// asked again.

// errConversationBusy is a conversation that went on changing while a turn was written.
var errConversationBusy = errors.New("conversation changed while writing")

// replaceID replaces from by into in ids, once.
func replaceID(ids []string, from string, into string) []string {
	out := []string{}
	for _, id := range ids {
		if id == from {
			id = into
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// markAsked records Ask AI as the source of the item of kind with the id, and makes it accepted.
func markAsked(spec *interfaces.RequirementSpec, kind string, id string) {
	switch item := specItem(spec, kind, id).(type) {
	case *interfaces.RequirementLogicNeed:
		item.Source, item.Accepted = interfaces.REQUIREMENT_SOURCE_ASK, true
	case *interfaces.RequirementScenario:
		item.Source, item.Accepted = interfaces.REQUIREMENT_SOURCE_ASK, true
	case *interfaces.RequirementQuestion:
		item.Source, item.Accepted = interfaces.REQUIREMENT_SOURCE_ASK, true
	case *interfaces.RequirementRule:
		item.Source = interfaces.REQUIREMENT_SOURCE_ASK
	}
}

// accept makes an item accepted, as a person who keeps it does.
func accept(spec *interfaces.RequirementSpec, kind string, id string) {
	switch item := specItem(spec, kind, id).(type) {
	case *interfaces.RequirementLogicNeed:
		item.Accepted = true
	case *interfaces.RequirementScenario:
		item.Accepted = true
	case *interfaces.RequirementQuestion:
		item.Accepted = true
	}
}

// adoptChange is the change adopting p makes; result takes the id of the item it adds.
func (s *requirementService) adoptChange(ctx context.Context, projectID string, p *interfaces.RequirementProposal,
	caller interfaces.AccountInfo, result *string) change {
	return func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
		switch p.Op {
		case interfaces.REQUIREMENT_PROPOSAL_ADD:
			if err := s.addChange(ctx, projectID, p.Kind, editOf(p.Kind, p.Item), result)(spec, drafted); err != nil {
				return err
			}
			markAsked(spec, p.Kind, *result)
		case interfaces.REQUIREMENT_PROPOSAL_MODIFY:
			return s.editChange(ctx, projectID, p.Kind, p.TargetID, editOf(p.Kind, p.Item))(spec, drafted)
		case interfaces.REQUIREMENT_PROPOSAL_DELETE:
			return deleteChange(ctx, p.Kind, p.TargetID)(spec, drafted)
		case interfaces.REQUIREMENT_PROPOSAL_MERGE:
			if p.Item != nil {
				if err := s.editChange(ctx, projectID, p.Kind, p.IntoID, editOf(p.Kind, p.Item))(spec, drafted); err != nil {
					return err
				}
			}
			accept(spec, p.Kind, p.IntoID)
			// What rests on or belongs to the one merged away now does on the one kept.
			for _, q := range spec.Questions {
				switch p.Kind {
				case interfaces.REQUIREMENT_KIND_LOGIC_NEEDS:
					q.LogicNeedIDs = replaceID(q.LogicNeedIDs, p.TargetID, p.IntoID)
				case interfaces.REQUIREMENT_KIND_SCENARIOS:
					q.ScenarioIDs = replaceID(q.ScenarioIDs, p.TargetID, p.IntoID)
				}
			}
			for _, n := range spec.LogicNeeds {
				if p.Kind == interfaces.REQUIREMENT_KIND_LOGIC_NEEDS && n.ExecutedBy == p.TargetID {
					n.ExecutedBy = p.IntoID
				}
			}
			if err := deleteChange(ctx, p.Kind, p.TargetID)(spec, drafted); err != nil {
				return err
			}
			needScenarios(spec)
		case interfaces.REQUIREMENT_PROPOSAL_LINK:
			n, ok := specItem(spec, p.Kind, p.TargetID).(*interfaces.RequirementLogicNeed)
			if !ok || p.NetworkItem == nil {
				return notFound(ctx)
			}
			n.Link = &interfaces.RequirementLogicLink{Type: p.NetworkItem.Type, ID: p.NetworkItem.ID,
				BoxID: p.NetworkItem.BoxID, By: caller, At: s.now()}
			n.Accepted = true
		case interfaces.REQUIREMENT_PROPOSAL_SPLIT:
			if len(p.Parts) != 2 {
				return invalidItem(ctx, "a split has two parts")
			}
			if err := s.editChange(ctx, projectID, p.Kind, p.TargetID, editOf(p.Kind, &p.Parts[0]))(spec, drafted); err != nil {
				return err
			}
			if err := s.addChange(ctx, projectID, p.Kind, editOf(p.Kind, &p.Parts[1]), result)(spec, drafted); err != nil {
				return err
			}
			markAsked(spec, p.Kind, *result)
			for _, m := range p.Moves {
				if q, ok := specItem(spec, interfaces.REQUIREMENT_KIND_QUESTIONS, m.QuestionID).(*interfaces.RequirementQuestion); ok && m.Part == 1 {
					q.ScenarioIDs = replaceID(q.ScenarioIDs, p.TargetID, *result)
				}
			}
			needScenarios(spec)
		default:
			return invalidItem(ctx, "op "+p.Op+" is unknown")
		}
		return nil
	}
}

func (s *requirementService) DecideProposals(ctx context.Context, token string, projectID string, conversationID string,
	turnID string, decision interfaces.RequirementProposalsDecision,
	caller interfaces.AccountInfo) (*interfaces.RequirementProposalsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DecideRequirementProposals")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	c, err := s.conversation(ctx, projectID, conversationID)
	if err != nil {
		span.SetStatus(codes.Error, "Get conversation failed")
		return nil, err
	}
	i := slices.IndexFunc(c.Turns, func(t *interfaces.RequirementConversationTurn) bool { return t.ID == turnID })
	if i < 0 || c.Turns[i].Status != interfaces.REQUIREMENT_TURN_READY {
		span.SetStatus(codes.Error, "Turn not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_ConversationNotFound)
	}
	proposals := c.Turns[i].Proposals
	seen := map[int]bool{}
	for _, index := range append(slices.Clone(decision.Adopt), decision.Decline...) {
		if index < 0 || index >= len(proposals) {
			span.SetStatus(codes.Error, "Unknown proposal")
			return nil, invalidItem(ctx, "no such proposal")
		}
		if seen[index] || proposals[index].Status != "" {
			span.SetStatus(codes.Error, "Proposal decided")
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_ProposalDecided)
		}
		seen[index] = true
	}
	if len(seen) == 0 {
		span.SetStatus(codes.Error, "Nothing decided")
		return nil, invalidItem(ctx, "no proposal is adopted or declined")
	}

	var req *interfaces.Requirement
	results := map[int]string{}
	if len(decision.Adopt) > 0 {
		// A link is to an item the live network has and the caller can read, as linking by hand.
		for _, index := range decision.Adopt {
			p := proposals[index]
			if p.Op != interfaces.REQUIREMENT_PROPOSAL_LINK || p.NetworkItem == nil {
				continue
			}
			network, err := logics.LiveNetwork(ctx, s.rla, entry.KNID)
			if err != nil {
				span.SetStatus(codes.Error, "Get live network failed")
				return nil, internalError(ctx, "Get live network failed", err)
			}
			kind := ""
			if before, ok := p.Before.(map[string]any); ok {
				kind, _ = before["kind"].(string)
			}
			target := &interfaces.RequirementLogicLink{Type: p.NetworkItem.Type, ID: p.NetworkItem.ID, BoxID: p.NetworkItem.BoxID}
			if impl := s.liveReader(ctx, token, network).read(target, kind); !impl.Available {
				span.SetStatus(codes.Error, "Implementation not readable")
				if impl.Reason == interfaces.LOGIC_IMPLEMENTATION_UNAVAILABLE {
					return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Requirement_ImplementationUnavailable)
				}
				return nil, invalidItem(ctx, p.NetworkItem.Type+" "+p.NetworkItem.ID+" is "+impl.Reason+" in the live network")
			}
		}
		req, err = s.update(ctx, token, projectID, decision.UpdateTime, caller,
			func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
				changed := []int{}
				for _, index := range decision.Adopt {
					p := proposals[index]
					// What an add or split adds being in the list already, it was adopted, or added since.
					added := p.Item
					if p.Op == interfaces.REQUIREMENT_PROPOSAL_SPLIT && len(p.Parts) == 2 {
						added = &p.Parts[1]
					}
					if fingerprint(spec, p.Kind, p.TargetID, p.IntoID) != p.Fingerprint ||
						(p.Op == interfaces.REQUIREMENT_PROPOSAL_ADD || p.Op == interfaces.REQUIREMENT_PROPOSAL_SPLIT) &&
							added != nil && repeats(spec, p.Kind, added) {
						changed = append(changed, index)
					}
				}
				if len(changed) > 0 {
					return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_ProposalChanged).
						WithErrorDetails(map[string]any{"indexes": changed})
				}
				for _, index := range decision.Adopt {
					var result string
					if err := s.adoptChange(ctx, projectID, proposals[index], caller, &result)(spec, drafted); err != nil {
						return err
					}
					results[index] = result
				}
				return nil
			})
		if err != nil {
			span.SetStatus(codes.Error, "Adopt failed")
			return nil, err
		}
	}
	if err := s.writeTurn(ctx, projectID, conversationID, turnID, func(t *interfaces.RequirementConversationTurn) bool {
		for _, index := range decision.Adopt {
			if index < len(t.Proposals) {
				t.Proposals[index].Status, t.Proposals[index].ResultID = interfaces.REQUIREMENT_PROPOSAL_ADOPTED, results[index]
			}
		}
		for _, index := range decision.Decline {
			if index < len(t.Proposals) {
				t.Proposals[index].Status = interfaces.REQUIREMENT_PROPOSAL_DECLINED
			}
		}
		return true
	}); err != nil {
		span.SetStatus(codes.Error, "Record decision failed")
		return nil, internalError(ctx, "Record decision failed", err)
	}
	c, err = s.conversation(ctx, projectID, conversationID)
	if err != nil {
		span.SetStatus(codes.Error, "Get conversation failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.RequirementProposalsResult{Conversation: c, Requirement: req}, nil
}
