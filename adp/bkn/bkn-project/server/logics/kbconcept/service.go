// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"
	"encoding/json"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type ExtractRequest struct {
	SelectionID       string `json:"selection_id"`
	SelectionRevision int64  `json:"selection_revision"`
	DraftUpdateTime   int64  `json:"draft_update_time"`
}
type ExtractResult struct {
	Draft          *interfaces.Draft `json:"draft"`
	AddedObjects   int               `json:"added_objects"`
	AddedRelations int               `json:"added_relations"`
}
type Service struct {
	projects   interfaces.ProjectService
	selections interfaces.KBResourceAccess
	drafts     interfaces.DraftAccess
	source     kb.Source
	concepts   kb.Concepts
}

func NewService(projects interfaces.ProjectService, selections interfaces.KBResourceAccess, drafts interfaces.DraftAccess, source kb.Source, concepts kb.Concepts) *Service {
	return &Service{projects, selections, drafts, source, concepts}
}
func conflict() error { return &kb.SourceError{StatusCode: 409} }

// Extract runs only on the interactive operator path. The remote read and Agent
// calls hold no database locks. Verified proposals join the existing draft by
// an atomic snapshot guard; adoption and Dataset creation are later decisions.
func (s *Service) Extract(ctx context.Context, token, project string, caller interfaces.AccountInfo, req ExtractRequest) (*ExtractResult, error) {
	if req.SelectionID == "" || req.SelectionRevision < 1 || req.DraftUpdateTime < 0 || caller.ID == "" || caller.Type != "user" {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	if s.projects == nil || s.selections == nil || s.drafts == nil || s.concepts == nil {
		return nil, kb.ErrUnavailable
	}
	reader, ok := s.source.(kb.SampleSource)
	if !ok {
		return nil, kb.ErrUnavailable
	}
	writer, ok := s.drafts.(interfaces.DocumentDraftAccess)
	if !ok {
		return nil, kb.ErrUnavailable
	}
	entry, err := s.projects.GetProject(ctx, token, project)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Project == nil {
		return nil, &kb.SourceError{StatusCode: 404}
	}
	if entry.Owner != caller {
		return nil, &kb.SourceError{StatusCode: 403}
	}
	if entry.Archived {
		return nil, conflict()
	}
	state, err := s.selections.Read(ctx, project)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.ID != req.SelectionID || state.Current.Revision != req.SelectionRevision {
		return nil, conflict()
	}
	draft, exists, err := s.drafts.GetDraft(ctx, project)
	if err != nil {
		return nil, err
	}
	if exists && (draft == nil || draft.Status != interfaces.DRAFT_STATUS_READY || draft.UpdateTime != req.DraftUpdateTime || draft.Items.Adding != nil) {
		return nil, conflict()
	}
	if !exists && req.DraftUpdateTime != 0 {
		return nil, conflict()
	}
	guard := interfaces.DocumentDraftGuard{Owner: caller, SelectionID: req.SelectionID, SelectionRevision: req.SelectionRevision, DraftExists: exists, DraftUpdateTime: req.DraftUpdateTime}
	if exists {
		raw, err := json.Marshal(draft)
		if err != nil {
			return nil, err
		}
		draft = &interfaces.Draft{}
		if err = json.Unmarshal(raw, draft); err != nil {
			return nil, err
		}
	} else {
		draft = &interfaces.Draft{ProjectID: project, Status: interfaces.DRAFT_STATUS_READY, Drafter: caller, CreateTime: time.Now().UnixMilli(), Items: interfaces.DraftItems{Inputs: &interfaces.DraftInputs{NamingLanguage: entry.NamingLanguage}}}
	}
	ctx = kb.WithOperatorAuthorization(ctx, token)
	if err = CheckDraftSources(ctx, s.source, draft.Items); err != nil {
		return nil, err
	}
	input, err := LoadSamples(ctx, reader, state.Current, entry.NamingLanguage)
	if err != nil {
		return nil, err
	}
	input.Request.Caller = caller
	// Pass a separate copy: a provider cannot mutate the draft used for merging.
	raw, err := json.Marshal(draft.Items.ObjectTypes)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &input.Request.ExistingObjects); err != nil {
		return nil, err
	}
	proposal, err := s.concepts.Extract(ctx, input.Request)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// All pinned versions and their current access are checked again after AI.
	// Never fall back to latest content or proxy identity when a version is gone.
	refs := make([]kb.SampleVersion, 0, len(input.Files))
	for _, file := range input.Files {
		refs = append(refs, kb.SampleVersion{KnowledgeBaseID: file.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.FileID, ContentVersionID: file.ContentVersionID}})
	}
	if _, err = reader.ReadSampleChunks(ctx, refs, kb.PageQuery{Limit: 1}); err != nil {
		return nil, err
	}
	if len(proposal.ObjectTypes) == 0 && len(proposal.RelationTypes) == 0 {
		if !exists {
			draft = nil
		}
		return &ExtractResult{Draft: draft}, nil
	}
	model, err := ValidateModel(input, draft.Items, proposal)
	if err != nil {
		return nil, err
	}
	addedObjects, addedRelations := len(model.ObjectTypes), len(model.RelationTypes)
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, model.ObjectTypes...)
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, model.RelationTypes...)
	evidence, err := BuildEvidence(input, draft.Items)
	if err != nil {
		return nil, err
	}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, evidence.Model.ObjectTypes...)
	draft.Items.RelationTypes = append(draft.Items.RelationTypes, evidence.Model.RelationTypes...)
	addedObjects += len(evidence.Model.ObjectTypes)
	addedRelations += len(evidence.Model.RelationTypes)
	draft.UpdateTime = max(time.Now().UnixMilli(), guard.DraftUpdateTime+1)
	if err = CheckDraftSources(ctx, s.source, draft.Items); err != nil {
		return nil, err
	}
	written, err := writer.CommitDocumentDraft(ctx, draft, guard)
	if err != nil {
		return nil, err
	}
	if !written {
		return nil, conflict()
	}
	return &ExtractResult{Draft: draft, AddedObjects: addedObjects, AddedRelations: addedRelations}, nil
}

