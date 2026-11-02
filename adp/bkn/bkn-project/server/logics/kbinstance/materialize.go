// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

type materializedRecord struct {
	Object *interfaces.DraftObjectType
	Input  InstanceRecord
	ID     string
	Anchor locatedEvidence
}
type fileRows struct {
	build             *interfaces.KBBuild
	draft             *interfaces.Draft
	file              interfaces.KBResourceFile
	document, version string
	objects           map[string]interfaces.DraftDatasetSpec
	relations         map[string]interfaces.DraftDatasetSpec
	roles             map[string]string
	writes            map[string]kb.DatasetWrite
	identities        map[string]map[string]any
}

// MaterializeFile validates the whole proposal before returning a single row.
// Entity properties remain independently evidenced claims until K5 selects a
// current value; one file never overwrites another file's canonical entity.
func MaterializeFile(build *interfaces.KBBuild, index int, draft *interfaces.Draft, input *kb.FileInput, task *interfaces.BknAgentTask) ([]kb.DatasetWrite, error) {
	if build == nil || build.NetworkID == "" || draft == nil || index < 0 || index >= len(build.Files) {
		return nil, ErrInvalidBuild
	}
	file := build.Files[index].File
	currentModel, err := kbdataset.ModelFingerprint(draft)
	if err != nil || currentModel != build.ModelFingerprint || task == nil || task.AgentVersion == nil || *task.AgentVersion != build.AgentVersion || task.TaskID == "" || task.TaskID != build.Files[index].AgentTaskID {
		return nil, ErrInvalidBuild
	}
	digest, err := FileInputDigest(fileRef(file), input)
	if err != nil || digest != build.Files[index].InputDigest {
		return nil, ErrInvalidBuild
	}
	version, err := extractionVersion(task)
	if err != nil {
		return nil, err
	}
	proposal, err := parseFileExtraction(task.Output)
	if err != nil {
		return nil, err
	}
	return materializeProposal(build, index, draft, input, proposal, version, task.CreateTime)
}

