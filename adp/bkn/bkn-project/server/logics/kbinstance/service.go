// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type BuildRequest struct {
	ID                string `json:"operation_id"`
	Mode              string `json:"mode"`
	SelectionID       string `json:"selection_id"`
	SelectionRevision int64  `json:"selection_revision"`
	DraftUpdateTime   int64  `json:"draft_update_time"`
	AgentVersion      int    `json:"agent_version"`
	ConfirmDelegation bool   `json:"confirm_delegation"`
}
type BuildService struct {
	Projects   interfaces.ProjectService
	Selections interfaces.KBResourceAccess
	Drafts     interfaces.DraftAccess
	Store      interfaces.KBBuildQueue
	Source     kb.Source
	Runtime    kb.BuildRuntime
	// FullReady is installed by the acceptance-review controller. A missing
	// assessment is never interpreted as perfect extraction or linking scores.
	FullReady      func(context.Context, *interfaces.KBBuild) error
	ReferenceReady func(context.Context, *interfaces.KBBuild) error
}

func (s *BuildService) Settings(ctx context.Context, token, project string, caller interfaces.AccountInfo) (map[string]int, error) {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return nil, err
	}
	if s.Runtime == nil {
		return nil, kb.ErrUnavailable
	}
	version, err := s.Runtime.Version(ctx, caller)
	if err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, kb.ErrUnavailable
	}
	return map[string]int{"agent_version": version, "max_files": 1000}, nil
}

func (s *BuildService) owner(ctx context.Context, token, project string, caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	if s.Projects == nil || s.Store == nil || caller.ID == "" || caller.Type != "user" {
		return nil, kb.ErrUnavailable
	}
	entry, _, err := s.Projects.GetProjectForStatus(ctx, token, project, caller)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Project == nil {
		return nil, &kb.SourceError{StatusCode: 404}
	}
	if entry.Owner != caller {
		return nil, &kb.SourceError{StatusCode: 403}
	}
	return entry, nil
}
func (s *BuildService) sourcePermission(ctx context.Context, token string, files []interfaces.KBResourceFile) error {
	if s.Source == nil {
		return kb.ErrUnavailable
	}
	seen := map[string]bool{}
	ctx = kb.WithOperatorAuthorization(ctx, token)
	for _, file := range files {
		if !seen[file.KnowledgeBaseID] {
			if _, err := s.Source.SummarizeFiles(ctx, kb.SourceScope{KnowledgeBaseID: file.KnowledgeBaseID}, kb.FileQuery{}); err != nil {
				return err
			}
			seen[file.KnowledgeBaseID] = true
		}
	}
	return nil
}
func (s *BuildService) Start(ctx context.Context, token, project string, caller interfaces.AccountInfo, req BuildRequest) (*interfaces.KBBuild, error) {
	operation, err := uuid.Parse(req.ID)
	if err != nil || req.ID != operation.String() || !req.ConfirmDelegation || req.AgentVersion < 0 || req.SelectionID == "" || req.SelectionRevision < 1 || req.DraftUpdateTime < 1 || (req.Mode != "debug" && req.Mode != "acceptance" && req.Mode != "full") {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	entry, err := s.owner(ctx, token, project, caller)
	if err != nil {
		return nil, err
	}
	if entry.Archived || s.Selections == nil || s.Drafts == nil {
		return nil, ErrBuildChanged
	}
	state, err := s.Selections.Read(ctx, project)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.ID != req.SelectionID || state.Current.Revision != req.SelectionRevision || state.Current.Owner != caller {
		return nil, ErrBuildChanged
	}
	if err = s.sourcePermission(ctx, token, state.Current.Files); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(struct {
		Project string
		Owner   interfaces.AccountInfo
		Request BuildRequest
	}{project, caller, req})
	hash := sha256.Sum256(raw)
	inputHash := hex.EncodeToString(hash[:])
	existing, err := s.Store.Get(ctx, project, req.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.InputHash != inputHash {
			return nil, ErrBuildChanged
		}
		return existing.Build, nil
	}
	if s.Runtime == nil {
		return nil, kb.ErrUnavailable
	}
	version, err := s.Runtime.Version(ctx, caller)
	if err != nil {
		return nil, err
	}
	if version < 1 || (req.AgentVersion > 0 && req.AgentVersion != version) {
		return nil, ErrBuildChanged
	}
	req.AgentVersion = version
	draft, found, err := s.Drafts.GetDraft(ctx, project)
	if err != nil {
		return nil, err
	}
	if !found || draft == nil || draft.UpdateTime != req.DraftUpdateTime {
		return nil, ErrBuildChanged
	}
	build, err := PlanFileBuild(req.ID, entry.KNID, req.Mode, req.AgentVersion, draft, state.Current)
	if err != nil {
		return nil, err
	}
	if req.Mode == "acceptance" || req.Mode == "full" {
		if s.ReferenceReady == nil {
			return nil, ErrAcceptanceRequired
		}
		if err = s.ReferenceReady(ctx, build); err != nil {
			return nil, err
		}
	}
	if req.Mode == "full" {
		if s.FullReady == nil {
			return nil, ErrAcceptanceRequired
		}
		if err = s.FullReady(ctx, build); err != nil {
			return nil, err
		}
	}
	prior, err := s.Store.List(ctx, project, 100)
	if err != nil {
		return nil, err
	}
	ReuseCompleted(build, prior)
	grant := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bkn-project-build-grant-v1/"+project+"/"+req.ID)).String()
	record := &interfaces.KBBuildRecord{Build: build, InputHash: inputHash, Identity: interfaces.KBBuildIdentity{GrantID: grant}}
	created, err := s.Store.Create(ctx, record)
	if err != nil {
		return nil, err
	}
	if !created {
		previous, readErr := s.Store.Get(ctx, project, req.ID)
		if readErr != nil {
			return nil, readErr
		}
		if previous != nil && previous.InputHash == inputHash {
			return previous.Build, nil
		}
		return nil, ErrBuildChanged
	}
	return build, nil
}
func (s *BuildService) List(ctx context.Context, token, project string, caller interfaces.AccountInfo) ([]*interfaces.KBBuild, error) {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return nil, err
	}
	builds, err := s.Store.List(ctx, project, 30)
	if err != nil {
		return nil, err
	}
	files := []interfaces.KBResourceFile{}
	for _, build := range builds {
		for _, file := range build.Files {
			files = append(files, file.File)
		}
	}
	if err = s.sourcePermission(ctx, token, files); err != nil {
		return nil, err
	}
	return builds, nil
}
func (s *BuildService) Cancel(ctx context.Context, token, project, id string, caller interfaces.AccountInfo) error {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return err
	}
	ok, err := s.Store.Cancel(ctx, project, id, caller, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if !ok {
		return &kb.SourceError{StatusCode: 404}
	}
	return nil
}

// CancelAll remains available after source access is revoked. It returns only
// a process count, never filenames or previously accessible source metadata.
func (s *BuildService) CancelAll(ctx context.Context, token, project string, caller interfaces.AccountInfo) (int, error) {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return 0, err
	}
	builds, err := s.Store.List(ctx, project, 100)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, build := range builds {
		if build.State == "succeeded" || build.State == "cancelled" || build.State == "needs_review" {
			continue
		}
		ok, err := s.Store.Cancel(ctx, project, build.ID, caller, time.Now().UnixMilli())
		if err != nil {
			return count, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}
