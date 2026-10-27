// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const token = "Bearer t"

var (
	owner  = interfaces.AccountInfo{ID: "u1", Type: "user"}
	author = interfaces.GitAuthor{Name: "u1"}
)

func shouldBeHTTPError(err error, status int, code string) {
	var httpErr *httperrors.HTTPError
	So(errors.As(err, &httpErr), ShouldBeTrue)
	So(httpErr.HTTPCode, ShouldEqual, status)
	So(httpErr.ErrorCode, ShouldEqual, code)
}

type fixture struct {
	svc *projectService
	pa  *pmock.MockProjectAccess
	ra  *pmock.MockReleaseAccess
	bba *pmock.MockBknBackendAccess
	ls  *pmock.MockLineageService
}

func newFixture(ctrl *gomock.Controller) *fixture {
	f := &fixture{
		pa:  pmock.NewMockProjectAccess(ctrl),
		ra:  pmock.NewMockReleaseAccess(ctrl),
		bba: pmock.NewMockBknBackendAccess(ctrl),
		ls:  pmock.NewMockLineageService(ctrl),
	}
	f.svc = &projectService{pa: f.pa, ra: f.ra, bba: f.bba, ls: f.ls}
	return f
}

func Test_ProjectService_CreateProject(t *testing.T) {
	Convey("Test projectService CreateProject\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		f := newFixture(ctrl)
		ctx := context.Background()

		Convey("A project of an existing network starts its lineage and branches off main\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{ID: "orders"}, nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "orders", author).Return(&interfaces.CreateLineageResult{}, nil)
			f.ra.EXPECT().ListReleases(gomock.Any(), interfaces.ReleasesQuery{LineageID: "orders", Limit: 1}).
				Return([]*interfaces.Release{}, nil)
			var branch string
			f.ls.EXPECT().CreateBranch(gomock.Any(), "orders", gomock.Any(), interfaces.GIT_MAIN_BRANCH).
				DoAndReturn(func(_ context.Context, _ string, name string, _ string) (*interfaces.CreateBranchResult, error) {
					branch = name
					return &interfaces.CreateBranchResult{Name: name}, nil
				})
			var stored *interfaces.Project
			f.pa.EXPECT().CreateProject(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, p *interfaces.Project) error { stored = p; return nil })
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), []string{"orders"}).Return(map[string]int64{"orders": 3}, nil)

			entry, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{
				Name: "  Orders  ", KNID: "orders", CatalogIDs: []string{"c1", "c2", "c1"},
			}, owner, author)

			So(err, ShouldBeNil)
			So(stored.Name, ShouldEqual, "Orders")
			So(stored.KNID, ShouldEqual, "orders")
			So(stored.Branch, ShouldEqual, "project/"+stored.ID)
			So(branch, ShouldEqual, stored.Branch)
			So(stored.BaselineNetworkID, ShouldEqual, "orders")
			So(stored.CatalogIDs, ShouldResemble, []string{"c1", "c2"})
			So(stored.Owner, ShouldResemble, owner)
			So(stored.Archived, ShouldBeFalse)
			So(entry.Project, ShouldEqual, stored)
			So(entry.OtherProjects, ShouldEqual, 2)
		})

		Convey("A second project of a network shares its lineage and starts from the newest release\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{ID: "orders"}, nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "orders", author).
				Return(nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_AlreadyExists))
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).
				Return([]*interfaces.Release{{NetworkID: "orders_v1_1_0"}}, nil)
			f.ls.EXPECT().CreateBranch(gomock.Any(), "orders", gomock.Any(), interfaces.GIT_MAIN_BRANCH).
				Return(&interfaces.CreateBranchResult{}, nil)
			var stored *interfaces.Project
			f.pa.EXPECT().CreateProject(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, p *interfaces.Project) error { stored = p; return nil })
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{"orders": 2}, nil)

			entry, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders"}, owner, author)

			So(err, ShouldBeNil)
			So(stored.BaselineNetworkID, ShouldEqual, "orders_v1_1_0")
			So(stored.CatalogIDs, ShouldNotBeNil)
			So(entry.OtherProjects, ShouldEqual, 1)
		})

		Convey("A project of a new network creates the network first\n", func() {
			f.bba.EXPECT().CreateKN(gomock.Any(), token, "", "Orders").Return("01a1-new", nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "01a1-new", author).Return(&interfaces.CreateLineageResult{}, nil)
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{}, nil)
			f.ls.EXPECT().CreateBranch(gomock.Any(), "01a1-new", gomock.Any(), interfaces.GIT_MAIN_BRANCH).
				Return(&interfaces.CreateBranchResult{}, nil)
			f.pa.EXPECT().CreateProject(gomock.Any(), gomock.Any()).Return(nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{"01a1-new": 1}, nil)

			entry, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{
				Name: "Orders", NewKN: &interfaces.NewKNRequest{Name: " Orders "},
			}, owner, author)

			So(err, ShouldBeNil)
			So(entry.KNID, ShouldEqual, "01a1-new")
			So(entry.BaselineNetworkID, ShouldEqual, "01a1-new")
			So(entry.OtherProjects, ShouldEqual, 0)
		})

		Convey("An invalid request is refused before any call\n", func() {
			long := strings.Repeat("名", maxNameRunes+1)
			for _, c := range []struct {
				req  interfaces.CreateProjectRequest
				code string
			}{
				{interfaces.CreateProjectRequest{Name: "  ", KNID: "orders"}, perrors.BknProject_Project_InvalidName},
				{interfaces.CreateProjectRequest{Name: long, KNID: "orders"}, perrors.BknProject_Project_InvalidName},
				{interfaces.CreateProjectRequest{Name: "Orders"}, perrors.BknProject_Project_InvalidNetwork},
				{interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders", NewKN: &interfaces.NewKNRequest{Name: "N"}},
					perrors.BknProject_Project_InvalidNetwork},
				{interfaces.CreateProjectRequest{Name: "Orders", NewKN: &interfaces.NewKNRequest{Name: " "}},
					perrors.BknProject_Project_InvalidNetwork},
				{interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders", CatalogIDs: []string{""}},
					perrors.BknProject_Project_InvalidCatalogIDs},
				{interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders", CatalogIDs: []string{strings.Repeat("c", 41)}},
					perrors.BknProject_Project_InvalidCatalogIDs},
				{interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders", CatalogIDs: make([]string, maxCatalogIDs+1)},
					perrors.BknProject_Project_InvalidCatalogIDs},
			} {
				_, err := f.svc.CreateProject(ctx, token, c.req, owner, author)
				shouldBeHTTPError(err, http.StatusBadRequest, c.code)
			}
		})

		Convey("A network the caller cannot see keeps bkn-backend's status\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusForbidden})

			_, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders"}, owner, author)

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Project_BknBackendRequestFailed)
		})

		Convey("A bkn-backend failure is a bad gateway\n", func() {
			f.bba.EXPECT().CreateKN(gomock.Any(), token, "", "Orders").
				Return("", &interfaces.BknBackendError{HTTPCode: http.StatusInternalServerError})

			_, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{
				Name: "Orders", NewKN: &interfaces.NewKNRequest{Name: "Orders"},
			}, owner, author)

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Project_BknBackendRequestFailed)
		})

		Convey("Any other lineage failure stops before a branch or a row\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "orders", author).
				Return(nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Lineage_BknBackendRequestFailed))

			_, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders"}, owner, author)

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Lineage_BknBackendRequestFailed)
		})

		Convey("A branch failure leaves no project\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "orders", author).Return(&interfaces.CreateLineageResult{}, nil)
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{}, nil)
			f.ls.EXPECT().CreateBranch(gomock.Any(), "orders", gomock.Any(), gomock.Any()).
				Return(nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_BranchAlreadyExists))

			_, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders"}, owner, author)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Lineage_BranchAlreadyExists)
		})

		Convey("A store failure is an internal error\n", func() {
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "orders", author).Return(&interfaces.CreateLineageResult{}, nil)
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

			_, err := f.svc.CreateProject(ctx, token, interfaces.CreateProjectRequest{Name: "Orders", KNID: "orders"}, owner, author)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Project_InternalError)
		})
	})
}

