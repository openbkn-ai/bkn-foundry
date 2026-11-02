// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

type referenceDatasets interface {
	kb.Datasets
	kb.DatasetReferenceRecords
}
type ReferenceService struct {
	Builds   *BuildService
	Store    interfaces.KBReferenceAccess
	Datasets referenceDatasets
}
type ReferenceStatus struct {
	FullReady         bool                  `json:"full_ready"`
	SelectionID       string                `json:"selection_id"`
	SelectionRevision int64                 `json:"selection_revision"`
	DraftUpdateTime   int64                 `json:"draft_update_time"`
	Revision          int64                 `json:"revision"`
	Files             []ReferenceFileStatus `json:"files"`
}
type ReferenceFileStatus struct {
	File       interfaces.KBResourceFile `json:"file"`
	Reviewed   bool                      `json:"reviewed"`
	ReviewedAt int64                     `json:"reviewed_at"`
}
type ReferenceView struct {
	ReferenceStatus
	Input      *kb.FileInput                   `json:"input"`
	Objects    []*interfaces.DraftObjectType   `json:"objects"`
	Relations  []*interfaces.DraftRelationType `json:"relations"`
	Extraction *FileExtraction                 `json:"extraction"`
}
type ReferenceRequest struct {
	SelectionID       string         `json:"selection_id"`
	SelectionRevision int64          `json:"selection_revision"`
	DraftUpdateTime   int64          `json:"draft_update_time"`
	Revision          int64          `json:"revision"`
	FileKey           string         `json:"file"`
	ConfirmComplete   bool           `json:"confirm_complete"`
	Extraction        FileExtraction `json:"extraction"`
}

func referenceID(build *interfaces.KBBuild) string {
	raw, _ := identityDigest("bkn-acceptance-scope-v1", build.ProjectID, build.ModelFingerprint, build.GenerationID, build.SelectionID, strconv.FormatInt(build.SelectionRevision, 10))
	return raw[:32]
}
func (s *ReferenceService) scope(ctx context.Context, token, project string, caller interfaces.AccountInfo) (*interfaces.KBBuild, *interfaces.Draft, *interfaces.KBReference, error) {
	if s.Builds == nil || s.Store == nil || s.Datasets == nil || s.Builds.Drafts == nil || s.Builds.Selections == nil || s.Builds.Runtime == nil {
		return nil, nil, nil, kb.ErrUnavailable
	}
	entry, err := s.Builds.owner(ctx, token, project, caller)
	if err != nil {
		return nil, nil, nil, err
	}
	if entry.Archived {
		return nil, nil, nil, ErrBuildChanged
	}
	selection, err := s.Builds.Selections.Read(ctx, project)
	if err != nil {
		return nil, nil, nil, err
	}
	if selection == nil || selection.Current == nil || selection.Current.Owner != caller {
		return nil, nil, nil, ErrBuildChanged
	}
	draft, found, err := s.Builds.Drafts.GetDraft(ctx, project)
	if err != nil {
		return nil, nil, nil, err
	}
	if !found || draft == nil {
		return nil, nil, nil, ErrBuildChanged
	}
	version, err := s.Builds.Runtime.Version(ctx, caller)
	if err != nil {
		return nil, nil, nil, err
	}
	build, err := PlanFileBuild("reference-review", entry.KNID, "acceptance", version, draft, selection.Current)
	if err != nil {
		return nil, nil, nil, err
	}
	files := []interfaces.KBResourceFile{}
	for _, file := range build.Files {
		files = append(files, file.File)
	}
	if err = s.Builds.sourcePermission(ctx, token, files); err != nil {
		return nil, nil, nil, err
	}
	record, err := s.Store.Get(ctx, project, referenceID(build))
	if err != nil {
		return nil, nil, nil, err
	}
	if record != nil && (record.Owner != caller || record.ModelFingerprint != build.ModelFingerprint || record.SelectionID != build.SelectionID || record.SelectionRevision != build.SelectionRevision || record.GenerationID != build.GenerationID || len(record.Files) != len(build.Files)) {
		return nil, nil, nil, ErrBuildChanged
	}
	if record != nil {
		for i, file := range record.Files {
			if file.File != build.Files[i].File {
				return nil, nil, nil, ErrBuildChanged
			}
		}
	}
	return build, draft, record, nil
}
func referenceStatus(build *interfaces.KBBuild, record *interfaces.KBReference) *ReferenceStatus {
	status := &ReferenceStatus{SelectionID: build.SelectionID, SelectionRevision: build.SelectionRevision, DraftUpdateTime: build.DraftUpdateTime, Files: []ReferenceFileStatus{}}
	if record != nil {
		status.Revision = record.Revision
	}
	for i, file := range build.Files {
		item := ReferenceFileStatus{File: file.File}
		if record != nil {
			item.Reviewed = record.Files[i].RowID != ""
			item.ReviewedAt = record.Files[i].ReviewedAt
		}
		status.Files = append(status.Files, item)
	}
	return status
}
func (s *ReferenceService) Status(ctx context.Context, token, project string, caller interfaces.AccountInfo) (*ReferenceStatus, error) {
	build, _, record, err := s.scope(ctx, token, project, caller)
	if err != nil {
		return nil, err
	}
	status := referenceStatus(build, record)
	err = s.FullReady(ctx, build)
	if err == nil {
		status.FullReady = true
	} else if !errors.Is(err, ErrAcceptanceRequired) {
		return nil, err
	}
	return status, nil
}

