// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Ask AI on a relation type not made yet: the form may have one end, both without a mapping, or
// nothing. The Agent reads the relation as the form has it and the object types it may join: the
// ends given, the other ends of the candidates the data suggests for the first end, then the other
// adopted object types, briefly. Its revisions are checked as the relation's own ask checks them, on
// a copy, and given also when it writes no answer; nothing is written.

// askEvidenceTimeout bounds reading the candidates before asking, so the answer stays within the
// deadline a gateway gives a request that is not streamed.
const askEvidenceTimeout = 10 * time.Second

func (s *draftService) AskNewRelationType(ctx context.Context, token string, projectID string,
	req interfaces.DraftRelationAskRequest, caller interfaces.AccountInfo, stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AskNewRelationType")
	defer span.End()

	draft, in, err := s.readyToAsk(ctx, token, projectID, req.Question, req.History)
	if err != nil {
		span.SetStatus(codes.Error, "Not askable")
		return nil, err
	}
	items := &draft.Items
	base := s.workingRelationType(ctx, items, &interfaces.DraftRelationType{}, req.Working, caller)
	// Ends the form has with no join the save would take yet.
	if req.Working != nil && base.Source == "" && base.Target == "" {
		for _, end := range []struct {
			id string
			to *string
		}{{req.Working.SourceObjectTypeID, &base.Source}, {req.Working.TargetObjectTypeID, &base.Target}} {
			if findObjectType(items, end.id) != nil {
				*end.to = end.id
			}
		}
	}
	in.Kind = askKindRelationType
	asked := askRelationOf(base)
	in.RelationType, in.Ends = &asked, []askObjectType{}
	offer := func(ot *interfaces.DraftObjectType, brief bool) {
		if ot != nil && !slices.ContainsFunc(in.Ends, func(e askObjectType) bool { return e.ID == ot.ID }) {
			end := askObjectTypeOf(ot)
			end.brief = brief
			in.Ends = append(in.Ends, end)
		}
	}
	given := []string{}
	for _, id := range []string{base.Source, base.Target} {
		if id != "" && !slices.Contains(given, id) {
			given = append(given, id)
			offer(findObjectType(items, id), false)
		}
	}
	if len(given) > 0 {
		in.Candidates = s.askCandidates(ctx, draft, given, caller)
		for _, c := range in.Candidates {
			offer(findObjectType(items, c.Source), false)
			offer(findObjectType(items, c.Target), false)
		}
	}
	if len(given) < 2 {
		for _, ot := range items.ObjectTypes {
			if ot.Adopted {
				offer(ot, true)
			}
		}
	}
	in.Editable = []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT,
		interfaces.DRAFT_ASK_REVISION_ENDS}

	answer, err := s.ask(ctx, caller, askTurn{input: in, finish: func(out *askOutput) *interfaces.DraftAskAnswer {
		return checkRevisions(out.Revisions, in.Editable, func(r askRevisionOutput) (*interfaces.DraftAskRevision, string) {
			return s.relationTypeRevision(ctx, items, base, r, caller)
		})
	}, answerOptional: true}, stream)
	if err != nil {
		span.SetStatus(codes.Error, "Ask AI failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return answer, nil
}

// askCandidates are the candidates of the first end given, adopted, that join the ends given: with
// both ends, those between them. Without them the Agent is asked all the same.
func (s *draftService) askCandidates(ctx context.Context, draft *interfaces.Draft, given []string,
	caller interfaces.AccountInfo) []askCandidateLine {
	first := findObjectType(&draft.Items, given[0])
	if first == nil || !first.Adopted {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, askEvidenceTimeout)
	defer cancel()
	candidates, err := s.relationCandidates(ctx, draft, first, caller)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Ask AI on a new relation read no candidates: %v", err))
		return nil
	}
	lines := []askCandidateLine{}
	for _, c := range candidates {
		if len(given) == 2 && !(slices.Contains(given, c.SourceObjectTypeID) && slices.Contains(given, c.TargetObjectTypeID)) {
			continue
		}
		if len(lines) == askMaxCandidates {
			break
		}
		lines = append(lines, askCandidateLineOf(len(lines), c))
	}
	return lines
}
