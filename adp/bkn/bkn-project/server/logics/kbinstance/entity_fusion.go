// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

// EntityFusionReceipt is Dataset provenance. Selected references identify the
// existing located property-claim edges, including for multi-source entities.
type EntityFusionReceipt struct {
	Version     int                 `json:"fusion"`
	BuildID     string              `json:"build_id"`
	InputDigest string              `json:"input_digest"`
	Sources     map[string][]string `json:"current_sources"`
	Pending     map[string]string   `json:"pending_fields"`
}
type EntityFusionSummary struct {
	ObjectTypeID string `json:"object_type_id"`
	Name         string `json:"name"`
	Entities     int    `json:"entities"`
	Current      int    `json:"current"`
	Pending      int    `json:"pending"`
}
type sourceFileGroup struct{ object, document, version string }
type entityPeriodKey struct{ document, version, record string }
type entityPeriod struct{ from, until *int64 }

type EntityFusionPlan struct {
	Writes    []kb.DatasetWrite
	Summaries []EntityFusionSummary
	Keep      map[string][]string
}

// PlanEntityFusion consumes a complete verified claim snapshot and proposes
// ordinary native Dataset rows. It never selects a source by name or row order.
func PlanEntityFusion(build *interfaces.KBBuild, draft *interfaces.Draft, claims []SourceClaim, previous map[string][]map[string]any, now int64) (*EntityFusionPlan, error) {
	if build == nil || draft == nil || len(build.Files) == 0 || now <= 0 {
		return nil, ErrInvalidBuild
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	rows, err := newFileRows(build, 0, draft, "")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(claims)
	if err != nil || len(raw) > 32<<20 {
		return nil, ErrInvalidBuild
	}
	digest := sha256.Sum256(raw)
	scopedClaims := map[sourceFileGroup][]SourceClaim{}
	for _, claim := range claims {
		key := sourceFileGroup{claim.ObjectTypeID, claim.DocumentID, claim.ContentVersionID}
		scopedClaims[key] = append(scopedClaims[key], claim)
	}
	result := &EntityFusionPlan{Writes: []kb.DatasetWrite{}, Summaries: []EntityFusionSummary{}, Keep: map[string][]string{}}
	for _, object := range draft.Items.ObjectTypes {
		if object == nil || !object.Adopted || object.Document == nil || object.Document.Role != "entity" {
			continue
		}
		spec, exists := rows.objects[object.ID]
		if !exists || !spec.Ready {
			return nil, ErrBuildChanged
		}
		old := map[string]map[string]any{}
		for _, row := range previous[spec.ResourceID] {
			id, ok := row["_id"].(string)
			if !ok || id == "" || old[id] != nil || row["model_fingerprint"] != build.ModelFingerprint || len(row) != len(spec.Layout.Fields) {
				return nil, ErrBuildChanged
			}
			old[id] = row
		}
		grouped := map[string]map[string][]FusionClaim{}
		identities := map[string]map[string]any{}
		seen := map[string]bool{}
		periodCache := map[entityPeriodKey]entityPeriod{}
		for _, claim := range claims {
			if claim.ObjectTypeID != object.ID {
				continue
			}
			if seen[claim.ID] || claim.ID == "" {
				return nil, ErrBuildChanged
			}
			seen[claim.ID] = true
			field := instanceProperty(object, claim.Property)
			if field == nil || field.ReadOnly || !validInstanceValue(field.Type, claim.Value) || len(claim.Identity) != len(object.PrimaryKeys) {
				return nil, ErrBuildChanged
			}
			identityValues := map[string]InstanceValue{}
			for _, key := range object.PrimaryKeys {
				p := instanceProperty(object, key)
				if p == nil || claim.Identity[key] == nil || !validInstanceValue(p.Type, claim.Identity[key]) {
					return nil, ErrBuildChanged
				}
				identityValues[key] = InstanceValue{Value: claim.Identity[key]}
			}
			key, err := businessKey(object, InstanceRecord{Values: identityValues})
			if err != nil {
				return nil, err
			}
			id, err := identityDigest("bkn-global-entity-v1", build.NetworkID, object.ID, key)
			if err != nil || id != claim.RecordID {
				return nil, ErrBuildChanged
			}
			if grouped[id] == nil {
				grouped[id] = map[string][]FusionClaim{}
				identities[id] = claim.Identity
			}
			if claim.Value == nil {
				continue
			}
			if field.SourceOfRecord == "table" && !slices.Contains(object.PrimaryKeys, field.Name) {
				return nil, ErrBuildChanged
			}
			if slices.Contains(object.PrimaryKeys, field.Name) {
				actual, err := canonicalBusinessValue(claim.Value)
				if err != nil {
					return nil, err
				}
				expected, err := canonicalBusinessValue(claim.Identity[field.Name])
				if err != nil || string(actual) != string(expected) {
					return nil, ErrBuildChanged
				}
			}
			periodKey := entityPeriodKey{claim.DocumentID, claim.ContentVersionID, id}
			period, found := periodCache[periodKey]
			if !found {
				from, until, err := entityClaimPeriod(draft.Items, object, claim, scopedClaims)
				if err != nil {
					return nil, err
				}
				period = entityPeriod{from, until}
				periodCache[periodKey] = period
			}
			// Business identity is permanent; a source document's term period
			// does not make the entity identifier itself expire.
			if slices.Contains(object.PrimaryKeys, field.Name) {
				period = entityPeriod{}
			}
			grouped[id][field.Name] = append(grouped[id][field.Name], FusionClaim{ID: claim.ID, EntityID: id, Property: field.Name, Value: claim.Value, EffectiveFrom: period.from, EffectiveTo: period.until, RecordedAt: claim.RecordedAt})
		}
		summary := EntityFusionSummary{ObjectTypeID: object.ID, Name: object.Name, Entities: len(grouped)}
		result.Keep[spec.ResourceID] = []string{}
		for id, properties := range grouped {
			for _, key := range object.PrimaryKeys {
				if len(properties[key]) == 0 {
					return nil, ErrBuildChanged
				}
			}
			values := map[string]any{"_id": id, "entity_id": id, "document_id": "", "content_version_id": "", "block_id": "", "source_start": 0, "source_end": 0, "source_quote": ""}
			for name, value := range identities[id] {
				values[name] = value
			}
			receipt := EntityFusionReceipt{Version: 1, BuildID: build.ID, InputDigest: hex.EncodeToString(digest[:]), Sources: map[string][]string{}, Pending: map[string]string{}}
			var prior EntityFusionReceipt
			if old[id] != nil {
				encoded, ok := old[id]["extraction_version"].(string)
				if !ok || json.Unmarshal([]byte(encoded), &prior) != nil || prior.Version != 1 {
					return nil, ErrBuildChanged
				}
			}
			for name, assertions := range properties {
				resolved, err := resolveEntityProperty(object, name, assertions, now)
				if err != nil {
					return nil, err
				}
				if resolved.Value != nil {
					values[name] = resolved.Value
					receipt.Sources[name] = resolved.Current
					summary.Current++
				} else if len(resolved.Pending) > 0 {
					reason := resolved.Reason
					if reason == "" {
						reason = "conflicting_evidence"
					}
					receipt.Pending[name] = reason
					summary.Pending++
					// An old value survives only while every cited source is still active
					// and still asserts that exact value. Removed evidence is not authority.
					if old[id] != nil && priorValueStillEvidenced(old[id][name], prior.Sources[name], assertions, now) {
						values[name] = old[id][name]
						receipt.Sources[name] = prior.Sources[name]
					}
				}
			}
			encoded, err := json.Marshal(receipt)
			if err != nil {
				return nil, err
			}
			rows.version = string(encoded)
			if err := rows.add(spec, values); err != nil {
				return nil, err
			}
			result.Keep[spec.ResourceID] = append(result.Keep[spec.ResourceID], id)
		}
		slices.Sort(result.Keep[spec.ResourceID])
		result.Summaries = append(result.Summaries, summary)
	}
	for _, write := range rows.writes {
		encoded, err := json.Marshal(write.Row)
		// Leave room for physical column names and the bounded 8 MiB read envelope.
		if err != nil || len(encoded) > 7<<20 {
			return nil, ErrInvalidBuild
		}
		result.Writes = append(result.Writes, write)
	}
	if _, err := validateWrites(build, draft, result.Writes); err != nil {
		return nil, err
	}
	slices.SortFunc(result.Summaries, func(a, b EntityFusionSummary) int { return strings.Compare(a.ObjectTypeID, b.ObjectTypeID) })
	return result, nil
}

func priorValueStillEvidenced(value any, ids []string, claims []FusionClaim, now int64) bool {
	if value == nil || len(ids) == 0 {
		return false
	}
	expected, err := canonicalBusinessValue(value)
	if err != nil {
		return false
	}
	found := map[string]bool{}
	for _, claim := range claims {
		if !slices.Contains(ids, claim.ID) || claim.EffectiveFrom != nil && *claim.EffectiveFrom > now || claim.EffectiveTo != nil && *claim.EffectiveTo <= now {
			continue
		}
		actual, err := canonicalBusinessValue(claim.Value)
		if err != nil || string(actual) != string(expected) {
			return false
		}
		found[claim.ID] = true
	}
	return len(found) == len(ids)
}

// An adopted identity link or explicit dual-source plan must connect the file
// period to the entity. Similarly named fields alone never establish authority.
func entityClaimPeriod(items interfaces.DraftItems, entity *interfaces.DraftObjectType, claim SourceClaim, claims map[sourceFileGroup][]SourceClaim) (*int64, *int64, error) {
	if len(entity.PrimaryKeys) != 1 {
		return nil, nil, nil
	}
	files := map[string]string{}
	if entity.Document.DualSource != nil {
		if !kbconcept.SourcePlanReady(items, entity, true) {
			return nil, nil, ErrBuildChanged
		}
		files[entity.Document.DualSource.FileObjectTypeID] = entity.PrimaryKeys[0]
	}
	for _, relation := range items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.Target != entity.ID || !kbconcept.LinkIdentityReady(items, relation) {
			continue
		}
		source := instanceObject(items, relation.Source)
		if source == nil || source.Document == nil || source.Document.Role != "file" || source.Document.FilePeriod == nil {
			continue
		}
		identity, err := kbconcept.FileIdentityProperty(items, source)
		if err != nil {
			return nil, nil, ErrBuildChanged
		}
		// A file may mention another entity (for example its supplier) without
		// making the file's own effective period authoritative for that entity.
		if identity != relation.DocumentMapping.MentionProperty {
			continue
		}
		files[source.ID] = entity.PrimaryKeys[0]
	}
	var selectedFrom, selectedUntil *int64
	selected := false
	for fileID, entityKey := range files {
		file := instanceObject(items, fileID)
		if file == nil || file.Document.FilePeriod == nil {
			continue
		}
		if !kbconcept.ValidFilePeriod(file, file.Document.FilePeriod) {
			return nil, nil, ErrBuildChanged
		}
		fileKey, err := kbconcept.FileIdentityProperty(items, file)
		if err != nil {
			return nil, nil, ErrBuildChanged
		}
		expected, err := businessJoinKey(claim.Identity[entityKey])
		if err != nil {
			return nil, nil, err
		}
		records := map[string]map[string]SourceClaim{}
		for _, source := range claims[sourceFileGroup{fileID, claim.DocumentID, claim.ContentVersionID}] {
			if source.ObjectTypeID != fileID || source.DocumentID != claim.DocumentID || source.ContentVersionID != claim.ContentVersionID {
				continue
			}
			actual, err := businessJoinKey(source.Identity[fileKey])
			if err != nil {
				return nil, nil, err
			}
			if actual != expected {
				continue
			}
			if records[source.RecordID] == nil {
				records[source.RecordID] = map[string]SourceClaim{}
			}
			if _, duplicate := records[source.RecordID][source.Property]; duplicate {
				return nil, nil, ErrBuildChanged
			}
			records[source.RecordID][source.Property] = source
		}
		for _, record := range records {
			from, err := fileClaimTime(file, record, file.Document.FilePeriod.From)
			if err != nil {
				return nil, nil, err
			}
			until, err := fileClaimTime(file, record, file.Document.FilePeriod.Until)
			if err != nil || from != nil && until != nil && *until <= *from {
				return nil, nil, ErrBuildChanged
			}
			if selected && (!reflect.DeepEqual(selectedFrom, from) || !reflect.DeepEqual(selectedUntil, until)) {
				return nil, nil, ErrBuildChanged
			}
			selectedFrom, selectedUntil, selected = from, until, true
		}
	}
	return selectedFrom, selectedUntil, nil
}

