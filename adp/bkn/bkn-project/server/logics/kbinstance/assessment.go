// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

// ReferenceReview is stored in a dedicated Dataset. It is never embedded in a
// project process row or returned by debug/acceptance prediction endpoints.
// The reviewer confirms against the source, independently of the prediction.
type ReferenceReview struct {
	Locations  map[string]ReferenceLocation `json:"locations,omitempty"`
	Reviewer   interfaces.AccountInfo       `json:"reviewer"`
	ReviewedAt int64                        `json:"reviewed_at"`
	Revision   int64                        `json:"revision"`
	Extraction FileExtraction               `json:"extraction"`
}

// MaterializeReference validates human references with the exact extraction
// model and source rules. Returned rows are comparison values, not writes to
// the prediction datasets; the review controller stores the reference payload
// in its separate immutable Dataset row. No Agent execution is fabricated.
func MaterializeReference(build *interfaces.KBBuild, index int, draft *interfaces.Draft, input *kb.FileInput, review ReferenceReview) ([]kb.DatasetWrite, error) {
	if build == nil || draft == nil || index < 0 || index >= len(build.Files) || review.Reviewer.ID == "" || review.Reviewer.Type != "user" || review.Reviewer != build.Owner || review.ReviewedAt <= 0 || review.Revision < 1 {
		return nil, ErrInvalidBuild
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	digest, err := FileInputDigest(fileRef(build.Files[index].File), input)
	if err != nil || digest != build.Files[index].InputDigest {
		return nil, ErrInvalidBuild
	}
	raw, err := json.Marshal(review.Extraction)
	if err != nil {
		return nil, ErrInvalidBuild
	}
	proposal, err := parseFileExtraction(string(raw))
	if err != nil {
		return nil, err
	}
	version, err := json.Marshal(struct {
		Reviewed int                    `json:"reviewed_reference"`
		Revision int64                  `json:"revision"`
		Reviewer interfaces.AccountInfo `json:"reviewer"`
		At       int64                  `json:"reviewed_at"`
	}{1, review.Revision, review.Reviewer, review.ReviewedAt})
	if err != nil {
		return nil, err
	}
	return materializeProposal(build, index, draft, input, proposal, string(version), review.ReviewedAt)
}

// AcceptanceCounts contains only measured counts, scores and issue categories.
// A nil score means an unknown denominator; it can never satisfy a release gate.
type AcceptanceCounts = interfaces.KBAcceptanceCounts

type assessmentAtom struct{ Kind, Key, Value string }

func scoreAtoms(expected, predicted []assessmentAtom) (AcceptanceCounts, error) {
	result := AcceptanceCounts{Issues: map[string]int{}}
	sets := func(atoms []assessmentAtom) (map[assessmentAtom]bool, error) {
		set := map[assessmentAtom]bool{}
		for _, atom := range atoms {
			if atom.Key == "" || atom.Value == "" || (atom.Kind != "object" && atom.Kind != "property" && atom.Kind != "relation" && atom.Kind != "link") {
				return nil, ErrInvalidBuild
			}
			set[atom] = true
		}
		return set, nil
	}
	gold, err := sets(expected)
	if err != nil {
		return result, err
	}
	actual, err := sets(predicted)
	if err != nil {
		return result, err
	}
	for atom := range gold {
		if atom.Kind == "link" {
			result.ExpectedLinks++
		} else {
			result.Expected++
		}
		if !actual[atom] {
			result.Issues["missing"]++
		}
	}
	for atom := range actual {
		if atom.Kind == "link" {
			result.PredictedLinks++
		} else {
			result.Predicted++
		}
		if gold[atom] {
			if atom.Kind == "link" {
				result.CorrectLinks++
			} else {
				result.Correct++
			}
		} else {
			result.Issues["unexpected_or_incorrect"]++
		}
	}
	calculateAcceptance(&result)
	return result, nil
}
func measuredRatio(correct, total int) *float64 {
	if total <= 0 {
		return nil
	}
	ratio := float64(correct) / float64(total)
	return &ratio
}
func calculateAcceptance(a *AcceptanceCounts) {
	if a.Expected < 0 || a.Predicted < 0 || a.Correct < 0 || a.Correct > a.Expected || a.Correct > a.Predicted || a.ExpectedLinks < 0 || a.PredictedLinks < 0 || a.CorrectLinks < 0 || a.CorrectLinks > a.PredictedLinks || a.CorrectLinks > a.ExpectedLinks {
		a.Precision, a.Recall, a.Linking = nil, nil, nil
		a.Passed = false
		return
	}

	a.Precision = measuredRatio(a.Correct, a.Predicted)
	a.Recall = measuredRatio(a.Correct, a.Expected)
	a.Linking = nil
	// The design's linking metric is alignment coverage: verified links to
	// confirmed business identities divided by eligible source mentions.
	// Similar names and unresolved/ambiguous candidates never count as linked.
	a.Linking = measuredRatio(a.CorrectLinks, a.ExpectedLinks)

	a.Passed = a.Precision != nil && a.Recall != nil && a.Linking != nil && *a.Precision >= 0.90 && *a.Recall >= 0.85 && *a.Linking >= 0.95
}

func assessmentAtoms(draft *interfaces.Draft, writes []kb.DatasetWrite) ([]assessmentAtom, error) {
	atoms := []assessmentAtom{}
	relations := map[string]*interfaces.DraftRelationType{}
	objects := map[string]*interfaces.DraftObjectType{}
	for _, relation := range draft.Items.RelationTypes {
		if relation != nil {
			relations[relation.ID] = relation
		}
	}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil {
			objects[object.ID] = object
		}
	}
	for _, write := range writes {
		relation := relations[write.Spec.RelationTypeID]
		if relation == nil || relation.Document == nil {
			continue
		}
		if relation.Document.Role == "evidence_reference" {
			target := objects[relation.Target]
			if target == nil || target.Document == nil || target.Document.Role != "evidence_document" {
				continue
			}
			id, ok := write.Row["source_record_id"].(string)
			if !ok || id == "" {
				return nil, ErrInvalidBuild
			}
			atoms = append(atoms, assessmentAtom{Kind: "object", Key: relation.Source + "/" + id, Value: "present"})
			property, _ := write.Row["property_name"].(string)
			if property != "" {
				value, err := canonicalBusinessValue(write.Row["claim_value"])
				if err != nil {
					return nil, ErrInvalidBuild
				}
				atoms = append(atoms, assessmentAtom{Kind: "property", Key: relation.Source + "/" + id + "/" + property, Value: string(value)})
			}
		} else if relation.Document.Role == "relation" {
			source, sourceOK := write.Row["source_record_id"].(string)
			target, targetOK := write.Row["target_record_id"].(string)
			if !sourceOK || !targetOK || source == "" || target == "" {
				return nil, ErrInvalidBuild
			}
			atoms = append(atoms, assessmentAtom{Kind: "relation", Key: relation.ID + "/" + source + "/" + target, Value: "present"})
		}
	}
	return atoms, nil
}