// SamplePreviewFragment is request-local editor evidence, never a stored sample
// snapshot. Quote is carried once as Text; the native citation keeps locations.
type SamplePreviewFragment struct {
	Text     string                         `json:"text"`
	FileName string                         `json:"file_name"`
	Citation interfaces.DraftSourceCitation `json:"citation"`
}
type SamplePreview struct {
	SelectionID       string                  `json:"selection_id"`
	SelectionRevision int64                   `json:"selection_revision"`
	Fragments         []SamplePreviewFragment `json:"fragments"`
}

func (s *Service) Samples(ctx context.Context, token, project string, caller interfaces.AccountInfo, selectionID string, revision int64) (*SamplePreview, error) {
	if selectionID == "" || revision < 1 || caller.ID == "" || caller.Type != "user" {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	if s.projects == nil || s.selections == nil {
		return nil, kb.ErrUnavailable
	}
	reader, ok := s.source.(kb.SampleSource)
	if !ok {
		return nil, kb.ErrUnavailable
	}
	entry, err := s.projects.GetProject(ctx, token, project)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Project == nil {
		return nil, &kb.SourceError{StatusCode: 404}
	}
	if entry.Owner != caller {
		return nil, &kb.SourceError{StatusCode: 403}
	}
	if entry.Archived {
		return nil, conflict()
	}
	state, err := s.selections.Read(ctx, project)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Current == nil || state.Current.ID != selectionID || state.Current.Revision != revision {
		return nil, conflict()
	}
	input, err := LoadSamples(kb.WithOperatorAuthorization(ctx, token), reader, state.Current, entry.NamingLanguage)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, file := range input.Files {
		names[versionKey(file.KnowledgeBaseID, file.FileID, file.ContentVersionID)] = file.Name
	}
	result := &SamplePreview{SelectionID: selectionID, SelectionRevision: revision, Fragments: []SamplePreviewFragment{}}
	for _, fragment := range input.Request.Samples {
		q := fragment.Citation
		result.Fragments = append(result.Fragments, SamplePreviewFragment{Text: fragment.Text, FileName: names[versionKey(q.KnowledgeBaseID, q.FileID, q.ContentVersionID)], Citation: interfaces.DraftSourceCitation{KnowledgeBaseID: q.KnowledgeBaseID, FileID: q.FileID, ContentVersionID: q.ContentVersionID, BlockID: q.BlockID, Start: q.Start, End: q.End}})
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, ErrInvalidSample
	}
	if len(raw) > 1<<20 {
		return nil, ErrSampleLimit
	}
	return result, nil
}