func fileClaimTime(object *interfaces.DraftObjectType, record map[string]SourceClaim, name string) (*int64, error) {
	if name == "" {
		return nil, nil
	}
	claim, exists := record[name]
	if !exists || claim.Value == nil {
		return nil, nil
	}
	value, ok := claim.Value.(string)
	field := instanceProperty(object, name)
	if !ok || field == nil {
		return nil, ErrBuildChanged
	}
	layout := time.RFC3339Nano
	if field.Type == "date" {
		layout = "2006-01-02"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return nil, ErrBuildChanged
	}
	stamp := parsed.UnixMilli()
	return &stamp, nil
}

// Temporal arbitration requires the existing explicit fusion authority. A
// document-owned field may combine agreeing active sources, but cannot silently
// become fusion-owned merely because a later date happens to be available.
func resolveEntityProperty(object *interfaces.DraftObjectType, name string, claims []FusionClaim, now int64) (FusionResult, error) {
	field := instanceProperty(object, name)
	if field == nil {
		return FusionResult{}, ErrBuildChanged
	}
	fusion := field.SourceOfRecord == "fusion"
	if object.Document.DualSource != nil {
		for _, rule := range object.Document.DualSource.Fields {
			if rule.Name == name && rule.SourceOfRecord == "fusion" && rule.DestinationObjectTypeID == object.ID {
				fusion = true
			}
		}
	}
	if fusion {
		return ResolvePropertyClaims(claims, now, false)
	}
	consensus := slices.Clone(claims)
	for i := range consensus {
		claim := &consensus[i]
		if (claim.EffectiveFrom == nil || *claim.EffectiveFrom <= now) && (claim.EffectiveTo == nil || *claim.EffectiveTo > now) {
			claim.EffectiveFrom = nil
		}
	}
	result, err := ResolvePropertyClaims(consensus, now, false)
	if result.Reason == "effective_time_required" {
		result.Reason = "authority_confirmation_required"
	}
	return result, err
}
