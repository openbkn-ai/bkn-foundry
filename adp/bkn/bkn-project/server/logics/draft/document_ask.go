// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// documentAskObject verifies the working form against current native debug
// text before any Agent call. Only existing located examples enter its context;
// the complete debug corpus and acceptance files are never forwarded here.
func (s *draftService) documentAskObject(ctx context.Context, token string, draft *interfaces.Draft, original, working *interfaces.DraftObjectType) (*interfaces.DraftObjectType, askObjectType, error) {
	if original.Document == nil || strings.HasPrefix(original.Document.Role, "evidence_") {
		return working, askObjectTypeOf(working), nil
	}
	input, err := s.documentEditInput(ctx, token, draft, original.Document)
	if err != nil {
		return nil, askObjectType{}, err
	}
	checked, err := kbconcept.ValidateEditedObject(input, draft.Items, original, working)
	if err != nil {
		return nil, askObjectType{}, documentValidationError(ctx, err)
	}
	asked := askObjectTypeOf(checked)
	remaining, examples := 4000, 0
	for i, p := range checked.Properties {
		if p.Extraction == nil || p.ReadOnly {
			continue
		}
		for _, q := range p.Extraction.Examples {
			if remaining == 0 || examples >= 10 {
				break
			}
			for _, sample := range input.Request.Samples {
				proof := sample.Citation
				if proof.KnowledgeBaseID != q.KnowledgeBaseID || proof.FileID != q.FileID || proof.ContentVersionID != q.ContentVersionID || proof.BlockID != q.BlockID || q.Start < proof.Start || q.End > proof.End {
					continue
				}
				raw := []rune(proof.Quote)
				from, to := q.Start-proof.Start, q.End-proof.Start
				if from < 0 || to > len(raw) || to <= from || to-from > remaining {
					continue
				}
				asked.Properties[i].Examples = append(asked.Properties[i].Examples, string(raw[from:to]))
				remaining -= to - from
				examples++
				break
			}
		}
	}
	return checked, asked, nil
}

// Document joins are checked against native proof and existing schema, without
// treating a mention column as a SQL table mapping or inventing hit counts.
func documentRelationRevision(ctx context.Context, input *kbconcept.SampleInput, items interfaces.DraftItems, base *interfaces.DraftRelationType, out askRevisionOutput) (*interfaces.DraftAskRevision, string) {
	if out.Field == interfaces.DRAFT_ASK_REVISION_NAME || out.Field == interfaces.DRAFT_ASK_REVISION_COMMENT {
		return nameOrComment(ctx, out.Field, out.Value, current(out.Field, base.Name, base.Description))
	}
	if out.Field != interfaces.DRAFT_ASK_REVISION_ENDS || base.Document == nil || base.Document.Role != "relation" || out.DocumentMapping == nil || len(out.Mapping) > 0 {
		return nil, askDroppedNotEditable
	}
	candidate := *base
	mapping := *out.DocumentMapping
	candidate.DocumentMapping = &mapping
	if out.Source != "" {
		candidate.Source = out.Source
	}
	if out.Target != "" {
		candidate.Target = out.Target
	}
	if out.Cardinality != "" {
		candidate.Cardinality = out.Cardinality
	}
	checked, err := kbconcept.ValidateEditedRelation(input, items, base, &candidate)
	if err != nil {
		return nil, askDroppedInvalid
	}
	if sameJoin(relationSnapshot(base), relationSnapshot(checked)) {
		return nil, askDroppedUnchanged
	}
	return &interfaces.DraftAskRevision{Value: interfaces.DraftAskEndsRevision{SourceObjectTypeID: checked.Source, TargetObjectTypeID: checked.Target, Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []interfaces.DraftRelationMapping{}, DocumentMapping: checked.DocumentMapping, Cardinality: checked.Cardinality}}, ""
}

func (s *draftService) askNewDocumentRelation(ctx context.Context, token string, draft *interfaces.Draft, in askInput, req interfaces.DraftRelationAskRequest, caller interfaces.AccountInfo, stream *interfaces.DraftAskStream) (*interfaces.DraftAskAnswer, error) {
	working := req.Working
	source := findObjectType(&draft.Items, working.SourceObjectTypeID)
	if source == nil || source.Document == nil || strings.HasPrefix(source.Document.Role, "evidence_") || (working.DocumentMapping.Mode != "linking" && working.DocumentMapping.Mode != "same_document") {
		return nil, invalidRelation(ctx, nil)
	}
	if working.TargetObjectTypeID != "" && findObjectType(&draft.Items, working.TargetObjectTypeID) == nil {
		return nil, invalidRelation(ctx, nil)
	}
	input, err := s.documentEditInput(ctx, token, draft, source.Document)
	if err != nil {
		return nil, err
	}
	id := "new_document_relation"
	for n := 2; findRelationType(&draft.Items, id) != nil; n++ {
		id = fmt.Sprintf("new_document_relation_%d", n)
	}
	name := strings.TrimSpace(working.Name)
	if name == "" {
		name, _ = i18n.Render(i18n.Language(draftLanguage(draft)), "BknProject.Draft.DocumentRelation.NewName", nil)
	}
	reason, _ := i18n.Render(i18n.Language(draftLanguage(draft)), "BknProject.Draft.DocumentRelation.ManualReason", nil)
	mapping := *working.DocumentMapping
	base := &interfaces.DraftRelationType{ID: id, Name: name, Description: strings.TrimSpace(working.Comment), Source: source.ID, Target: working.TargetObjectTypeID, Cardinality: working.Cardinality, DocumentMapping: &mapping,
		Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: reason, Citations: append([]interfaces.DraftSourceCitation(nil), source.Document.Citations...), SelectionID: source.Document.SelectionID, SelectionRevision: source.Document.SelectionRevision}}
	if base.Cardinality == "" {
		base.Cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
	}
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
			return documentRelationRevision(ctx, input, draft.Items, base, r)
		})
	}}, stream)
}
