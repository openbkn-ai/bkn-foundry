// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"errors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

type memorySelections struct {
	interfaces.KBResourceAccess
	current, pending          *interfaces.KBSelection
	cleanup                   []*interfaces.KBSelection
	events                    *[]string
	failPrepare, errorRelease error
}

func (m *memorySelections) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return &interfaces.KBResourceState{Current: m.current, Pending: m.pending, CleanupPending: int64(len(m.cleanup))}, nil
}
func (m *memorySelections) Get(_ context.Context, _ string, id string) (*interfaces.KBSelection, error) {
	if m.pending != nil && m.pending.ID == id {
		return m.pending, nil
	}
	if m.current != nil && m.current.ID == id {
		return m.current, nil
	}
	return nil, nil
}
func (m *memorySelections) Prepare(_ context.Context, s *interfaces.KBSelection) error {
	*m.events = append(*m.events, "prepare")
	if m.failPrepare != nil {
		return m.failPrepare
	}
	m.pending = s
	return nil
}
func (m *memorySelections) Activate(context.Context, string, string) error {
	*m.events = append(*m.events, "activate")
	m.current = m.pending
	m.current.State = "active"
	m.pending = nil
	return nil
}
func (m *memorySelections) Cleanup(context.Context, string) ([]*interfaces.KBSelection, error) {
	return m.cleanup, nil
}
func (m *memorySelections) Clear(context.Context, string) error {
	if m.current != nil && !m.current.Retained {
		m.current.State = "release_pending"
		m.cleanup = append(m.cleanup, m.current)
	}
	m.current = nil
	return nil
}
func (m *memorySelections) MarkReleased(_ context.Context, _ string, id, hold string) error {
	for _, s := range m.cleanup {
		for i := range s.Holds {
			if s.Holds[i].ID == hold {
				s.Holds[i].Released = true
			}
		}
	}
	if hold == "" {
		all := true
		for _, selection := range m.cleanup {
			for _, group := range selection.Holds {
				all = all && group.Released
			}
		}
		if all {
			m.cleanup = nil
		}
	}
	return nil
}

type sourceStub struct {
	kb.UnavailableSource
	events                       *[]string
	holdErr, releaseErr, readErr error
	held                         []kb.HoldRequest
	files                        func(kb.SourceScope, kb.FileQuery) (kb.FilePage, error)
}

func (s *sourceStub) ListKnowledgeBases(context.Context, kb.SourceScope, kb.PageQuery) (kb.KnowledgeBasePage, error) {
	return kb.KnowledgeBasePage{Entries: []kb.KnowledgeBase{{ID: "1", Name: "Records"}}}, nil
}
func (s *sourceStub) ListCatalogs(context.Context, kb.SourceScope, kb.CatalogQuery) (kb.CatalogPage, error) {
	return kb.CatalogPage{Entries: []kb.Catalog{{ID: "dir", Name: "Documents"}}}, nil
}
func (s *sourceStub) ListFiles(_ context.Context, scope kb.SourceScope, query kb.FileQuery) (kb.FilePage, error) {
	if s.files != nil {
		return s.files(scope, query)
	}
	return kb.FilePage{Entries: []kb.File{{VersionRef: kb.VersionRef{FileID: "5", ContentVersionID: "11"}, CatalogID: "dir", Name: "Agreement"}}}, nil
}
func (s *sourceStub) SummarizeFiles(context.Context, kb.SourceScope, kb.FileQuery) (kb.FileSummary, error) {
	return kb.FileSummary{FileCount: 1, VersionedFileCount: 1}, s.readErr
}
func (s *sourceStub) Hold(ctx context.Context, _ kb.SourceScope, h kb.HoldRequest) error {
	*s.events = append(*s.events, "hold")
	s.held = append(s.held, h)
	return s.holdErr
}
func (s *sourceStub) ReleaseHold(context.Context, kb.SourceScope, string) error {
	*s.events = append(*s.events, "release")
	return s.releaseErr
}

