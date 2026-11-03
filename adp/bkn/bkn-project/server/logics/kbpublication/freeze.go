// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbpublication captures a release's fixed execution model separately
// from its reviewable Git definitions. Private evidence stays in Vega; restoring
// a snapshot verifies its own complete model rather than overriding a fingerprint.
package kbpublication

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

const DefinitionPath = "project/knowledge-base.json"
const maxSnapshotBytes = 32 << 20

var ErrInvalidSnapshot = errors.New("invalid published knowledge-base model")

// Bundle.Runtime belongs in an immutable owned Dataset row, never Git or a
// process table. Process storage keeps only its resource/row identity and hashes.
type Bundle struct {
	Definition       []byte
	Runtime          []byte
	RuntimeDigest    string
	ModelFingerprint string
}
type runtimeSnapshot struct {
	Schema           int               `json:"schema"`
	DefinitionDigest string            `json:"definition_digest"`
	ModelFingerprint string            `json:"model_fingerprint"`
	Draft            *interfaces.Draft `json:"model"`
}
type rules struct {
	Schema    int            `json:"schema"`
	Objects   []objectRule   `json:"objects"`
	Relations []relationRule `json:"relations"`
}
type objectRule struct {
	ID       string        `json:"id"`
	Document *documentRule `json:"document,omitempty"`
	Fields   []fieldRule   `json:"fields"`
}
type fieldRule struct {
	Name           string `json:"name"`
	Instruction    string `json:"instruction,omitempty"`
	SourceOfRecord string `json:"source_of_record,omitempty"`
}
type documentRule struct {
	Role                 string                       `json:"role"`
	Layer                string                       `json:"layer"`
	TimeProperty         string                       `json:"time_property,omitempty"`
	ParticipantRelations []string                     `json:"participant_relations,omitempty"`
	FileIdentity         string                       `json:"file_identity,omitempty"`
	SentenceIdentity     []string                     `json:"sentence_identity,omitempty"`
	EffectiveFrom        string                       `json:"effective_from,omitempty"`
	EffectiveUntil       string                       `json:"effective_until,omitempty"`
	FactValueProperty    string                       `json:"fact_value_property,omitempty"`
	FactSubjectRelation  string                       `json:"fact_subject_relation,omitempty"`
	DualSource           *interfaces.DualSourceChoice `json:"dual_source,omitempty"`
}
type relationRule struct {
	ID                 string        `json:"id"`
	Document           *documentRule `json:"document,omitempty"`
	Mode               string        `json:"mode,omitempty"`
	MentionProperty    string        `json:"mention_property,omitempty"`
	IdentityProperty   string        `json:"identity_property,omitempty"`
	SourceNameProperty string        `json:"source_name_property,omitempty"`
	TargetNameProperty string        `json:"target_name_property,omitempty"`
}

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Definition is an allowlisted projection of executable modeling choices. New
// process/audit/evidence fields cannot silently become Git content. BKN already
// carries object schemas, physical bindings, labels and endpoint identities.
func Definition(items interfaces.DraftItems) ([]byte, error) {
	result := rules{Schema: 1, Objects: []objectRule{}, Relations: []relationRule{}}
	document := func(value *interfaces.DraftDocumentModel) *documentRule {
		if value == nil {
			return nil
		}
		rule := &documentRule{Role: value.Role, Layer: value.Layer, TimeProperty: value.TimeProperty, ParticipantRelations: append([]string(nil), value.ParticipantRelations...)}
		if value.FactResolution != nil {
			rule.FactValueProperty = value.FactResolution.ValueProperty
			rule.FactSubjectRelation = value.FactResolution.SubjectRelationID
		}
		if value.FileIdentity != nil {
			rule.FileIdentity = value.FileIdentity.Property
		}
		if value.SentenceIdentity != nil {
			rule.SentenceIdentity = append([]string(nil), value.SentenceIdentity.Properties...)
		}
		if value.FilePeriod != nil {
			rule.EffectiveFrom = value.FilePeriod.From
			rule.EffectiveUntil = value.FilePeriod.Until
		}
		if value.DualSource != nil {
			choice := value.DualSource.DualSourceChoice
			rule.DualSource = &choice
		}
		return rule
	}
	for _, object := range items.ObjectTypes {
		if object == nil {
			return nil, ErrInvalidSnapshot
		}
		if !object.Adopted {
			continue
		}
		entry := objectRule{ID: object.ID, Document: document(object.Document), Fields: []fieldRule{}}
		for _, field := range object.Properties {
			if field == nil {
				return nil, ErrInvalidSnapshot
			}
			rule := fieldRule{Name: field.Name, SourceOfRecord: field.SourceOfRecord}
			if field.Extraction != nil {
				rule.Instruction = field.Extraction.Instruction
			}
			entry.Fields = append(entry.Fields, rule)
		}
		slices.SortFunc(entry.Fields, func(a, b fieldRule) int { return cmp.Compare(a.Name, b.Name) })
		result.Objects = append(result.Objects, entry)
	}
	for _, relation := range items.RelationTypes {
		if relation == nil {
			return nil, ErrInvalidSnapshot
		}
		if !relation.Adopted {
			continue
		}
		entry := relationRule{ID: relation.ID, Document: document(relation.Document)}
		if mapping := relation.DocumentMapping; mapping != nil {
			entry.Mode = mapping.Mode
			entry.MentionProperty = mapping.MentionProperty
			entry.IdentityProperty = mapping.IdentityProperty
			entry.SourceNameProperty = mapping.SourceNameProperty
			entry.TargetNameProperty = mapping.TargetNameProperty
		}
		result.Relations = append(result.Relations, entry)
	}
	slices.SortFunc(result.Objects, func(a, b objectRule) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(result.Relations, func(a, b relationRule) int { return cmp.Compare(a.ID, b.ID) })
	return json.MarshalIndent(result, "", "  ")
}

func Freeze(draft *interfaces.Draft, plan *interfaces.DraftDatasetPlan) (*Bundle, error) {
	if draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY || draft.UpdateTime <= 0 || draft.Items.Confirmation == nil || draft.Items.Confirmation.UpdateTime != draft.UpdateTime || draft.Items.Confirmation.Commit == "" || plan == nil || plan.State != "ready" {
		return nil, ErrInvalidSnapshot
	}
	for _, spec := range plan.Datasets {
		if !spec.Ready {
			return nil, ErrInvalidSnapshot
		}
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != plan.ModelFingerprint {
		return nil, ErrInvalidSnapshot
	}
	bound, err := kbdataset.BindPreview(draft, plan)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	// Persist the exact adopted execution model, without unrelated process
	// decisions, credentials, candidate queues or earlier preparation attempts.
	model := &interfaces.Draft{ProjectID: draft.ProjectID, Status: draft.Status, UpdateTime: draft.UpdateTime, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{}, RelationTypes: []*interfaces.DraftRelationType{}, DatasetPlans: []*interfaces.DraftDatasetPlan{plan}, Confirmation: draft.Items.Confirmation}}
	for _, object := range bound.Items.ObjectTypes {
		if object != nil && object.Adopted {
			model.Items.ObjectTypes = append(model.Items.ObjectTypes, object)
		}
	}
	for _, relation := range bound.Items.RelationTypes {
		if relation != nil && relation.Adopted {
			model.Items.RelationTypes = append(model.Items.RelationTypes, relation)
		}
	}
	definition, err := Definition(model.Items)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(runtimeSnapshot{Schema: 1, DefinitionDigest: digest(definition), ModelFingerprint: fingerprint, Draft: model})
	if err != nil || len(raw) > maxSnapshotBytes {
		return nil, ErrInvalidSnapshot
	}
	// This also verifies that serialization preserved the complete execution model.
	sum := digest(raw)
	if _, err := Restore(raw, sum, fingerprint); err != nil {
		return nil, err
	}
	return &Bundle{Definition: definition, Runtime: raw, RuntimeDigest: sum, ModelFingerprint: fingerprint}, nil
}

func Restore(raw []byte, expectedDigest, expectedFingerprint string) (*interfaces.Draft, error) {
	if len(raw) == 0 || len(raw) > maxSnapshotBytes || digest(raw) != expectedDigest {
		return nil, ErrInvalidSnapshot
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot runtimeSnapshot
	if decoder.Decode(&snapshot) != nil || snapshot.Schema != 1 || snapshot.ModelFingerprint != expectedFingerprint || snapshot.Draft == nil {
		return nil, ErrInvalidSnapshot
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalidSnapshot
	}
	fingerprint, err := kbdataset.ModelFingerprint(snapshot.Draft)
	if err != nil || fingerprint != snapshot.ModelFingerprint {
		return nil, ErrInvalidSnapshot
	}
	if len(snapshot.Draft.Items.DatasetPlans) != 1 {
		return nil, ErrInvalidSnapshot
	}
	plan := snapshot.Draft.Items.DatasetPlans[0]
	if plan == nil || plan.State != "ready" || plan.ModelFingerprint != fingerprint {
		return nil, ErrInvalidSnapshot
	}
	for _, spec := range plan.Datasets {
		if !spec.Ready {
			return nil, ErrInvalidSnapshot
		}
	}
	if _, err := kbdataset.BindPreview(snapshot.Draft, plan); err != nil {
		return nil, ErrInvalidSnapshot
	}
	definition, err := Definition(snapshot.Draft.Items)
	if err != nil || digest(definition) != snapshot.DefinitionDigest {
		return nil, ErrInvalidSnapshot
	}
	return snapshot.Draft, nil
}
