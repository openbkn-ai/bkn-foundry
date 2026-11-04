// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func associationAtom(relation, document, source string, target any) (assessmentAtom, error) {
	if relation == "" || document == "" || source == "" {
		return assessmentAtom{}, ErrInvalidBuild
	}
	value, err := businessJoinKey(target)
	if err != nil {
		return assessmentAtom{}, err
	}
	return assessmentAtom{Kind: "link", Key: relation + "/" + document + "/" + source, Value: value}, nil
}

// Human-confirmed source mentions determine eligible associations independently
// of how many predicted mentions the linker happened to match.
func expectedAssociationAtoms(draft *interfaces.Draft, writes []kb.DatasetWrite) ([]assessmentAtom, error) {
	result := []assessmentAtom{}
	for _, write := range writes {
		var evidence *interfaces.DraftRelationType
		for _, r := range draft.Items.RelationTypes {
			if r != nil && r.ID == write.Spec.RelationTypeID {
				evidence = r
				break
			}
		}
		if evidence == nil || !evidence.Adopted || evidence.Document == nil {
			continue
		}
		document, _ := write.Row["document_id"].(string)
		source, _ := write.Row["source_record_id"].(string)
		if evidence.Document.Role == "relation" && evidence.DocumentMapping != nil && evidence.DocumentMapping.Mode == "same_document" {
			atom, err := associationAtom(evidence.ID, document, source, write.Row["target_record_id"])
			if err != nil {
				return nil, err
			}
			result = append(result, atom)
			continue
		}
		if evidence.Document.Role != "evidence_reference" {
			continue
		}
		targetIsDocument := false
		for _, o := range draft.Items.ObjectTypes {
			if o != nil && o.ID == evidence.Target && o.Document != nil && o.Document.Role == "evidence_document" {
				targetIsDocument = true
			}
		}
		if !targetIsDocument || write.Row["claim_value"] == nil {
			continue
		}
		property, _ := write.Row["property_name"].(string)
		for _, relation := range draft.Items.RelationTypes {
			if relation == nil || !relation.Adopted || relation.Document == nil || relation.Document.Role != "relation" || relation.Source != evidence.Source || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" || relation.DocumentMapping.MentionProperty != property {
				continue
			}
			atom, err := associationAtom(relation.ID, document, source, write.Row["claim_value"])
			if err != nil {
				return nil, err
			}
			result = append(result, atom)
		}
	}
	return result, nil
}

// Only verified, acknowledged association rows count as actual links. Planned
// match coverage, source mentions and unresolved targets are never substitutes.
func resolvedAssociationAtoms(draft *interfaces.Draft, writes []kb.DatasetWrite) ([]assessmentAtom, error) {
	result := []assessmentAtom{}
	for _, write := range writes {
		for _, relation := range draft.Items.RelationTypes {
			if relation == nil || !relation.Adopted || relation.ID != write.Spec.RelationTypeID || relation.Document == nil || relation.Document.Role != "relation" || relation.DocumentMapping == nil {
				continue
			}
			mapping := relation.DocumentMapping
			if mapping.Mode != "linking" && mapping.Mode != "same_document" {
				continue
			}
			if _, err := businessJoinKey(write.Row["target_record_id"]); err != nil {
				return nil, err
			}
			value := write.Row["target_record_id"]
			if mapping.Mode == "linking" {
				identity, ok := write.Row["target_identity"].(map[string]any)
				if !ok {
					return nil, ErrInvalidBuild
				}
				value = identity[mapping.IdentityProperty]
			}
			document, _ := write.Row["document_id"].(string)
			source, _ := write.Row["source_record_id"].(string)
			atom, err := associationAtom(relation.ID, document, source, value)
			if err != nil {
				return nil, err
			}
			result = append(result, atom)
		}
	}
	return result, nil
}

