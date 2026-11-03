// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// prepareNativeLinks derives exact-number edges to the canonical entity rows
// in this same verified plan. An omitted AI edge does not change the confirmed
// identity rule; absent targets stay pending and no fuzzy match is invented.
func prepareNativeLinks(build *interfaces.KBBuild, draft *interfaces.Draft, claims []SourceClaim, fusion *EntityFusionPlan, result *preparedSystemLinks) error {
	for _, relation := range draft.Items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.Document == nil || relation.DocumentMapping == nil || relation.DocumentMapping.Mode != "linking" {
			continue
		}
		target := instanceObject(draft.Items, relation.Target)
		if target == nil || target.Document == nil || target.Document.Role != "entity" {
			continue
		}
		snapshot := &SystemSnapshot{Targets: []LinkTarget{}, Records: []ReconcileRecord{}}
		for _, write := range fusion.Writes {
			if write.Spec.ObjectTypeID != target.ID {
				continue
			}
			identity := write.Row[relation.DocumentMapping.IdentityProperty]
			if identity == nil {
				return ErrBuildChanged
			}
			snapshot.Targets = append(snapshot.Targets, LinkTarget{Identity: identity, RecordID: write.Row["_id"]})
			snapshot.Records = append(snapshot.Records, ReconcileRecord{Identity: identity})
		}
		raw, err := json.Marshal(snapshot.Targets)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		snapshot.Digest = hex.EncodeToString(digest[:])
		plan, err := PlanNativeEntityLinks(build, draft, relation, claims, snapshot)
		if err != nil {
			return err
		}
		result.Writes = append(result.Writes, plan.Writes...)
		text := map[string]ReconcileRecord{}
		for _, claim := range claims {
			if claim.ObjectTypeID != relation.Source || claim.Property != relation.DocumentMapping.MentionProperty {
				continue
			}
			key, err := businessJoinKey(claim.Value)
			if err != nil {
				return err
			}
			text[key] = ReconcileRecord{Identity: claim.Value}
		}
		textRows := []ReconcileRecord{}
		for _, row := range text {
			textRows = append(textRows, row)
		}
		counts, err := ReconcileSources(snapshot.Records, textRows, nil)
		if err != nil {
			return err
		}
		summary := SystemLinkSummary{RelationID: relation.ID, Name: relation.Name, Eligible: int64(plan.Resolution.Eligible), Linked: int64(len(plan.Resolution.Linked)), Pending: int64(len(plan.Resolution.Pending)), Both: counts.Both, SystemOnly: counts.SystemOnly, TextOnly: counts.TextOnly}
		result.Preview.Relations = append(result.Preview.Relations, summary)
		result.Preview.Eligible += summary.Eligible
		result.Preview.Linked += summary.Linked
		result.Preview.Pending += summary.Pending
		for index, step := range build.Files {
			rows, err := newFileRows(build, index, draft, "")
			if err != nil {
				return err
			}
			spec, exists := rows.relations[relation.ID]
			if !exists {
				return ErrBuildChanged
			}
			scope := systemLinkScope{Spec: spec, File: kb.DatasetFileScope{KnowledgeBaseID: step.File.KnowledgeBaseID, FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID, DocumentID: rows.document, ModelFingerprint: build.ModelFingerprint}, Keep: []string{}}
			for _, write := range plan.Writes {
				if write.Row["document_id"] == rows.document {
					scope.Keep = append(scope.Keep, write.Row["_id"].(string))
				}
			}
			slices.Sort(scope.Keep)
			result.Scopes = append(result.Scopes, scope)
		}
	}
	return nil
}
