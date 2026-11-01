// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/google/uuid"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

type Service struct {
	projects interfaces.ProjectService
	store    interfaces.KBResourceAccess
	source   kb.Source
}

func NewService(projects interfaces.ProjectService, store interfaces.KBResourceAccess, source kb.Source) *Service {
	return &Service{projects, store, source}
}

func (s *Service) owner(ctx context.Context, token, project string, caller interfaces.AccountInfo, open bool) error {
	var entry *interfaces.ProjectEntry
	var err error
	if open {
		entry, err = s.projects.GetProject(ctx, token, project)
	} else {
		entry, _, err = s.projects.GetProjectForStatus(ctx, token, project, caller)
	}
	if err != nil {
		return err
	}
	if entry.Owner != caller {
		return httperrors.NewHTTPError(ctx, 403, perrors.BknProject_Project_NotOwner)
	}
	if open && entry.Archived {
		return httperrors.NewHTTPError(ctx, 409, perrors.BknProject_Requirement_ProjectArchived)
	}
	return nil
}
func (s *Service) Read(ctx context.Context, token, project string) (*interfaces.KBResourceState, error) {
	if _, err := s.projects.GetProject(ctx, token, project); err != nil {
		return nil, err
	}
	state, err := s.store.Read(ctx, project)
	if err != nil {
		return nil, err
	}
	ctx = kb.WithOperatorAuthorization(ctx, token)
	checked := map[string]bool{}
	// Stored filenames/choices are not a bypass of current KB read permission.
	for _, selection := range []*interfaces.KBSelection{state.Current, state.Pending} {
		if selection != nil {
			for _, scope := range selection.Ranges {
				if !checked[scope.KnowledgeBaseID] {
					if _, err = s.source.SummarizeFiles(ctx, kb.SourceScope{KnowledgeBaseID: scope.KnowledgeBaseID}, kb.FileQuery{}); err != nil {
						return nil, err
					}
					checked[scope.KnowledgeBaseID] = true
				}
			}
		}
	}
	return state, nil
}

// Changes is read-only: accepting changes requires another explicit lock decision.
func (s *Service) Changes(ctx context.Context, token, project string) (*interfaces.KBSourceChanges, error) {
	state, err := s.Read(ctx, token, project)
	if err != nil {
		return nil, err
	}
	if state.Current == nil || state.Current.State != "active" {
		return nil, interfaces.ErrKBSelectionConflict
	}
	latest, err := s.files(kb.WithOperatorAuthorization(ctx, token), state.Current.Ranges)
	if err != nil {
		return nil, err
	}
	changes := &interfaces.KBSourceChanges{SelectionID: state.Current.ID, Revision: state.Current.Revision, Added: []interfaces.KBResourceFile{}, Changed: []interfaces.KBResourceFile{}, NoLongerInRange: []interfaces.KBResourceFile{}}
	previous := map[string]interfaces.KBResourceFile{}
	for _, file := range state.Current.Files {
		previous[file.Key()] = file
	}
	seen := map[string]bool{}
	for _, file := range latest {
		seen[file.Key()] = true
		old, found := previous[file.Key()]
		if !found {
			changes.Added = append(changes.Added, file)
		} else if old.ContentVersionID != file.ContentVersionID {
			changes.Changed = append(changes.Changed, file)
		}
	}
	for _, file := range state.Current.Files {
		if !seen[file.Key()] {
			changes.NoLongerInRange = append(changes.NoLongerInRange, file)
		}
	}
	return changes, nil
}

func normalized(request interfaces.KBLockRequest) (interfaces.KBLockRequest, string, error) {
	id, err := uuid.Parse(request.ID)
	if err != nil {
		return request, "", &kb.SourceError{StatusCode: 400}
	}
	request.ID = id.String()
	if request.ExpectedID != "" {
		id, err = uuid.Parse(request.ExpectedID)
		if err != nil {
			return request, "", &kb.SourceError{StatusCode: 400}
		}
		request.ExpectedID = id.String()
	}
	if len(request.Ranges) == 0 {
		return request, "", &kb.SourceError{StatusCode: 400}
	}
	scopes := map[string]interfaces.KBSourceRange{}
	for _, scope := range request.Ranges {
		if scope.KnowledgeBaseID == "" || len(scope.MimeTypes) > 20 {
			return request, "", &kb.SourceError{StatusCode: 400}
		}
		scope.MimeTypes = append([]string{}, scope.MimeTypes...)
		sort.Strings(scope.MimeTypes)
		raw, err := json.Marshal(scope)
		if err != nil {
			return request, "", err
		}
		scopes[string(raw)] = scope
	}
	keys := make([]string, 0, len(scopes))
	for key := range scopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	request.Ranges = []interfaces.KBSourceRange{}
	for _, key := range keys {
		request.Ranges = append(request.Ranges, scopes[key])
	}
	raw, err := json.Marshal(struct {
		Expected string
		Ranges   []interfaces.KBSourceRange
	}{request.ExpectedID, request.Ranges})
	if err != nil {
		return request, "", err
	}
	hash := sha256.Sum256(raw)
	return request, hex.EncodeToString(hash[:]), nil
}

