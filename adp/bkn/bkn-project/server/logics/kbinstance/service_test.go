// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"testing"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"go.uber.org/mock/gomock"
)

type serviceQueue struct {
	processMemory
	created int
}

func (s *serviceQueue) Get(ctx context.Context, project, id string) (*interfaces.KBBuildRecord, error) {
	if s.record == nil {
		return nil, nil
	}
	return s.processMemory.Get(ctx, project, id)
}
func (s *serviceQueue) Create(ctx context.Context, r *interfaces.KBBuildRecord) (bool, error) {
	s.created++
	return s.processMemory.Create(ctx, r)
}
func (s *serviceQueue) List(context.Context, string, int) ([]*interfaces.KBBuild, error) {
	if s.record == nil {
		return []*interfaces.KBBuild{}, nil
	}
	return []*interfaces.KBBuild{s.record.Build}, nil
}
func (s *serviceQueue) Runnable(context.Context, int) ([]interfaces.KBBuildKey, error) {
	return nil, nil
}
func (s *serviceQueue) Cancel(context.Context, string, string, interfaces.AccountInfo, int64) (bool, error) {
	s.record.Build.State = "cancel_pending"
	return true, nil
}

type serviceSource struct {
	kb.UnavailableSource
	calls int
	err   error
}

func (s *serviceSource) SummarizeFiles(ctx context.Context, _ kb.SourceScope, _ kb.FileQuery) (kb.FileSummary, error) {
	s.calls++
	if kb.OperatorAuthorization(ctx) != "Bearer operator-token" {
		return kb.FileSummary{}, &kb.SourceError{StatusCode: 401}
	}
	return kb.FileSummary{}, s.err
}
func serviceFixture(t *testing.T) (*BuildService, *serviceQueue, *serviceSource, BuildRequest, *interfaces.Draft, *pmock.MockDraftAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	projects := pmock.NewMockProjectService(ctrl)
	selections := pmock.NewMockKBResourceAccess(ctrl)
	drafts := pmock.NewMockDraftAccess(ctrl)
	draft, selection := buildPlanFixture(t)
	entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "project", KNID: "lineage", Owner: selection.Owner}}
	projects.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer operator-token", "project", selection.Owner).Return(entry, false, nil).AnyTimes()
	selections.EXPECT().Read(gomock.Any(), "project").Return(&interfaces.KBResourceState{Current: selection}, nil).AnyTimes()
	queue := &serviceQueue{}
	source := &serviceSource{}
	s := &BuildService{Projects: projects, Selections: selections, Drafts: drafts, Store: queue, Source: source, Runtime: &fileRuntime{}}
	req := BuildRequest{ID: uuid.NewString(), Mode: "debug", SelectionID: selection.ID, SelectionRevision: selection.Revision, DraftUpdateTime: draft.UpdateTime, AgentVersion: 3, ConfirmDelegation: true}
	return s, queue, source, req, draft, drafts
}
func TestBuildStartPersistsOnlyOneScopedIntentAndNoRequestCredential(t *testing.T) {
	s, queue, source, req, draft, drafts := serviceFixture(t)
	drafts.EXPECT().GetDraft(gomock.Any(), "project").Return(draft, true, nil).Times(1)
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	for i := 0; i < 2; i++ {
		build, err := s.Start(context.Background(), "Bearer operator-token", "project", owner, req)
		if err != nil || build.NetworkID != "lineage" || len(build.Files) != 1 || build.Files[0].File.SampleRole != "debug" {
			t.Fatal("wrong build intent", err)
		}
	}
	if queue.created != 1 || source.calls != 2 || queue.record.Identity.GrantID == "" || queue.record.Identity.PrincipalID != "" {
		t.Fatal("duplicate intent or premature external delegation")
	}
	req.Mode = "acceptance"
	if _, err := s.Start(context.Background(), "Bearer operator-token", "project", owner, req); err == nil {
		t.Fatal("operation id reused for different immutable scope")
	}
}
func TestBuildStartCannotInventFullReadinessOrSkipExplicitDelegation(t *testing.T) {
	for _, scenario := range []string{"not_confirmed", "full_without_review", "revoked", "draft_changed"} {
		t.Run(scenario, func(t *testing.T) {
			s, queue, source, req, draft, drafts := serviceFixture(t)
			switch scenario {
			case "not_confirmed":
				req.ConfirmDelegation = false
			case "full_without_review":
				req.Mode = "full"
				drafts.EXPECT().GetDraft(gomock.Any(), "project").Return(draft, true, nil)
			case "revoked":
				source.err = &kb.SourceError{StatusCode: 403}
			case "draft_changed":
				draft.UpdateTime++
				drafts.EXPECT().GetDraft(gomock.Any(), "project").Return(draft, true, nil)
			}
			if _, err := s.Start(context.Background(), "Bearer operator-token", "project", interfaces.AccountInfo{ID: "owner", Type: "user"}, req); err == nil || queue.created != 0 {
				t.Fatal("unapproved or stale build started")
			}
		})
	}
}
func TestCancellationRemainsAvailableAfterSourcePermissionRevocation(t *testing.T) {
	s, queue, source, _, draft, _ := serviceFixture(t)
	queue.record = &interfaces.KBBuildRecord{Build: &interfaces.KBBuild{ID: "job", ProjectID: draft.ProjectID}}
	source.err = &kb.SourceError{StatusCode: 403}
	if err := s.Cancel(context.Background(), "Bearer operator-token", "project", "job", interfaces.AccountInfo{ID: "owner", Type: "user"}); err != nil || source.calls != 0 || queue.record.Build.State != "cancel_pending" {
		t.Fatal("revocation prevented cleanup", err)
	}
}
