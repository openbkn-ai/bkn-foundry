// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

type SourceClaim struct {
	ID, ObjectTypeID, RecordID, Property, DocumentID, ContentVersionID string
	ExtractionVersion                                                  string
	Identity                                                           map[string]any
	Value                                                              any
	Evidence                                                           []locatedEvidence
	RecordedAt                                                         int64
}

// ReadSourceClaims consumes only the current build's acknowledged, independently
// evidenced property assertions. It returns no partial result on a stale task,
// changed model, failed page or revoked source. Block/media copies are not used
// as additional assertions; document-reference edges are the canonical claims.
func ReadSourceClaims(ctx context.Context, reader kb.DatasetFileSnapshot, build *interfaces.KBBuild, draft *interfaces.Draft, guard func(context.Context) error) ([]SourceClaim, error) {
	return readSourceClaims(ctx, reader, build, draft, guard, false)
}

// Staged claims are explicitly preview-only; construction's public reader keeps
// requiring live acknowledgements. Refresh validates task outputs separately.
func readSourceClaims(ctx context.Context, reader kb.DatasetFileSnapshot, build *interfaces.KBBuild, draft *interfaces.Draft, guard func(context.Context) error, staged bool) ([]SourceClaim, error) {
	if reader == nil || guard == nil || build == nil || draft == nil || len(build.Files) == 0 {
		return nil, ErrInvalidBuild
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	result := []SourceClaim{}
	seen := map[string]bool{}
	for index, step := range build.Files {
		if !usableClaimStep(step, staged) {
			return nil, ErrBuildChanged
		}
		rows, err := newFileRows(build, index, draft, "")
		if err != nil || len(rows.relations) == 0 {
			return nil, ErrBuildChanged
		}
		scope := kb.DatasetFileScope{KnowledgeBaseID: step.File.KnowledgeBaseID, FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID, DocumentID: rows.document, ModelFingerprint: build.ModelFingerprint}
		for _, relation := range draft.Items.RelationTypes {
			if relation == nil || !relation.Adopted || relation.Document == nil || relation.Document.Role != "evidence_reference" || relation.Target != rows.roles["evidence_document"] {
				continue
			}
			object := instanceObject(draft.Items, relation.Source)
			if object == nil || strings.HasPrefix(object.Document.Role, "evidence_") {
				continue
			}
			spec, exists := rows.relations[relation.ID]
			if !exists || !spec.Ready {
				return nil, ErrBuildChanged
			}
			if err := guard(ctx); err != nil {
				return nil, err
			}
			claims, err := reader.ReadFileRows(ctx, spec, scope, guard)
			if err != nil {
				return nil, err
			}
			for _, row := range claims {
				claim, err := sourceClaim(build, index, draft, object, scope, row)
				if err != nil {
					return nil, err
				}
				key := spec.ResourceID + "/" + claim.ID
				if seen[key] || len(result) >= 100000 {
					return nil, ErrBuildChanged
				}
				seen[key] = true
				result = append(result, *claim)
			}
		}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b SourceClaim) int {
		return strings.Compare(a.ObjectTypeID+"/"+a.DocumentID+"/"+a.ID, b.ObjectTypeID+"/"+b.DocumentID+"/"+b.ID)
	})
	return result, nil
}

func sourceClaim(build *interfaces.KBBuild, index int, draft *interfaces.Draft, object *interfaces.DraftObjectType, scope kb.DatasetFileScope, row map[string]any) (*SourceClaim, error) {
	id, _ := row["_id"].(string)
	record, _ := row["source_record_id"].(string)
	property, _ := row["property_name"].(string)
	version, _ := row["extraction_version"].(string)
	identity, ok := row["source_identity"].(map[string]any)
	keys := object.PrimaryKeys
	if object.Document.Role == "file" {
		key, err := kbconcept.FileIdentityProperty(draft.Items, object)
		if err != nil {
			return nil, ErrBuildChanged
		}
		keys = []string{key}
	} else if object.Document.Role != "entity" {
		keys = nil
		if object.DisplayKey != "" && identity[object.DisplayKey] != nil {
			keys = []string{object.DisplayKey}
		}
	}
	field := instanceProperty(object, property)
	if id == "" || record == "" || !ok || len(identity) != len(keys) || row["model_fingerprint"] != scope.ModelFingerprint || row["document_id"] != scope.DocumentID || row["target_record_id"] != scope.DocumentID || row["content_version_id"] != scope.ContentVersionID || !ordinaryInstanceProperty(object, field) || !validInstanceValue(field.Type, row["claim_value"]) {
		return nil, ErrBuildChanged
	}
	for _, key := range keys {
		p := instanceProperty(object, key)
		if p == nil || identity[key] == nil || !validInstanceValue(p.Type, identity[key]) {
			return nil, ErrBuildChanged
		}
	}
	receipt, err := verifiedSourceExecution(build, index, version)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(row["evidence"])
	if err != nil {
		return nil, ErrBuildChanged
	}
	var evidence []locatedEvidence
	if json.Unmarshal(encoded, &evidence) != nil || len(evidence) == 0 || len(evidence) > 20 {
		return nil, ErrBuildChanged
	}
	for _, location := range evidence {
		if location.BlockID == "" || location.Start < 0 || location.End <= location.Start || (!build.Files[index].File.IncludeOriginal && location.Quote != "") {
			return nil, ErrBuildChanged
		}
	}
	if !build.Files[index].File.IncludeOriginal && row["source_quote"] != "" {
		return nil, ErrBuildChanged
	}
	return &SourceClaim{ID: id, ObjectTypeID: object.ID, RecordID: record, Property: property, DocumentID: scope.DocumentID, ContentVersionID: scope.ContentVersionID, ExtractionVersion: version, Identity: identity, Value: row["claim_value"], Evidence: evidence, RecordedAt: receipt.RecordedAt}, nil
}

func usableClaimStep(step interfaces.KBFileBuild, staged bool) bool {
	return step.State == "succeeded" || (staged && step.State == "validated" && len(step.RowsDigest) == 64 && len(step.InputDigest) == 64)
}