func Test_ProjectService_ListProjects(t *testing.T) {
	Convey("Test projectService ListProjects\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		f := newFixture(ctrl)
		ctx := context.Background()
		p3 := &interfaces.Project{ID: "p3", KNID: "orders", CreateTime: 3}
		p2 := &interfaces.Project{ID: "p2", KNID: "parts", CreateTime: 2}
		p1 := &interfaces.Project{ID: "p1", KNID: "orders", CreateTime: 1, Archived: true}

		Convey("Only projects of visible networks are listed, unarchived by default, a page at a time\n", func() {
			unarchived := false
			f.bba.EXPECT().ListKNIDs(gomock.Any(), token).Return([]string{"orders", "parts"}, nil)
			want := interfaces.ProjectsQuery{KNIDs: []string{"orders", "parts"}, Archived: &unarchived, Name: "ord", Limit: 3}
			f.pa.EXPECT().ListProjects(gomock.Any(), want).Return([]*interfaces.Project{p3, p2, p1}, nil)
			f.pa.EXPECT().CountProjects(gomock.Any(), want).Return(int64(5), nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), []string{"orders", "parts"}).
				Return(map[string]int64{"orders": 2, "parts": 1}, nil)

			list, err := f.svc.ListProjects(ctx, token, "", " ord ", 2, "")

			So(err, ShouldBeNil)
			So(list.Total, ShouldEqual, 5)
			So(len(list.Entries), ShouldEqual, 2)
			So(list.Entries[0].OtherProjects, ShouldEqual, 1)
			So(list.Entries[1].OtherProjects, ShouldEqual, 0)
			So(list.NextCursor, ShouldNotBeNil)

			f.bba.EXPECT().ListKNIDs(gomock.Any(), token).Return([]string{"orders", "parts"}, nil)
			next := want
			next.Cursor = &interfaces.ProjectCursor{CreateTime: 2, ID: "p2"}
			f.pa.EXPECT().ListProjects(gomock.Any(), next).Return([]*interfaces.Project{}, nil)
			f.pa.EXPECT().CountProjects(gomock.Any(), next).Return(int64(5), nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), []string{}).Return(map[string]int64{}, nil)

			page, err := f.svc.ListProjects(ctx, token, "false", "ord", 2, *list.NextCursor)

			So(err, ShouldBeNil)
			So(page.Entries, ShouldNotBeNil)
			So(page.Entries, ShouldBeEmpty)
			So(page.NextCursor, ShouldBeNil)
		})

		Convey("An archived project counts every unarchived project of its network as another\n", func() {
			f.bba.EXPECT().ListKNIDs(gomock.Any(), token).Return([]string{"orders"}, nil)
			f.pa.EXPECT().ListProjects(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, q interfaces.ProjectsQuery) ([]*interfaces.Project, error) {
					So(q.Archived, ShouldBeNil)
					return []*interfaces.Project{p1}, nil
				})
			f.pa.EXPECT().CountProjects(gomock.Any(), gomock.Any()).Return(int64(1), nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), []string{"orders"}).Return(map[string]int64{"orders": 2}, nil)

			list, err := f.svc.ListProjects(ctx, token, "all", "", 20, "")

			So(err, ShouldBeNil)
			So(list.Entries[0].OtherProjects, ShouldEqual, 2)
		})

		Convey("archived=true lists archived projects only\n", func() {
			f.bba.EXPECT().ListKNIDs(gomock.Any(), token).Return([]string{"orders"}, nil)
			f.pa.EXPECT().ListProjects(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, q interfaces.ProjectsQuery) ([]*interfaces.Project, error) {
					So(*q.Archived, ShouldBeTrue)
					return []*interfaces.Project{}, nil
				})
			f.pa.EXPECT().CountProjects(gomock.Any(), gomock.Any()).Return(int64(0), nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)

			_, err := f.svc.ListProjects(ctx, token, "true", "", 20, "")

			So(err, ShouldBeNil)
		})

		Convey("Bad parameters are refused before any call\n", func() {
			_, err := f.svc.ListProjects(ctx, token, "yes", "", 20, "")
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidArchived)

			_, err = f.svc.ListProjects(ctx, token, "", strings.Repeat("n", maxNameRunes+1), 20, "")
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidName)

			for _, limit := range []int{0, interfaces.MAX_PROJECT_LIMIT + 1} {
				_, err = f.svc.ListProjects(ctx, token, "", "", limit, "")
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Limit)
			}
			for _, cursor := range []string{"not base64!", "bm90IGpzb24", encodeCursor(interfaces.ProjectCursor{CreateTime: 1})} {
				_, err = f.svc.ListProjects(ctx, token, "", "", 20, cursor)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Cursor)
			}
		})

		Convey("A bkn-backend failure listing networks is a bad gateway\n", func() {
			f.bba.EXPECT().ListKNIDs(gomock.Any(), token).Return(nil, errors.New("connection refused"))

			_, err := f.svc.ListProjects(ctx, token, "", "", 20, "")

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Project_BknBackendRequestFailed)
		})
	})
}