func referenceIndex(build *interfaces.KBBuild, file string) (int, error) {
	for i, step := range build.Files {
		if step.File.Key() == file {
			return i, nil
		}
	}
	return -1, &kb.SourceError{StatusCode: 404}
}
func (s *ReferenceService) readInput(ctx context.Context, token string, build *interfaces.KBBuild, index int) (*kb.FileInput, error) {
	source, ok := s.Builds.Source.(kb.EvidenceSource)
	if !ok {
		return nil, kb.ErrUnavailable
	}
	return ReadFileInput(kb.WithOperatorAuthorization(ctx, token), source, fileRef(build.Files[index].File))
}

// View is the independent blind annotation screen. It reads only source and
// previously human-confirmed references, never an extraction task/prediction.
func (s *ReferenceService) View(ctx context.Context, token, project, file string, caller interfaces.AccountInfo) (*ReferenceView, error) {
	build, draft, record, err := s.scope(ctx, token, project, caller)
	if err != nil {
		return nil, err
	}
	index, err := referenceIndex(build, file)
	if err != nil {
		return nil, err
	}
	input, err := s.readInput(ctx, token, build, index)
	if err != nil {
		return nil, err
	}
	view := &ReferenceView{ReferenceStatus: *referenceStatus(build, record), Input: input, Objects: []*interfaces.DraftObjectType{}, Relations: []*interfaces.DraftRelationType{}}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Adopted && object.Document != nil && (object.Document.Role == "entity" || object.Document.Role == "file" || object.Document.Role == "in_document" || object.Document.Role == "fact" || object.Document.Role == "event") {
			view.Objects = append(view.Objects, object)
		}
	}
	for _, relation := range draft.Items.RelationTypes {
		if relation != nil && relation.Adopted && relation.Document != nil && relation.Document.Role == "relation" {
			view.Relations = append(view.Relations, relation)
		}
	}
	if record != nil && record.Files[index].RowID != "" {
		review, err := s.Read(ctx, build, record, index, input)
		if err != nil {
			return nil, err
		}
		view.Extraction = &review.Extraction
		if err = referenceNumericValues(draft, view.Extraction, true); err != nil {
			return nil, err
		}
	}
	return view, nil
}
func referenceSpec(build *interfaces.KBBuild, catalog string) (interfaces.DraftDatasetSpec, error) {
	id := referenceID(build)
	resource, _ := identityDigest("bkn-reference-dataset-v1", build.ProjectID, id)
	object := &interfaces.DraftObjectType{Adopted: true, PrimaryKeys: []string{"_id"}, Document: &interfaces.DraftDocumentModel{Role: "evidence_document"}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true}, {Name: "document_id", Type: "string", ReadOnly: true}, {Name: "content_version_id", Type: "string", ReadOnly: true}, {Name: "reference_review", Type: "text", ReadOnly: true}}}
	layout, err := kbdataset.BuildLayout(object)
	if err != nil {
		return interfaces.DraftDatasetSpec{}, err
	}
	return interfaces.DraftDatasetSpec{Owner: build.Owner, ProjectID: build.ProjectID, GenerationID: id, ObjectTypeID: "acceptance_reference", ResourceID: resource[:32], CatalogID: catalog, Name: "Acceptance references", Layout: *layout, Ready: true}, nil
}
func (s *ReferenceService) Save(ctx context.Context, token, project string, caller interfaces.AccountInfo, req ReferenceRequest) (*ReferenceStatus, error) {
	if !req.ConfirmComplete || req.Revision < 0 {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	build, draft, record, err := s.scope(ctx, token, project, caller)
	if err != nil {
		return nil, err
	}
	if req.SelectionID != build.SelectionID || req.SelectionRevision != build.SelectionRevision || req.DraftUpdateTime != build.DraftUpdateTime {
		return nil, ErrBuildChanged
	}
	index, err := referenceIndex(build, req.FileKey)
	if err != nil {
		return nil, err
	}
	input, err := s.readInput(ctx, token, build, index)
	if err != nil {
		return nil, err
	}
	digest, err := FileInputDigest(fileRef(build.Files[index].File), input)
	if err != nil {
		return nil, err
	}
	build.Files[index].InputDigest = digest
	if err = referenceNumericValues(draft, &req.Extraction, false); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(req.Extraction)
	if err != nil || len(raw) > 256<<10 {
		return nil, &kb.SourceError{StatusCode: 413}
	}
	signature := sha256.Sum256(raw)
	reviewHash := hex.EncodeToString(signature[:])
	if record != nil && record.Files[index].ReviewHash == reviewHash && record.Files[index].InputDigest == digest {
		return referenceStatus(build, record), nil
	}
	if record == nil && req.Revision != 0 || record != nil && record.Revision != req.Revision {
		return nil, ErrBuildChanged
	}
	now := time.Now().UnixMilli()
	review := ReferenceReview{Reviewer: caller, ReviewedAt: now, Revision: req.Revision + 1, Extraction: req.Extraction}
	if _, err = MaterializeReference(build, index, draft, input, review); err != nil {
		return nil, err
	}
	if record == nil {
		catalog, err := s.Datasets.Catalog(ctx, project)
		if err != nil {
			return nil, err
		}
		spec, err := referenceSpec(build, catalog)
		if err != nil {
			return nil, err
		}
		record = &interfaces.KBReference{ID: referenceID(build), ProjectID: project, Owner: caller, ModelFingerprint: build.ModelFingerprint, SelectionID: build.SelectionID, SelectionRevision: build.SelectionRevision, DraftUpdateTime: build.DraftUpdateTime, GenerationID: build.GenerationID, Dataset: spec, Files: []interfaces.KBReferenceFile{}}
		for _, step := range build.Files {
			record.Files = append(record.Files, interfaces.KBReferenceFile{File: step.File})
		}
	}
	rowID, _ := identityDigest("bkn-reviewed-reference-v1", record.ID, req.FileKey, digest, reviewHash, strconv.FormatInt(review.Revision, 10))
	spec := record.Dataset
	spec.Ready = false
	if err = s.Datasets.Ensure(ctx, spec); err != nil {
		return nil, err
	}
	spec.Ready = true
	// Recheck live scope immediately before writing to the dedicated Dataset.
	if _, _, current, err := s.scope(ctx, token, project, caller); err != nil {
		return nil, err
	} else if current != nil && current.Revision != req.Revision {
		return nil, ErrBuildChanged
	}
	document, _ := DocumentID(build.Files[index].File.KnowledgeBaseID, build.Files[index].File.FileID)
	storedReview, err := referenceForStorage(review, input, build.Files[index].File.IncludeOriginal)
	if err != nil {
		return nil, err
	}
	storedPayload, err := json.Marshal(storedReview)
	if err != nil {
		return nil, err
	}
	row := map[string]any{"_id": rowID, "document_id": document, "content_version_id": build.Files[index].File.ContentVersionID, "model_fingerprint": build.ModelFingerprint, "extraction_version": "reviewed-reference-v1", "reference_review": string(storedPayload)}
	if err = s.Datasets.Replace(ctx, spec, row); err != nil {
		return nil, err
	}
	record.Revision, record.DraftUpdateTime, record.UpdateTime = req.Revision+1, build.DraftUpdateTime, max(now, record.UpdateTime+1)
	record.Files[index] = interfaces.KBReferenceFile{File: build.Files[index].File, ReviewRevision: review.Revision, RowID: rowID, InputDigest: digest, ReviewHash: reviewHash, Reviewer: caller, ReviewedAt: now}
	saved, err := s.Store.Put(ctx, record, req.Revision)
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, ErrBuildChanged
	}
	return referenceStatus(build, record), nil
}
func (s *ReferenceService) Read(ctx context.Context, build *interfaces.KBBuild, record *interfaces.KBReference, index int, input *kb.FileInput) (*ReferenceReview, error) {
	if record == nil || index < 0 || index >= len(record.Files) || record.Files[index].RowID == "" || record.ModelFingerprint != build.ModelFingerprint || record.Owner != build.Owner {
		return nil, ErrAcceptanceRequired
	}
	file := record.Files[index]
	digest, err := FileInputDigest(fileRef(file.File), input)
	if err != nil || digest != file.InputDigest {
		return nil, ErrBuildChanged
	}
	row, err := s.Datasets.ReadRow(ctx, record.Dataset, file.RowID)
	if err != nil {
		return nil, err
	}
	document, _ := DocumentID(file.File.KnowledgeBaseID, file.File.FileID)
	if row["model_fingerprint"] != build.ModelFingerprint || row["document_id"] != document || row["content_version_id"] != file.File.ContentVersionID {
		return nil, ErrInvalidBuild
	}
	payload, ok := row["reference_review"].(string)
	if !ok || len(payload) > 1<<20 {
		return nil, ErrInvalidBuild
	}
	raw := []byte(payload)
	var review ReferenceReview
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&review) != nil || review.Reviewer != file.Reviewer || review.ReviewedAt != file.ReviewedAt || review.Revision != file.ReviewRevision {
		return nil, ErrInvalidBuild
	}
	if err = hydrateReference(&review, input, file.File.IncludeOriginal); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(review.Extraction)
	signature := sha256.Sum256(encoded)
	if hex.EncodeToString(signature[:]) != file.ReviewHash {
		return nil, ErrInvalidBuild
	}
	return &review, nil
}