func materializeProposal(build *interfaces.KBBuild, index int, draft *interfaces.Draft, input *kb.FileInput, proposal *FileExtraction, version string, recordedAt int64) ([]kb.DatasetWrite, error) {
	file := build.Files[index].File
	b, err := newFileRows(build, index, draft, version)
	if err != nil {
		return nil, err
	}
	document := b.document
	records := map[string]*materializedRecord{}
	ordered := []*materializedRecord{}
	local := []*materializedRecord{}
	for _, record := range proposal.Records {
		object := instanceObject(draft.Items, record.ObjectTypeID)
		if object == nil || record.Reference == "" || len(record.Reference) > 128 || records[record.Reference] != nil || strings.HasPrefix(object.Document.Role, "evidence_") {
			return nil, ErrInvalidBuild
		}
		if object.Document.Role != "entity" && object.Document.Role != "file" && object.Document.Role != "in_document" && object.Document.Role != "fact" && object.Document.Role != "event" {
			return nil, ErrInvalidBuild
		}
		anchor, err := locateInstance(input, record.Anchor, file.IncludeOriginal)
		if err != nil {
			return nil, err
		}
		expected := 0
		for _, property := range object.Properties {
			if ordinaryInstanceProperty(object, property) {
				expected++
				if _, exists := record.Values[property.Name]; !exists {
					return nil, ErrInvalidBuild
				}
			}
		}
		if len(record.Values) != expected {
			return nil, ErrInvalidBuild
		}
		for name, value := range record.Values {
			property := instanceProperty(object, name)
			if !ordinaryInstanceProperty(object, property) || !validInstanceValue(property.Type, value.Value) {
				return nil, ErrInvalidBuild
			}
			if value.Value != nil {
				if _, err = locateInstances(input, value.Evidence, file.IncludeOriginal); err != nil {
					return nil, err
				}
			} else if len(value.Evidence) != 0 {
				return nil, ErrInvalidBuild
			}
		}
		item := &materializedRecord{Object: object, Input: record, Anchor: anchor}
		switch object.Document.Role {
		case "entity":
			key, err := businessKey(object, record)
			if err != nil {
				return nil, err
			}
			item.ID, err = identityDigest("bkn-global-entity-v1", build.NetworkID, object.ID, key)
			if err != nil {
				return nil, err
			}
		case "file":
			global, err := confirmedFileIdentity(draft.Items, object, record, build.NetworkID)
			if err != nil {
				return nil, err
			}
			item.ID, err = FileObjectID(global, document)
			if err != nil {
				return nil, err
			}
		default:
			local = append(local, item)
		}
		records[record.Reference] = item
		ordered = append(ordered, item)
	}
	anchors, err := recordSentenceAnchors(input, local)
	if err != nil {
		return nil, err
	}
	ids, err := AssignSentenceAnchors(build.NetworkID, document, anchors)
	if err != nil {
		return nil, err
	}
	for i, item := range local {
		item.ID = ids[i]
	}
	for _, record := range ordered {
		keys := record.Object.PrimaryKeys
		if record.Object.Document.Role == "file" {
			key, err := kbconcept.FileIdentityProperty(draft.Items, record.Object)
			if err != nil {
				return nil, err
			}
			keys = []string{key}
		} else if record.Object.Document.Role != "entity" {
			keys = []string{record.Object.DisplayKey}
		}
		values := map[string]any{}
		for _, key := range keys {
			if value, exists := record.Input.Values[key]; exists && value.Value != nil {
				values[key] = value.Value
			}
		}
		b.identities[record.Object.ID+"/"+record.ID] = values
	}
	for _, record := range ordered {
		if record.Object.Document.Role != "entity" {
			values := map[string]any{"_id": record.ID, "entity_id": "", "document_id": document, "content_version_id": file.ContentVersionID, "block_id": record.Anchor.BlockID, "source_start": record.Anchor.Start, "source_end": record.Anchor.End, "source_quote": record.Anchor.Quote}
			for name, value := range record.Input.Values {
				values[name] = value.Value
			}
			if record.Object.Document.Role == "fact" || record.Object.Document.Role == "event" {
				if recordedAt <= 0 {
					return nil, ErrInvalidBuild
				}
				values["recorded_at"] = time.UnixMilli(recordedAt).UTC().Format(time.RFC3339Nano)
				values["status"] = "pending_review"
			}
			if err = b.add(b.objects[record.Object.ID], values); err != nil {
				return nil, err
			}
		}
		claimCount := 0
		for name, value := range record.Input.Values {
			if value.Value != nil {
				locations, err := locateInstances(input, value.Evidence, file.IncludeOriginal)
				if err != nil {
					return nil, err
				}
				count, err := b.claim(record.Object.ID, record.ID, name, value.Value, locations)
				if err != nil {
					return nil, err
				}
				claimCount += count
			}
		}
		if record.Object.Document.Role == "entity" && claimCount == 0 {
			return nil, ErrInvalidBuild
		}
	}
	for _, relation := range proposal.Relations {
		if err = b.businessRelation(records, input, relation); err != nil {
			return nil, err
		}
	}
	if err = b.addNative(input); err != nil {
		return nil, err
	}
	writes := make([]kb.DatasetWrite, 0, len(b.writes))
	for _, write := range b.writes {
		writes = append(writes, write)
	}
	if _, err = validateWrites(build, draft, writes); err != nil {
		return nil, err
	}
	return writes, nil
}

