// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
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
func prepareNativeLinks(ctx context.Context, reader kb.DatasetFileSnapshot, build *interfaces.KBBuild, draft *interfaces.Draft, claims []SourceClaim, fusion *EntityFusionPlan, result *preparedSystemLinks, guard func(context.Context) error, staged bool) error {
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
		plan, err := planIdentityLinks(build, draft, relation, claims, snapshot, nil, true, staged)
		if err != nil {
			return err
		}
		if err := preserveNativeRelationEvidence(ctx, reader, build, draft, relation, plan, guard); err != nil {
			return err
		}
		result.Writes = append(result.Writes, plan.Writes...)
		result.Preserved = append(result.Preserved, plan.Preserved...)
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
			for _, write := range append(slices.Clone(plan.Writes), plan.Preserved...) {
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

// Grounded extracted observations already linking the exact confirmed pair are
// retained at their stable IDs. A deterministic fallback is written only when
// no such edge exists. Keeping observations separately preserves all evidence
// without a growing union, silent truncation or needless identical rewrites.
func preserveNativeRelationEvidence(ctx context.Context, reader kb.DatasetFileSnapshot, build *interfaces.KBBuild, draft *interfaces.Draft, relation *interfaces.DraftRelationType, plan *SystemLinkPlan, guard func(context.Context) error) error {
	indexed := map[[4]string]map[string]any{}
	for _, write := range plan.Writes {
		document, a := write.Row["document_id"].(string)
		source, b := write.Row["source_record_id"].(string)
		target, c := write.Row["target_record_id"].(string)
		if !a || !b || !c {
			return ErrBuildChanged
		}
		key := [4]string{write.Spec.ResourceID, document, source, target}
		if indexed[key] != nil {
			return ErrBuildChanged
		}
		indexed[key] = write.Row
	}
	present := map[[4]string]bool{}
	for index, step := range build.Files {
		rows, err := newFileRows(build, index, draft, "")
		if err != nil {
			return err
		}
		spec, exists := rows.relations[relation.ID]
		if !exists {
			return ErrBuildChanged
		}
		scope := kb.DatasetFileScope{KnowledgeBaseID: step.File.KnowledgeBaseID, FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID, DocumentID: rows.document, ModelFingerprint: build.ModelFingerprint}
		existing, err := reader.ReadFileRows(ctx, spec, scope, guard)
		if err != nil {
			return err
		}
		for _, row := range existing {
			version, ok := row["extraction_version"].(string)
			if !ok || row["model_fingerprint"] != scope.ModelFingerprint || row["document_id"] != scope.DocumentID || row["content_version_id"] != scope.ContentVersionID || row["property_name"] != "" || row["claim_value"] != nil {
				return ErrBuildChanged
			}
			if _, err := verifiedSourceExecution(build, index, version); err != nil {
				return err
			}
			raw, err := json.Marshal(row["evidence"])
			if err != nil {
				return err
			}
			var proof []locatedEvidence
			if json.Unmarshal(raw, &proof) != nil || len(proof) == 0 || len(proof) > 40 {
				return ErrBuildChanged
			}
			for _, location := range proof {
				if location.BlockID == "" || location.Start < 0 || location.End <= location.Start || !step.File.IncludeOriginal && location.Quote != "" {
					return ErrBuildChanged
				}
			}
			if !step.File.IncludeOriginal && row["source_quote"] != "" {
				return ErrBuildChanged
			}
			source, a := row["source_record_id"].(string)
			target, b := row["target_record_id"].(string)
			if !a || !b {
				return ErrBuildChanged
			}
			key := [4]string{spec.ResourceID, rows.document, source, target}
			nativeRow := indexed[key]
			if nativeRow == nil {
				continue
			}
			for _, field := range []string{"source_identity", "target_identity"} {
				left, err := canonicalBusinessValue(nativeRow[field])
				if err != nil {
					return err
				}
				right, err := canonicalBusinessValue(row[field])
				if err != nil {
					return err
				}
				if string(left) != string(right) {
					return ErrBuildChanged
				}
			}
			normalized := map[string]any{}
			for field, value := range row {
				normalized[field] = value
			}
			normalized["evidence"] = proof
			plan.Preserved = append(plan.Preserved, kb.DatasetWrite{Spec: spec, Row: normalized})
			present[key] = true
		}
	}
	writes := []kb.DatasetWrite{}
	for _, write := range plan.Writes {
		key := [4]string{write.Spec.ResourceID, write.Row["document_id"].(string), write.Row["source_record_id"].(string), write.Row["target_record_id"].(string)}
		if !present[key] {
			writes = append(writes, write)
		}
	}
	plan.Writes = writes
	return nil
}