// Ready freezes the independently confirmed reference revision before any
// heldout prediction is submitted. It changes process metadata only.
func (s *ReferenceService) Ready(ctx context.Context, build *interfaces.KBBuild) error {
	if s.Store == nil || build == nil {
		return ErrAcceptanceRequired
	}
	record, err := s.Store.Get(ctx, build.ProjectID, referenceID(build))
	if err != nil {
		return err
	}
	if record == nil || record.Revision < 1 || record.Owner != build.Owner || record.ModelFingerprint != build.ModelFingerprint || record.SelectionID != build.SelectionID || record.SelectionRevision != build.SelectionRevision || record.GenerationID != build.GenerationID {
		return ErrAcceptanceRequired
	}
	heldout := map[string]interfaces.KBResourceFile{}
	for _, file := range build.Files {
		if file.File.SampleRole == "acceptance" {
			heldout[file.File.Key()] = file.File
		}
	}
	if len(heldout) == 0 || len(heldout) != len(record.Files) {
		return ErrAcceptanceRequired
	}
	for _, review := range record.Files {
		if heldout[review.File.Key()] != review.File || review.RowID == "" || review.Reviewer != build.Owner || review.ReviewedAt <= 0 || review.ReviewRevision < 1 || len(review.InputDigest) != 64 || len(review.ReviewHash) != 64 {
			return ErrAcceptanceRequired
		}
	}
	build.ReferenceID, build.ReferenceRevision = record.ID, record.Revision
	return nil
}
func (s *ReferenceService) Evaluate(ctx context.Context, build *interfaces.KBBuild, index int, draft *interfaces.Draft, input *kb.FileInput, writes []kb.DatasetWrite) (*AcceptanceCounts, error) {
	if build == nil || build.Mode != "acceptance" || build.ReferenceID != referenceID(build) || build.ReferenceRevision < 1 || s.Store == nil || s.Datasets == nil {
		return nil, ErrAcceptanceRequired
	}
	record, err := s.Store.Get(ctx, build.ProjectID, build.ReferenceID)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Revision != build.ReferenceRevision || index < 0 || index >= len(record.Files) || index >= len(build.Files) || record.Files[index].File != build.Files[index].File {
		return nil, ErrAcceptanceRequired
	}
	review, err := s.Read(ctx, build, record, index, input)
	if err != nil {
		return nil, err
	}
	expectedWrites, err := MaterializeReference(build, index, draft, input, *review)
	if err != nil {
		return nil, err
	}
	gold, err := assessmentAtoms(draft, expectedWrites)
	if err != nil {
		return nil, err
	}
	actual, err := assessmentAtoms(draft, writes)
	if err != nil {
		return nil, err
	}
	score, err := scoreAtoms(gold, actual)
	if err != nil {
		return nil, err
	}
	return &score, nil
}
func (s *ReferenceService) FullReady(ctx context.Context, build *interfaces.KBBuild) error {
	if err := s.Ready(ctx, build); err != nil {
		return err
	}
	if s.Builds == nil || s.Builds.Store == nil {
		return ErrAcceptanceRequired
	}
	candidates, err := s.Builds.Store.List(ctx, build.ProjectID, 100)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.Mode != "acceptance" || candidate.State != "succeeded" || candidate.Owner != build.Owner || candidate.ModelFingerprint != build.ModelFingerprint || candidate.SelectionID != build.SelectionID || candidate.SelectionRevision != build.SelectionRevision || candidate.GenerationID != build.GenerationID || candidate.AgentVersion != build.AgentVersion || candidate.ReferenceID != build.ReferenceID || candidate.ReferenceRevision != build.ReferenceRevision || candidate.PromptVersion == nil || *candidate.PromptVersion < 1 || candidate.ModelID == "" {
			continue
		}
		heldout := map[string]interfaces.KBResourceFile{}
		for _, step := range build.Files {
			if step.File.SampleRole == "acceptance" {
				heldout[step.File.Key()] = step.File
			}
		}
		if len(candidate.Files) != len(heldout) {
			continue
		}
		total := &AcceptanceCounts{Issues: map[string]int{}}
		valid := true
		for _, file := range candidate.Files {
			if file.State != "succeeded" || heldout[file.File.Key()] != file.File || file.Assessment == nil || file.AgentTaskID == "" || file.AgentVersion == nil || *file.AgentVersion != candidate.AgentVersion || file.PromptVersion == nil || *file.PromptVersion != *candidate.PromptVersion || file.ModelID != candidate.ModelID || len(file.InputDigest) != 64 || len(file.RowsDigest) != 64 {
				valid = false
				break
			}
			delete(heldout, file.File.Key())
			score := file.Assessment
			total.Expected += score.Expected
			total.Predicted += score.Predicted
			total.Correct += score.Correct
			total.ExpectedLinks += score.ExpectedLinks
			total.PredictedLinks += score.PredictedLinks
			total.CorrectLinks += score.CorrectLinks
		}
		if !valid || len(heldout) != 0 {
			continue
		}
		calculateAcceptance(total)
		if total.Passed {
			// Agent versions may retain mutable model defaults. Pin the actual
			// passing execution so the first full-build receipt is checked by
			// AttachFileTask before any extracted row can be written.
			prompt := *candidate.PromptVersion
			build.PromptVersion = &prompt
			build.ModelID, build.Model = candidate.ModelID, candidate.Model
			return nil
		}
	}

	return ErrAcceptanceRequired
}