func confirmedFileIdentity(items interfaces.DraftItems, file *interfaces.DraftObjectType, record InstanceRecord, network string) (string, error) {
	// Reuse the already human-confirmed field-authority plan. A file's stable
	// business number is never guessed from a name or from extraction order.
	name, err := kbconcept.FileIdentityProperty(items, file)
	if err != nil {
		return "", ErrInvalidBuild
	}
	value, exists := record.Values[name]
	if !exists || value.Value == nil {
		return "", ErrInvalidBuild
	}
	if text, ok := value.Value.(string); ok && strings.TrimSpace(text) == "" {
		return "", ErrInvalidBuild
	}
	raw, err := canonicalBusinessValue(value.Value)
	if err != nil {
		return "", ErrInvalidBuild
	}
	return string(raw), nil
}
func (b *fileRows) add(spec interfaces.DraftDatasetSpec, values map[string]any) error {
	if spec.ResourceID == "" || !spec.Ready || spec.Layout.Mapping["model_fingerprint"] == "" || spec.Layout.Mapping["extraction_version"] == "" {
		return ErrInvalidBuild
	}
	row := map[string]any{}
	for _, field := range spec.Layout.Fields {
		row[field.OriginalName] = nil
	}
	for name, value := range values {
		if _, exists := row[name]; !exists {
			return ErrInvalidBuild
		}
		row[name] = value
	}
	row["model_fingerprint"], row["extraction_version"] = b.build.ModelFingerprint, b.version
	id, ok := row["_id"].(string)
	if !ok || id == "" {
		return ErrInvalidBuild
	}
	key := spec.ResourceID + "/" + id
	if previous, exists := b.writes[key]; exists {
		old, _ := json.Marshal(previous.Row)
		next, _ := json.Marshal(row)
		if string(old) != string(next) {
			return ErrInvalidBuild
		}
		return nil
	}
	if len(b.writes) >= 100000 {
		return ErrInvalidBuild
	}
	b.writes[key] = kb.DatasetWrite{Spec: spec, Row: row}
	return nil
}
func (b *fileRows) claim(typeID, id, name string, value any, locations []locatedEvidence) (int, error) {
	count := 0
	for _, relation := range b.draft.Items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.Document == nil || relation.Document.Role != "evidence_reference" || relation.Source != typeID {
			continue
		}
		switch relation.Target {
		case b.roles["evidence_document"]:
			if err := b.edge(relation, id, b.document, name, value, locations); err != nil {
				return 0, err
			}
			count++
		case b.roles["evidence_block"]:
			if !b.file.IncludeOriginal {
				continue
			}
			blocks := map[string][]locatedEvidence{}
			for _, location := range locations {
				blocks[location.BlockID] = append(blocks[location.BlockID], location)
			}
			for block, proof := range blocks {
				target, err := identityDigest("bkn-source-block-v1", b.document, b.file.ContentVersionID, block)
				if err != nil {
					return 0, err
				}
				if err = b.edge(relation, id, target, name, value, proof); err != nil {
					return 0, err
				}
				count++
			}
		}
	}
	return count, nil
}
func (b *fileRows) edge(relation *interfaces.DraftRelationType, source, target, property string, value any, locations []locatedEvidence) error {
	slices.SortFunc(locations, func(a, c locatedEvidence) int {
		rawA, _ := json.Marshal(a)
		rawC, _ := json.Marshal(c)
		return strings.Compare(string(rawA), string(rawC))
	})
	proof, _ := json.Marshal(locations)
	claim, err := canonicalBusinessValue(value)
	if err != nil {
		return err
	}
	id, err := identityDigest("bkn-source-edge-v1", b.build.NetworkID, relation.ID, source, target, b.document, "property:"+property, "claim:"+string(claim), "evidence:"+string(proof))
	if err != nil {
		return err
	}
	values := map[string]any{"_id": id, "source_record_id": source, "target_record_id": target, "document_id": b.document, "content_version_id": b.file.ContentVersionID, "block_id": "", "source_start": 0, "source_end": 0, "source_quote": "", "media_id": "", "media_occurrence_id": "", "property_name": property, "claim_value": value, "evidence": locations}
	values["source_identity"], values["target_identity"] = b.identities[relation.Source+"/"+source], b.identities[relation.Target+"/"+target]
	if len(locations) > 0 {
		q := locations[0]
		values["block_id"], values["source_start"], values["source_end"], values["source_quote"] = q.BlockID, q.Start, q.End, q.Quote
	}
	return b.add(b.relations[relation.ID], values)
}
func (b *fileRows) businessRelation(records map[string]*materializedRecord, input *kb.FileInput, proposal InstanceRelation) error {
	var model *interfaces.DraftRelationType
	for _, r := range b.draft.Items.RelationTypes {
		if r != nil && r.ID == proposal.RelationTypeID && r.Adopted && r.Document != nil && r.Document.Role == "relation" && r.DocumentRepair == "" {
			model = r
		}
	}
	source, target := records[proposal.Source], records[proposal.Target]
	if model == nil || model.DocumentMapping == nil || source == nil || target == nil || source.Object.ID != model.Source || target.Object.ID != model.Target {
		return ErrInvalidBuild
	}
	m := model.DocumentMapping
	left, right := "", ""
	switch m.Mode {
	case "same_document":
		left, right = m.SourceNameProperty, m.TargetNameProperty
	case "linking":
		left, right = m.MentionProperty, m.IdentityProperty
	default:
		return ErrInvalidBuild
	}
	a, okA := source.Input.Values[left]
	c, okC := target.Input.Values[right]
	if !okA || !okC || a.Value == nil || c.Value == nil {
		return ErrInvalidBuild
	}
	x, err := canonicalBusinessValue(a.Value)
	if err != nil {
		return err
	}
	y, err := canonicalBusinessValue(c.Value)
	if err != nil {
		return err
	}
	if string(x) != string(y) {
		return ErrInvalidBuild
	}
	locations, err := locateInstances(input, proposal.Evidence, b.file.IncludeOriginal)
	if err != nil {
		return err
	}
	return b.edge(model, source.ID, target.ID, "", nil, locations)
}