func Test_ProjectService_GetAndArchive(t *testing.T) {
	Convey("Test projectService GetProject and SetProjectArchived\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		f := newFixture(ctrl)
		ctx := context.Background()
		stored := func() *interfaces.Project {
			return &interfaces.Project{ID: "p1", KNID: "orders", Owner: owner, UpdateTime: 1}
		}

		Convey("A visible project is returned with the other projects of its network\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), []string{"orders"}).Return(map[string]int64{"orders": 4}, nil)

			entry, err := f.svc.GetProject(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(entry.ID, ShouldEqual, "p1")
			So(entry.OtherProjects, ShouldEqual, 3)
		})

		Convey("A project of a network the caller cannot see is not found, like a missing one\n", func() {
			for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
				f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
				f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(nil, &interfaces.BknBackendError{HTTPCode: status})

				_, err := f.svc.GetProject(ctx, token, "p1")

				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
			}
			f.pa.EXPECT().GetProject(gomock.Any(), "none").Return(nil, false, nil)

			_, err := f.svc.GetProject(ctx, token, "none")

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Project_NotFound)
		})

		Convey("Another bkn-backend failure is not taken for invisibility\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusInternalServerError})

			_, err := f.svc.GetProject(ctx, token, "p1")

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Project_BknBackendRequestFailed)
		})

		Convey("The owner archives the project\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.pa.EXPECT().SetProjectArchived(gomock.Any(), "p1", true, gomock.Any()).Return(nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{"orders": 1}, nil)

			entry, err := f.svc.SetProjectArchived(ctx, token, "p1", true, owner)

			So(err, ShouldBeNil)
			So(entry.Archived, ShouldBeTrue)
			So(entry.UpdateTime, ShouldBeGreaterThan, 1)
			So(entry.OtherProjects, ShouldEqual, 1)
		})

		Convey("Archiving an archived project changes nothing\n", func() {
			archived := stored()
			archived.Archived = true
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(archived, true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)

			entry, err := f.svc.SetProjectArchived(ctx, token, "p1", true, owner)

			So(err, ShouldBeNil)
			So(entry.UpdateTime, ShouldEqual, 1)
		})

		Convey("Only the owner may archive\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)

			_, err := f.svc.SetProjectArchived(ctx, token, "p1", true, interfaces.AccountInfo{ID: "u2", Type: "user"})

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
		})

		Convey("An archive write failure is an internal error\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
			f.pa.EXPECT().SetProjectArchived(gomock.Any(), "p1", true, gomock.Any()).Return(errors.New("db down"))

			_, err := f.svc.SetProjectArchived(ctx, token, "p1", true, owner)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Project_InternalError)
		})
	})
}