// Browser editors use decimal text for numeric fields, preserving integers
// beyond JavaScript's safe range. AI predictions retain strict numeric JSON.
func referenceNumericValues(draft *interfaces.Draft, proposal *FileExtraction, editor bool) error {
	for i := range proposal.Records {
		record := &proposal.Records[i]
		object := instanceObject(draft.Items, record.ObjectTypeID)
		if object == nil {
			return ErrInvalidBuild
		}
		for name, value := range record.Values {
			property := instanceProperty(object, name)
			if property == nil {
				return ErrInvalidBuild
			}
			switch property.Type {
			case "json":
				if value.Value != nil {
					if editor {
						raw, err := json.Marshal(value.Value)
						if err != nil {
							return ErrInvalidBuild
						}
						value.Value = string(raw)
					} else if text, ok := value.Value.(string); ok {
						decoder := json.NewDecoder(strings.NewReader(text))
						decoder.UseNumber()
						var parsed any
						if decoder.Decode(&parsed) != nil {
							return ErrInvalidBuild
						}
						var trailing any
						if decoder.Decode(&trailing) != io.EOF {
							return ErrInvalidBuild
						}
						value.Value = parsed
						if parsed == nil {
							value.Evidence = []InstanceCitation{}
						}
					}
				}
			case "integer", "unsigned integer", "float", "decimal":
				if editor {
					if n, ok := value.Value.(json.Number); ok {
						value.Value = n.String()
					}
				} else if text, ok := value.Value.(string); ok {
					number := json.Number(text)
					if !validInstanceValue(property.Type, number) {
						return ErrInvalidBuild
					}
					value.Value = number
				}
			}
			record.Values[name] = value
		}
	}
	return nil
}

// Guard runs before every acceptance step. A reference edit after submission
// cannot silently change the gold set or cause a new heldout inference.
func (s *ReferenceService) Guard(ctx context.Context, build *interfaces.KBBuild) error {
	if build == nil || build.ReferenceID == "" || build.ReferenceRevision < 1 {
		return ErrAcceptanceRequired
	}
	snapshot := *build
	if err := s.Ready(ctx, &snapshot); err != nil {
		return err
	}
	if snapshot.ReferenceID != build.ReferenceID || snapshot.ReferenceRevision != build.ReferenceRevision {
		return ErrAcceptanceRequired
	}
	return nil
}