func newFileRows(build *interfaces.KBBuild, index int, draft *interfaces.Draft, version string) (*fileRows, error) {
	file := build.Files[index].File
	document, err := DocumentID(file.KnowledgeBaseID, file.FileID)
	if err != nil {
		return nil, err
	}
	b := &fileRows{build: build, draft: draft, file: file, document: document, version: version, objects: map[string]interfaces.DraftDatasetSpec{}, relations: map[string]interfaces.DraftDatasetSpec{}, roles: map[string]string{}, writes: map[string]kb.DatasetWrite{}, identities: map[string]map[string]any{}}
	for _, plan := range draft.Items.DatasetPlans {
		if plan != nil && plan.ID == build.GenerationID && plan.ModelFingerprint == build.ModelFingerprint && plan.State == "ready" {
			for _, spec := range plan.Datasets {
				if !spec.Ready {
					return nil, ErrInvalidBuild
				}
				if spec.RelationTypeID != "" {
					b.relations[spec.RelationTypeID] = spec
				} else {
					b.objects[spec.ObjectTypeID] = spec
				}
			}
		}
	}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Adopted && object.Document != nil && strings.HasPrefix(object.Document.Role, "evidence_") {
			if b.roles[object.Document.Role] != "" {
				return nil, ErrInvalidBuild
			}
			b.roles[object.Document.Role] = object.ID
		}
	}
	return b, nil
}

func (b *fileRows) addNative(input *kb.FileInput) error {
	file, draft := b.file, b.draft
	native, err := BuildEvidenceRows(fileRef(file), file.Name, file.IncludeOriginal, input.Blocks, input.Media, input.Text)
	if err != nil {
		return err
	}
	for role, rows := range native.Objects {
		typeID := b.roles[role]
		if typeID == "" && len(rows) > 0 {
			return ErrInvalidBuild
		}
		for _, row := range rows {
			if err = b.add(b.objects[typeID], row); err != nil {
				return err
			}
		}
	}
	for _, edge := range native.Edges {
		found := false
		for _, relation := range draft.Items.RelationTypes {
			if relation != nil && relation.Adopted && relation.Document != nil && relation.Document.Role == "evidence_occurrence" && relation.Source == b.roles[edge.SourceRole] && relation.Target == b.roles[edge.TargetRole] {
				if err = b.edge(relation, edge.SourceID, edge.TargetID, "", nil, nil); err != nil {
					return err
				}
				found = true
			}
		}
		if !found {
			return ErrInvalidBuild
		}
	}
	return nil
}

// NativeEvidenceVersion names a deterministic parser/evidence operation, not a
// fabricated Agent execution. Unsupported image-only content remains reviewable.
const NativeEvidenceVersion = `{"native_evidence":1}`

func MaterializeNativeFile(build *interfaces.KBBuild, index int, draft *interfaces.Draft, input *kb.FileInput) ([]kb.DatasetWrite, error) {
	if build == nil || draft == nil || index < 0 || index >= len(build.Files) {
		return nil, ErrInvalidBuild
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrInvalidBuild
	}
	digest, err := FileInputDigest(fileRef(build.Files[index].File), input)
	if err != nil || digest != build.Files[index].InputDigest {
		return nil, ErrInvalidBuild
	}
	b, err := newFileRows(build, index, draft, NativeEvidenceVersion)
	if err != nil {
		return nil, err
	}
	if err = b.addNative(input); err != nil {
		return nil, err
	}
	writes := make([]kb.DatasetWrite, 0, len(b.writes))
	for _, write := range b.writes {
		writes = append(writes, write)
	}
	if _, err = validateWrites(build, draft, writes); err != nil {
		return nil, err
	}
	return writes, nil
}