func TestSourceSelectionWorkflow(t *testing.T) {
	Convey("Anonymous retention writes follow durable owner decisions", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		owner := interfaces.AccountInfo{ID: "u", Type: "user"}
		ps.EXPECT().GetProject(gomock.Any(), "Bearer test", "p").AnyTimes().Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner}}, nil)
		ps.EXPECT().GetProjectForStatus(gomock.Any(), "Bearer test", "p", owner).AnyTimes().Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p", Owner: owner}}, false, nil)
		events := []string{}
		store := &memorySelections{events: &events}
		source := &sourceStub{events: &events}
		service := NewService(ps, store, source)
		request := interfaces.KBLockRequest{ID: "00000000-0000-4000-8000-000000000001", Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "dir"}}}
		ctx := context.Background()
		Convey("A failed local intent never writes a remote hold", func() {
			store.failPrepare = errors.New("database unavailable")
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldNotBeNil)
			So(events, ShouldResemble, []string{"prepare"})
		})
		Convey("Locking orders intent, hold and activation", func() {
			state, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			So(events, ShouldResemble, []string{"prepare", "hold", "activate"})
			So(state.Current.Files[0].ContentVersionID, ShouldEqual, "11")
		})
		Convey("An uncertain hold outcome stays pending and resumes the exact UUID/version set", func() {
			source.holdErr = &kb.SourceError{StatusCode: 503}
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldNotBeNil)
			So(store.pending, ShouldNotBeNil)
			So(store.current, ShouldBeNil)
			original := source.held[0]
			source.holdErr = nil
			_, err = service.Resume(ctx, "Bearer test", "p", owner, request.ID)
			So(err, ShouldBeNil)
			So(source.held[1], ShouldResemble, original)
		})
		Convey("Release failure remains retryable; clearing never needs a new query grant", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			source.releaseErr = &kb.SourceError{StatusCode: 503}
			source.readErr = &kb.SourceError{StatusCode: 403}
			state, err := service.Clear(ctx, "Bearer test", "p", owner)
			So(err, ShouldBeNil)
			So(state.CleanupPending, ShouldEqual, 1)
			source.releaseErr = nil
			state, err = service.Cleanup(ctx, "Bearer test", "p", owner)
			So(err, ShouldBeNil)
			So(state.CleanupPending, ShouldEqual, 0)
		})
		Convey("Published references survive clearing", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			store.current.Retained = true
			_, err = service.Clear(ctx, "Bearer test", "p", owner)
			So(err, ShouldBeNil)
			So(events, ShouldNotContain, "release")
		})
		Convey("A partial source scan cannot create an intent or retire existing data", func() {
			source.files = func(_ kb.SourceScope, query kb.FileQuery) (kb.FilePage, error) {
				if query.Cursor == "" {
					return kb.FilePage{NextCursor: "next", Entries: []kb.File{{VersionRef: kb.VersionRef{FileID: "5", ContentVersionID: "11"}}}}, nil
				}
				return kb.FilePage{}, &kb.SourceError{StatusCode: 503}
			}
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldNotBeNil)
			So(events, ShouldBeEmpty)
			So(store.pending, ShouldBeNil)
		})
		Convey("Source changes compare content identities without rewriting the locked selection", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			source.files = func(kb.SourceScope, kb.FileQuery) (kb.FilePage, error) {
				return kb.FilePage{Entries: []kb.File{{VersionRef: kb.VersionRef{FileID: "5", ContentVersionID: "12"}, Name: "Revised agreement"}, {VersionRef: kb.VersionRef{FileID: "6", ContentVersionID: "13"}, Name: "Invoice"}}}, nil
			}
			changes, err := service.Changes(ctx, "Bearer test", "p")
			So(err, ShouldBeNil)
			So(changes.Changed, ShouldHaveLength, 1)
			So(changes.Added, ShouldHaveLength, 1)
			So(changes.NoLongerInRange, ShouldBeEmpty)
			So(store.current.Files[0].ContentVersionID, ShouldEqual, "11")
			So(events, ShouldResemble, []string{"prepare", "hold", "activate"})
		})
		Convey("Directory moves with the same file/version do not count as new content", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			source.files = func(kb.SourceScope, kb.FileQuery) (kb.FilePage, error) {
				return kb.FilePage{Entries: []kb.File{{VersionRef: kb.VersionRef{FileID: "5", ContentVersionID: "11"}, CatalogID: "other"}}}, nil
			}
			changes, err := service.Changes(ctx, "Bearer test", "p")
			So(err, ShouldBeNil)
			So(changes.Changed, ShouldBeEmpty)
			So(changes.Added, ShouldBeEmpty)
		})
		Convey("An incomplete change scan reports an error rather than absent files", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", owner, request)
			So(err, ShouldBeNil)
			source.files = func(kb.SourceScope, kb.FileQuery) (kb.FilePage, error) {
				return kb.FilePage{}, &kb.SourceError{StatusCode: 503}
			}
			changes, err := service.Changes(ctx, "Bearer test", "p")
			So(err, ShouldNotBeNil)
			So(changes, ShouldBeNil)
			So(store.current.Files, ShouldHaveLength, 1)
		})
		Convey("Only the owner may create source retention intents", func() {
			_, err := service.Lock(ctx, "Bearer test", "p", interfaces.AccountInfo{ID: "other", Type: "user"}, request)
			So(err, ShouldNotBeNil)
			So(events, ShouldBeEmpty)
		})
	})
}