func (s *Service) Lock(ctx context.Context, token, project string, caller interfaces.AccountInfo, request interfaces.KBLockRequest) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, true); err != nil {
		return nil, err
	}
	request, hash, err := normalized(request)
	if err != nil {
		return nil, err
	}
	existing, err := s.store.Get(ctx, project, request.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.InputHash != hash {
			return nil, interfaces.ErrKBSelectionConflict
		}
		return s.complete(ctx, token, project, existing)
	}
	ctx = kb.WithOperatorAuthorization(ctx, token)
	files, err := s.files(ctx, request.Ranges)
	if err != nil {
		return nil, err
	}
	selection := &interfaces.KBSelection{ID: request.ID, ProjectID: project, ExpectedID: request.ExpectedID, InputHash: hash, State: "pending", Revision: 1, Ranges: request.Ranges, Files: files, Samples: interfaces.KBSamplePlan{Mode: "specified"}, Owner: caller, CreateTime: time.Now().UnixMilli()}
	selection.UpdateTime = selection.CreateTime
	selection.Decisions = []interfaces.KBResourceDecision{{Kind: "lock", By: caller, Time: selection.CreateTime}}
	selection.Holds, err = makeHolds(files)
	if err != nil {
		return nil, err
	}
	if err = s.store.Prepare(ctx, selection); err != nil {
		// A concurrent retry must reuse the persisted hold UUIDs, never its own
		// freshly generated UUIDs that did not reach the intent transaction.
		saved, getErr := s.store.Get(ctx, project, request.ID)
		if getErr != nil {
			return nil, getErr
		}
		if saved == nil || saved.InputHash != hash {
			return nil, err
		}
		selection = saved
	}
	return s.complete(ctx, token, project, selection)
}
func (s *Service) Resume(ctx context.Context, token, project string, caller interfaces.AccountInfo, id string) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, true); err != nil {
		return nil, err
	}
	selection, err := s.store.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if selection == nil {
		return nil, interfaces.ErrKBSelectionConflict
	}
	return s.complete(ctx, token, project, selection)
}
func (s *Service) complete(ctx context.Context, token, project string, selection *interfaces.KBSelection) (*interfaces.KBResourceState, error) {
	if selection.State != "pending" && selection.State != "active" {
		return nil, interfaces.ErrKBSelectionConflict
	}
	ctx = kb.WithOperatorAuthorization(ctx, token)
	refs := map[string]kb.VersionRef{}
	for _, file := range selection.Files {
		if file.ContentVersionID != "" {
			refs[file.KnowledgeBaseID+"/"+file.ContentVersionID] = kb.VersionRef{FileID: file.FileID, ContentVersionID: file.ContentVersionID}
		}
	}
	for _, hold := range selection.Holds {
		if hold.Released {
			return nil, interfaces.ErrKBSelectionConflict
		}
		request := kb.HoldRequest{ID: hold.ID, Versions: []kb.VersionRef{}}
		for _, id := range hold.VersionIDs {
			ref, found := refs[hold.KnowledgeBaseID+"/"+id]
			if !found {
				return nil, interfaces.ErrKBSelectionConflict
			}
			request.Versions = append(request.Versions, ref)
		}
		if err := s.source.Hold(ctx, kb.SourceScope{KnowledgeBaseID: hold.KnowledgeBaseID}, request); err != nil {
			return nil, err
		}
	}
	if err := s.store.Activate(ctx, project, selection.ID); err != nil {
		return nil, err
	}
	// Activation commits before retiring previous source holds. Failed cleanup
	// stays durable and retryable, without persisting this request's credential.
	s.cleanup(ctx, project)
	return s.Read(ctx, token, project)
}
func (s *Service) SetSamples(ctx context.Context, token, project string, caller interfaces.AccountInfo, id string, revision int64, plan interfaces.KBSamplePlan) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, true); err != nil {
		return nil, err
	}
	state, err := s.Read(ctx, token, project)
	if err != nil {
		return nil, err
	}
	if state.Current == nil || state.Current.ID != id || state.Current.State != "active" || state.Current.Revision != revision {
		return nil, interfaces.ErrKBSelectionConflict
	}
	files, err := chooseSamples(state.Current.Files, plan, rand.Shuffle)
	if err != nil {
		return nil, err
	}
	if err = s.store.SetSamples(ctx, project, id, revision, files, plan); err != nil {
		return nil, err
	}
	return s.Read(ctx, token, project)
}
func (s *Service) Cancel(ctx context.Context, token, project string, caller interfaces.AccountInfo, id string) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, false); err != nil {
		return nil, err
	}
	if err := s.store.Retire(ctx, project, id); err != nil {
		return nil, err
	}
	s.cleanup(kb.WithOperatorAuthorization(ctx, token), project)
	return s.cleanupCounts(ctx, project)
}
func (s *Service) Clear(ctx context.Context, token, project string, caller interfaces.AccountInfo) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, false); err != nil {
		return nil, err
	}
	if err := s.store.Clear(ctx, project); err != nil {
		return nil, err
	}
	s.cleanup(kb.WithOperatorAuthorization(ctx, token), project)
	return s.cleanupCounts(ctx, project)
}
func (s *Service) Cleanup(ctx context.Context, token, project string, caller interfaces.AccountInfo) (*interfaces.KBResourceState, error) {
	if err := s.owner(ctx, token, project, caller, false); err != nil {
		return nil, err
	}
	s.cleanup(kb.WithOperatorAuthorization(ctx, token), project)
	// Return counts only: cleanup does not confer permission to read filenames.
	return s.cleanupCounts(ctx, project)
}
func (s *Service) cleanupCounts(ctx context.Context, project string) (*interfaces.KBResourceState, error) {
	state, err := s.store.Read(ctx, project)
	if err != nil {
		return nil, err
	}
	return &interfaces.KBResourceState{CleanupPending: state.CleanupPending}, nil
}