func assessLinkWrites(draft *interfaces.Draft, expected, actual []kb.DatasetWrite, previous *AcceptanceCounts) (*AcceptanceCounts, error) {
	if previous == nil {
		return nil, ErrAcceptanceRequired
	}
	gold, err := expectedAssociationAtoms(draft, expected)
	if err != nil {
		return nil, err
	}
	links, err := resolvedAssociationAtoms(draft, actual)
	if err != nil {
		return nil, err
	}
	measured, err := scoreAtoms(gold, links)
	if err != nil {
		return nil, err
	}
	result := *previous
	result.Issues = map[string]int{}
	for key, value := range previous.Issues {
		result.Issues[key] = value
	}
	for key, adjustment := range map[string]int{"missing": measured.Issues["missing"] - (previous.ExpectedLinks - previous.CorrectLinks), "unexpected_or_incorrect": measured.Issues["unexpected_or_incorrect"] - (previous.PredictedLinks - previous.CorrectLinks)} {
		count := result.Issues[key] + adjustment
		if count < 0 {
			return nil, ErrInvalidBuild
		}
		if count == 0 {
			delete(result.Issues, key)
		} else {
			result.Issues[key] = count
		}
	}
	result.ExpectedLinks, result.PredictedLinks, result.CorrectLinks = measured.ExpectedLinks, measured.PredictedLinks, measured.CorrectLinks
	calculateAcceptance(&result)
	return &result, nil
}

// MeasureLinks reads the already-pinned independent references. It never
// changes annotations or the measured extraction counts. The caller commits
// these candidate scores only after all link writes and final guards succeed.
func (s *ReferenceService) MeasureLinks(ctx context.Context, token string, build *interfaces.KBBuild, draft *interfaces.Draft, writes []kb.DatasetWrite, guard func(context.Context) error) ([]*AcceptanceCounts, error) {
	if s == nil || s.Store == nil || s.Datasets == nil || build == nil || draft == nil || build.Mode != "acceptance" || build.ReferenceID != referenceID(build) || build.ReferenceRevision < 1 || guard == nil {
		return nil, ErrAcceptanceRequired
	}
	record, err := s.Store.Get(ctx, build.ProjectID, build.ReferenceID)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Revision != build.ReferenceRevision || len(record.Files) != len(build.Files) {
		return nil, ErrAcceptanceRequired
	}
	result := []*AcceptanceCounts{}
	for i, file := range build.Files {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		if file.File != record.Files[i].File || file.State != "succeeded" || file.Assessment == nil {
			return nil, ErrAcceptanceRequired
		}
		input, err := s.readInput(ctx, token, build, i)
		if err != nil {
			return nil, err
		}
		review, err := s.Read(ctx, build, record, i, input)
		if err != nil {
			return nil, err
		}
		expected, err := MaterializeReference(build, i, draft, input, *review)
		if err != nil {
			return nil, err
		}
		document, err := DocumentID(file.File.KnowledgeBaseID, file.File.FileID)
		if err != nil {
			return nil, err
		}
		actual := []kb.DatasetWrite{}
		for _, write := range writes {
			if write.Row["document_id"] == document {
				actual = append(actual, write)
			}
		}
		score, err := assessLinkWrites(draft, expected, actual, file.Assessment)
		if err != nil {
			return nil, err
		}
		result = append(result, score)
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func aggregateAcceptance(files []interfaces.KBFileBuild) *AcceptanceCounts {
	total := &AcceptanceCounts{Issues: map[string]int{}}
	for _, file := range files {
		if file.State != "succeeded" || file.Assessment == nil {
			return nil
		}
		score := file.Assessment
		total.Expected += score.Expected
		total.Predicted += score.Predicted
		total.Correct += score.Correct
		total.ExpectedLinks += score.ExpectedLinks
		total.PredictedLinks += score.PredictedLinks
		total.CorrectLinks += score.CorrectLinks
		for key, value := range score.Issues {
			total.Issues[key] += value
		}
	}
	calculateAcceptance(total)
	return total
}
