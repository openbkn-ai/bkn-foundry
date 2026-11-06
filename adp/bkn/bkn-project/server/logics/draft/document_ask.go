// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// documentAskObject has the hooks check the working form before any Agent call, and adds the examples
// they verified to what the Agent reads.
func (s *draftService) documentAskObject(ctx context.Context, token string, draft *interfaces.Draft, original, working *interfaces.DraftObjectType) (*interfaces.DraftObjectType, askObjectType, []string, error) {
	checked, err := documentHooks().AskObject(ctx, draftHost{s}, token, draft, original, working)
	if err != nil {
		return nil, askObjectType{}, nil, err
	}
	asked := askObjectTypeOf(checked.Working)
	for i := range asked.Properties {
		asked.Properties[i].Examples = checked.Examples[asked.Properties[i].Name]
	}
	return checked.Working, asked, checked.Editable, nil
}

// askNewDocumentRelation asks about a relation the hooks shape from a form not saved yet.
func (s *draftService) askNewDocumentRelation(ctx context.Context, token string, draft *interfaces.Draft, in askInput, req interfaces.DraftRelationAskRequest, caller interfaces.AccountInfo, stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
	asking, err := documentHooks().AskNewRelation(ctx, draftHost{s}, token, draft, req)
	if err != nil {
		return nil, err
	}
	base, reviser := asking.Working, asking.Revise
	in.Kind = askKindRelationType
	asked := askRelationOf(base)
	asked.ID = ""
	in.RelationType = &asked
	offered := map[string]bool{}
	others := 0
	for _, object := range draft.Items.ObjectTypes {
		chosen := object.ID == base.Source || object.ID == base.Target
		if !chosen && (!object.Adopted || others >= 20) {
			continue
		}
		end := askObjectTypeOf(object)
		end.brief = !chosen
		in.Ends = append(in.Ends, end)
		offered[object.ID] = true
		if !chosen {
			others++
		}
	}
	for _, end := range []struct{ id, role string }{{base.Source, askChosenSource}, {base.Target, askChosenTarget}} {
		if object := findObjectType(&draft.Items, end.id); object != nil {
			in.ChosenEnds = append(in.ChosenEnds, askChosenEnd{ID: object.ID, Name: object.Name, Role: end.role})
		}
	}
	in.Editable = []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_COMMENT, interfaces.DRAFT_ASK_REVISION_ENDS}
	return s.ask(ctx, caller, askTurn{input: in, answerOptional: true, finish: func(out *askOutput) *interfaces.DraftAskAnswer {
		return checkRevisions(out.Revisions, in.Editable, func(r askRevisionOutput) (*interfaces.DraftAskRevision, string) {
			if r.Field == interfaces.DRAFT_ASK_REVISION_ENDS {
				if !keepsChosenEnds(base, r) {
					return nil, askDroppedNotFromGivenEnd
				}
				source, target := r.Source, r.Target
				if source == "" {
					source = base.Source
				}
				if target == "" {
					target = base.Target
				}
				if !offered[source] || !offered[target] {
					return nil, askDroppedNotEditable
				}
			}
			return reviser(r)
		})
	}}, stream)
}
