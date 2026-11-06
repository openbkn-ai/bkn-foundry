// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// documentAskObject verifies the working form against current native debug
// text before any Agent call. Only existing located examples enter its context;
// the complete debug corpus and acceptance files are never forwarded here.
func (s service) documentAskObject(ctx context.Context, token string, draft *interfaces.Draft, original, working *interfaces.DraftObjectType) (*interfaces.DraftObjectType, map[string][]string, error) {
	if original.Document == nil || strings.HasPrefix(original.Document.Role, "evidence_") {
		return working, nil, nil
	}
	input, err := s.documentEditInput(ctx, token, draft, original.Document)
	if err != nil {
		return nil, nil, err
	}
	checked, err := kbconcept.ValidateEditedObject(input, draft.Items, original, working)
	if err != nil {
		return nil, nil, documentValidationError(ctx, err)
	}
	asked := map[string][]string{}
	remaining, examples := 4000, 0
	for _, p := range checked.Properties {
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
				asked[p.Name] = append(asked[p.Name], string(raw[from:to]))
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
func RelationRevision(ctx context.Context, s projecthooks.Host, input *kbconcept.SampleInput, items interfaces.DraftItems, base *interfaces.DraftRelationType, out projecthooks.AskRevisionOutput) (*interfaces.DraftAskRevision, string) {
	if out.Field == interfaces.DRAFT_ASK_REVISION_NAME || out.Field == interfaces.DRAFT_ASK_REVISION_COMMENT {
		return s.NameOrComment(ctx, out.Field, out.Value, base.Name, base.Description)
	}
	if out.Field != interfaces.DRAFT_ASK_REVISION_ENDS || base.Document == nil || base.Document.Role != "relation" || out.DocumentMapping == nil || len(out.Mapping) > 0 {
		return nil, projecthooks.AskDroppedNotEditable
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
		return nil, projecthooks.AskDroppedInvalid
	}
	if s.SameJoin(s.RelationSnapshot(base), s.RelationSnapshot(checked)) {
		return nil, projecthooks.AskDroppedUnchanged
	}
	return &interfaces.DraftAskRevision{Value: interfaces.DraftAskEndsRevision{SourceObjectTypeID: checked.Source, TargetObjectTypeID: checked.Target, Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []interfaces.DraftRelationMapping{}, DocumentMapping: checked.DocumentMapping, Cardinality: checked.Cardinality}}, ""
}

// newDocumentRelation is the relation a form not saved yet would make, with what checks its revisions.
func (s service) newDocumentRelation(ctx context.Context, token string, draft *interfaces.Draft, req interfaces.DraftRelationAskRequest) (*interfaces.DraftRelationType, projecthooks.RelationReviser, error) {
	working := req.Working
	source := projecthooks.FindObjectType(&draft.Items, working.SourceObjectTypeID)
	if source == nil || source.Document == nil || strings.HasPrefix(source.Document.Role, "evidence_") || (working.DocumentMapping.Mode != "linking" && working.DocumentMapping.Mode != "same_document") {
		return nil, nil, s.InvalidRelation(ctx, nil)
	}
	if working.TargetObjectTypeID != "" && projecthooks.FindObjectType(&draft.Items, working.TargetObjectTypeID) == nil {
		return nil, nil, s.InvalidRelation(ctx, nil)
	}
	input, err := s.documentEditInput(ctx, token, draft, source.Document)
	if err != nil {
		return nil, nil, err
	}
	id := "new_document_relation"
	for n := 2; projecthooks.FindRelationType(&draft.Items, id) != nil; n++ {
		id = fmt.Sprintf("new_document_relation_%d", n)
	}
	name := strings.TrimSpace(working.Name)
	if name == "" {
		name, _ = i18n.Render(i18n.Language(projecthooks.DraftLanguage(draft)), "BknProject.Draft.DocumentRelation.NewName", nil)
	}
	reason, _ := i18n.Render(i18n.Language(projecthooks.DraftLanguage(draft)), "BknProject.Draft.DocumentRelation.ManualReason", nil)
	mapping := *working.DocumentMapping
	base := &interfaces.DraftRelationType{ID: id, Name: name, Description: strings.TrimSpace(working.Comment), Source: source.ID, Target: working.TargetObjectTypeID, Cardinality: working.Cardinality, DocumentMapping: &mapping,
		Document: &interfaces.DraftDocumentModel{Role: "relation", Reason: reason, Citations: append([]interfaces.DraftSourceCitation(nil), source.Document.Citations...), SelectionID: source.Document.SelectionID, SelectionRevision: source.Document.SelectionRevision}}
	if base.Cardinality == "" {
		base.Cardinality = interfaces.DRAFT_CARDINALITY_MANY_TO_ONE
	}
	return base, func(r projecthooks.AskRevisionOutput) (*interfaces.DraftAskRevision, string) {
		return RelationRevision(ctx, s.Host, input, draft.Items, base, r)
	}, nil
}
