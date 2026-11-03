// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"slices"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

type SystemLinkPlan struct {
	Preserved    []kb.DatasetWrite
	Writes       []kb.DatasetWrite
	Resolution   LinkResolution
	SystemDigest string
}

// PlanSystemLinks prepares a complete, replayable set of evidenced edges. Source
// statements and the structured table remain untouched. The write coordinator
// must revalidate this snapshot digest and current model/source grants before
// activating the set; a preview alone is never a permission to write.
func PlanSystemLinks(build *interfaces.KBBuild, draft *interfaces.Draft, relation *interfaces.DraftRelationType, claims []SourceClaim, system *SystemSnapshot, terms []ConfirmedTermLink) (*SystemLinkPlan, error) {
	return planIdentityLinks(build, draft, relation, claims, system, terms, false)
}

func PlanNativeEntityLinks(build *interfaces.KBBuild, draft *interfaces.Draft, relation *interfaces.DraftRelationType, claims []SourceClaim, target *SystemSnapshot) (*SystemLinkPlan, error) {
	return planIdentityLinks(build, draft, relation, claims, target, nil, true)
}

func planIdentityLinks(build *interfaces.KBBuild, draft *interfaces.Draft, relation *interfaces.DraftRelationType, claims []SourceClaim, system *SystemSnapshot, terms []ConfirmedTermLink, native bool) (*SystemLinkPlan, error) {
	if build == nil || draft == nil || relation == nil || !relation.Adopted || !kbconcept.LinkIdentityReady(draft.Items, relation) || system == nil || len(system.Digest) != 64 {
		return nil, ErrInvalidBuild
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	var target *interfaces.DraftObjectType
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.ID == relation.Target {
			target = object
		}
	}
	if target == nil || !target.Adopted || len(target.PrimaryKeys) != 1 || (!native && target.Document != nil) || (native && (target.Document == nil || target.Document.Role != "entity")) {
		return nil, ErrInvalidBuild
	}
	steps := map[string]int{}
	for index, step := range build.Files {
		if step.State != "succeeded" {
			return nil, ErrBuildChanged
		}
		document, err := DocumentID(step.File.KnowledgeBaseID, step.File.FileID)
		if err != nil {
			return nil, err
		}
		steps[document] = index
	}
	sources := []LinkSource{}
	bySource := map[string]SourceClaim{}
	for _, claim := range claims {
		if claim.ObjectTypeID != relation.Source || claim.Property != relation.DocumentMapping.MentionProperty {
			continue
		}
		index, exists := steps[claim.DocumentID]
		if !exists || claim.ContentVersionID != build.Files[index].File.ContentVersionID || len(claim.Evidence) == 0 {
			return nil, ErrBuildChanged
		}
		if _, err := verifiedSourceExecution(build, index, claim.ExtractionVersion); err != nil {
			return nil, err
		}
		id, err := identityDigest("bkn-link-mention-v1", relation.ID, claim.DocumentID, claim.RecordID)
		if err != nil {
			return nil, err
		}
		if _, duplicate := bySource[id]; duplicate {
			return nil, ErrBuildChanged
		}
		bySource[id] = claim
		term, _ := claim.Value.(string)
		sources = append(sources, LinkSource{ID: id, Identity: claim.Value, Term: term})
	}
	resolution, err := ResolveLinks(build.NetworkID, sources, system.Targets, terms)
	if err != nil {
		return nil, err
	}
	result := &SystemLinkPlan{Writes: []kb.DatasetWrite{}, Resolution: resolution, SystemDigest: system.Digest}
	businessIdentities := map[string]any{}
	for _, target := range system.Targets {
		record := target.RecordID
		if record == nil {
			record = target.Identity
		}
		key, err := businessJoinKey(record)
		if err != nil {
			return nil, err
		}
		businessIdentities[key] = target.Identity
	}
	for _, link := range resolution.Linked {
		claim := bySource[link.SourceID]
		index := steps[claim.DocumentID]
		rows, err := newFileRows(build, index, draft, claim.ExtractionVersion)
		if err != nil {
			return nil, err
		}
		spec, exists := rows.relations[relation.ID]
		if !exists {
			return nil, ErrBuildChanged
		}
		key, err := businessJoinKey(link.Target)
		if err != nil {
			return nil, err
		}
		prefix := "bkn-system-link-v1"
		if native {
			prefix = "bkn-native-link-v1"
		}
		id, err := identityDigest(prefix, build.NetworkID, relation.ID, claim.RecordID, key, claim.DocumentID)
		if err != nil {
			return nil, err
		}
		proof := slices.Clone(claim.Evidence)
		slices.SortFunc(proof, func(a, b locatedEvidence) int {
			left, _ := json.Marshal(a)
			right, _ := json.Marshal(b)
			return strings.Compare(string(left), string(right))
		})
		first := proof[0]
		business, exists := businessIdentities[key]
		if !exists {
			return nil, ErrBuildChanged
		}
		values := map[string]any{"_id": id, "source_record_id": claim.RecordID, "target_record_id": link.Target, "source_identity": claim.Identity, "target_identity": map[string]any{relation.DocumentMapping.IdentityProperty: business}, "document_id": claim.DocumentID, "content_version_id": claim.ContentVersionID, "block_id": first.BlockID, "source_start": first.Start, "source_end": first.End, "source_quote": first.Quote, "media_id": "", "media_occurrence_id": "", "property_name": "", "claim_value": nil, "evidence": proof}
		if err := rows.add(spec, values); err != nil {
			return nil, err
		}
		for _, write := range rows.writes {
			result.Writes = append(result.Writes, write)
		}
	}
	slices.SortFunc(result.Writes, func(a, b kb.DatasetWrite) int { return strings.Compare(a.Row["_id"].(string), b.Row["_id"].(string)) })
	return result, nil
}
