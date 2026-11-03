// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
)

type FactStateSummary struct {
	ObjectTypeID string `json:"object_type_id"`
	Name         string `json:"name"`
	Records      int    `json:"records"`
	Current      int    `json:"current"`
	Superseded   int    `json:"superseded"`
	Pending      int    `json:"pending"`
}
type factStatePlan struct {
	Unconfirmed       []string
	Writes, Preserved []kb.DatasetWrite
	Summaries         []FactStateSummary
}
type factStateRow struct {
	object  *interfaces.DraftObjectType
	write   kb.DatasetWrite
	claims  map[string]SourceClaim
	subject string
}

// planFactStates reads every acknowledged fact row before returning any update.
// Claims and entity links come from the same verified preparation. It never
// guesses a fact's business value, subject, effective period or evidence rank.
func planFactStates(ctx context.Context, reader kb.DatasetFileSnapshot, build *interfaces.KBBuild, draft *interfaces.Draft, claims []SourceClaim, links []kb.DatasetWrite, now int64, guard func(context.Context) error) (*factStatePlan, error) {
	if reader == nil || build == nil || draft == nil || guard == nil || now <= 0 || len(build.Files) == 0 {
		return nil, ErrInvalidBuild
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	result := &factStatePlan{Writes: []kb.DatasetWrite{}, Preserved: []kb.DatasetWrite{}, Summaries: []FactStateSummary{}}
	factTypes := map[string]bool{}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Adopted && object.Document != nil && object.Document.Role == "fact" {
			if kbconcept.ValidFactResolution(draft.Items, object, object.Document.FactResolution) {
				factTypes[object.ID] = true
			} else {
				result.Unconfirmed = append(result.Unconfirmed, object.Name)
			}
		}
	}
	slices.Sort(result.Unconfirmed)
	if len(factTypes) == 0 {
		return result, nil
	}
	claimIndex := map[string]map[string]SourceClaim{}
	for _, claim := range claims {
		if !factTypes[claim.ObjectTypeID] {
			continue
		}
		key := claim.ObjectTypeID + "/" + claim.RecordID
		if claimIndex[key] == nil {
			claimIndex[key] = map[string]SourceClaim{}
		}
		if previous, exists := claimIndex[key][claim.Property]; exists {
			left, err := canonicalBusinessValue(previous.Value)
			if err != nil {
				return nil, err
			}
			right, err := canonicalBusinessValue(claim.Value)
			if err != nil || string(left) != string(right) {
				return nil, ErrBuildChanged
			}
		}
		claimIndex[key][claim.Property] = claim
	}
	linkIndex := map[string]map[string]bool{}
	for _, link := range links {
		if link.Spec.RelationTypeID == "" {
			continue
		}
		source, ok := link.Row["source_record_id"].(string)
		if !ok {
			return nil, ErrBuildChanged
		}
		identity, ok := link.Row["target_identity"].(map[string]any)
		if !ok {
			return nil, ErrBuildChanged
		}
		encoded, err := canonicalBusinessValue(identity)
		if err != nil {
			return nil, err
		}
		key := link.Spec.RelationTypeID + "/" + source
		if linkIndex[key] == nil {
			linkIndex[key] = map[string]bool{}
		}
		linkIndex[key][string(encoded)] = true
	}
	all := []factStateRow{}
	payloadBytes := 0
	seen := map[string]bool{}
	for _, object := range draft.Items.ObjectTypes {
		if object == nil || !object.Adopted || object.Document == nil || object.Document.Role != "fact" || !factTypes[object.ID] {
			continue
		}
		valid := kbconcept.ValidFactResolution(draft.Items, object, object.Document.FactResolution)
		for index, step := range build.Files {
			rows, err := newFileRows(build, index, draft, "")
			if err != nil {
				return nil, err
			}
			spec, exists := rows.objects[object.ID]
			if !exists || !spec.Ready {
				return nil, ErrBuildChanged
			}
			scope := kb.DatasetFileScope{KnowledgeBaseID: step.File.KnowledgeBaseID, FileID: step.File.FileID, ContentVersionID: step.File.ContentVersionID, DocumentID: rows.document, ModelFingerprint: build.ModelFingerprint}
			observed, err := reader.ReadFileRows(ctx, spec, scope, guard)
			if err != nil {
				return nil, err
			}
			for _, row := range observed {
				id, ok := row["_id"].(string)
				key := object.ID + "/" + id
				if !ok || id == "" || seen[key] || len(all) >= 100000 || row["document_id"] != scope.DocumentID || row["content_version_id"] != scope.ContentVersionID || row["model_fingerprint"] != build.ModelFingerprint || !step.File.IncludeOriginal && row["source_quote"] != "" {
					return nil, ErrBuildChanged
				}
				seen[key] = true
				encoded, err := json.Marshal(row)
				if err != nil {
					return nil, ErrInvalidBuild
				}
				payloadBytes += len(encoded)
				if payloadBytes > 32<<20 {
					return nil, ErrInvalidBuild
				}
				version, ok := row["extraction_version"].(string)
				if !ok {
					return nil, ErrBuildChanged
				}
				execution, err := verifiedSourceExecution(build, index, version)
				if err != nil {
					return nil, err
				}
				recorded, ok := row["recorded_at"].(string)
				if !ok {
					return nil, ErrBuildChanged
				}
				stamp, err := time.Parse(time.RFC3339Nano, recorded)
				if err != nil || stamp.UnixMilli() != execution.RecordedAt {
					return nil, ErrBuildChanged
				}
				status, ok := row["status"].(string)
				if !ok || status != "current" && status != "superseded" && status != "pending_review" {
					return nil, ErrBuildChanged
				}
				recordClaims := claimIndex[key]
				for _, field := range object.Properties {
					if !ordinaryInstanceProperty(object, field) {
						continue
					}
					value, exists := row[field.Name]
					if !exists {
						return nil, ErrBuildChanged
					}
					claim, asserted := recordClaims[field.Name]
					if !asserted {
						if value != nil {
							return nil, ErrBuildChanged
						}
						continue
					}
					left, err := canonicalBusinessValue(value)
					if err != nil {
						return nil, err
					}
					right, err := canonicalBusinessValue(claim.Value)
					if err != nil || string(left) != string(right) || claim.ExtractionVersion != version || claim.DocumentID != scope.DocumentID || claim.ContentVersionID != scope.ContentVersionID {
						return nil, ErrBuildChanged
					}
				}
				entry := factStateRow{object: object, write: kb.DatasetWrite{Spec: spec, Row: row}, claims: recordClaims}
				if valid {
					subjects := linkIndex[object.Document.FactResolution.SubjectRelationID+"/"+id]
					if len(subjects) == 1 {
						for subject := range subjects {
							entry.subject = subject
						}
					}
				}
				all = append(all, entry)
			}
		}
	}
	for _, claim := range claims {
		object := instanceObject(draft.Items, claim.ObjectTypeID)
		if object != nil && object.Document != nil && object.Document.Role == "fact" && factTypes[object.ID] && !seen[claim.ObjectTypeID+"/"+claim.RecordID] {
			return nil, ErrBuildChanged
		}
	}
	statuses, err := resolveFactStates(all, now)
	if err != nil {
		return nil, err
	}
	summaries := map[string]*FactStateSummary{}
	for _, entry := range all {
		summary := summaries[entry.object.ID]
		if summary == nil {
			summary = &FactStateSummary{ObjectTypeID: entry.object.ID, Name: entry.object.Name}
			summaries[entry.object.ID] = summary
		}
		id := entry.write.Row["_id"].(string)
		status := statuses[entry.object.ID+"/"+id]
		summary.Records++
		switch status {
		case "current":
			summary.Current++
		case "superseded":
			summary.Superseded++
		default:
			summary.Pending++
		}
		expected := kb.DatasetWrite{Spec: entry.write.Spec, Row: maps.Clone(entry.write.Row)}
		expected.Row["status"] = status
		if build.Mode != "full" || entry.write.Row["status"] == status {
			result.Preserved = append(result.Preserved, expected)
		} else {
			result.Writes = append(result.Writes, expected)
		}
	}
	for _, summary := range summaries {
		result.Summaries = append(result.Summaries, *summary)
	}
	slices.SortFunc(result.Summaries, func(a, b FactStateSummary) int { return strings.Compare(a.ObjectTypeID, b.ObjectTypeID) })
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func resolveFactStates(rows []factStateRow, now int64) (map[string]string, error) {
	if now <= 0 {
		return nil, ErrInvalidBuild
	}
	statuses := map[string]string{}
	groups := map[string][]FusionClaim{}
	for _, row := range rows {
		id, ok := row.write.Row["_id"].(string)
		if !ok || id == "" || row.object == nil || row.object.Document == nil {
			return nil, ErrBuildChanged
		}
		key := row.object.ID + "/" + id
		statuses[key] = "pending_review"
		proof := row.object.Document.FactResolution
		if proof == nil || row.subject == "" {
			continue
		}
		claim, exists := row.claims[proof.ValueProperty]
		if !exists || claim.Value == nil {
			continue
		}
		from, err := fileClaimTime(row.object, row.claims, "effective_from")
		if err != nil {
			return nil, err
		}
		until, err := fileClaimTime(row.object, row.claims, "effective_to")
		if err != nil {
			return nil, err
		}
		if from != nil && until != nil && *until <= *from {
			continue
		}
		if from != nil && until != nil && *until <= now {
			statuses[key] = "superseded"
			continue
		}
		group := row.object.ID + "/" + row.subject
		groups[group] = append(groups[group], FusionClaim{ID: key, EntityID: row.subject, Property: proof.ValueProperty, Value: claim.Value, EffectiveFrom: from, EffectiveTo: until, RecordedAt: claim.RecordedAt})
	}
	for _, claims := range groups {
		resolved, err := ResolvePropertyClaims(claims, now, true)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved.Current {
			statuses[id] = "current"
		}
		for _, id := range resolved.Superseded {
			statuses[id] = "superseded"
		}
	}
	return statuses, nil
}