func (s *Service) cleanup(ctx context.Context, project string) {
	selections, err := s.store.Cleanup(ctx, project)
	if err != nil {
		return
	}
	for _, selection := range selections {
		if selection.Retained {
			continue
		}
		for _, hold := range selection.Holds {
			if hold.Released {
				continue
			}
			if s.source.ReleaseHold(ctx, kb.SourceScope{KnowledgeBaseID: hold.KnowledgeBaseID}, hold.ID) == nil {
				_ = s.store.MarkReleased(ctx, project, selection.ID, hold.ID)
			}
		}
		_ = s.store.MarkReleased(ctx, project, selection.ID, "")
	}
}

func (s *Service) files(ctx context.Context, ranges []interfaces.KBSourceRange) ([]interfaces.KBResourceFile, error) {
	files := []interfaces.KBResourceFile{}
	byID := map[string]int{}
	for _, scope := range ranges {
		sourceScope := kb.SourceScope{KnowledgeBaseID: scope.KnowledgeBaseID}
		if scope.CatalogID != "" {
			found := false
			cursor := ""
			seen := map[string]bool{}
			for {
				page, err := s.source.ListCatalogs(ctx, sourceScope, kb.CatalogQuery{PageQuery: kb.PageQuery{Limit: 200, Cursor: cursor}})
				if err != nil {
					return nil, err
				}
				for _, catalog := range page.Entries {
					found = found || catalog.ID == scope.CatalogID
				}
				if page.NextCursor == "" {
					break
				}
				if seen[page.NextCursor] {
					return nil, &kb.SourceError{StatusCode: 502}
				}
				seen[page.NextCursor] = true
				cursor = page.NextCursor
			}
			if !found {
				return nil, &kb.SourceError{StatusCode: 404}
			}
		}
		cursor := ""
		seen := map[string]bool{}
		for {
			page, err := s.source.ListFiles(ctx, sourceScope, kb.FileQuery{PageQuery: kb.PageQuery{Limit: 200, Cursor: cursor}, CatalogID: scope.CatalogID, Recursive: scope.Recursive, MimeTypes: scope.MimeTypes, UpdatedSince: scope.UpdatedSince})
			if err != nil {
				return nil, err
			}
			for _, file := range page.Entries {
				selected := interfaces.KBResourceFile{KnowledgeBaseID: scope.KnowledgeBaseID, FileID: file.FileID, ContentVersionID: file.ContentVersionID, CatalogID: file.CatalogID, Name: file.Name, MimeType: file.MimeType, ChunkCount: file.ChunkCount, Size: file.Size, IncludeOriginal: scope.IncludeOriginal}
				if i, duplicate := byID[selected.Key()]; duplicate {
					if files[i].ContentVersionID != selected.ContentVersionID {
						return nil, interfaces.ErrKBSelectionConflict
					}
					files[i].IncludeOriginal = files[i].IncludeOriginal || scope.IncludeOriginal
				} else {
					byID[selected.Key()] = len(files)
					files = append(files, selected)
				}
			}
			if page.NextCursor == "" {
				break
			}
			if seen[page.NextCursor] {
				return nil, &kb.SourceError{StatusCode: 502}
			}
			seen[page.NextCursor] = true
			cursor = page.NextCursor
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Key() < files[j].Key() })
	return files, nil
}
func makeHolds(files []interfaces.KBResourceFile) ([]interfaces.KBHoldGroup, error) {
	groups := map[string][]string{}
	for _, file := range files {
		if file.ContentVersionID != "" {
			groups[file.KnowledgeBaseID] = append(groups[file.KnowledgeBaseID], file.ContentVersionID)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	holds := []interfaces.KBHoldGroup{}
	for _, key := range keys {
		versions := groups[key]
		for len(versions) > 0 {
			count := min(len(versions), 1000)
			id, err := uuid.NewRandom()
			if err != nil {
				return nil, err
			}
			holds = append(holds, interfaces.KBHoldGroup{ID: id.String(), KnowledgeBaseID: key, VersionIDs: append([]string{}, versions[:count]...)})
			versions = versions[count:]
		}
	}
	return holds, nil
}
