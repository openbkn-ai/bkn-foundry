// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// editionProjects is an edition's project hooks that delete and package what they own.
type editionProjects struct {
	deleted, deletingRows []string
	exported              map[string]map[string][]byte
	imported              map[string]map[string][]byte
	replace               map[string]string
}

func (e *editionProjects) Archiving(context.Context, *sql.Tx, string, int64) error          { return nil }
func (e *editionProjects) Archived(context.Context, string, string, interfaces.AccountInfo) {}
func (e *editionProjects) Deleted(_ context.Context, _ string, projectID string, _ interfaces.AccountInfo) {
	e.deleted = append(e.deleted, projectID)
}
func (e *editionProjects) DeletingRows(_ context.Context, _ *sql.Tx, projectID string) error {
	e.deletingRows = append(e.deletingRows, projectID)
	return nil
}
func (e *editionProjects) ExportParts(context.Context, string, string, interfaces.AccountInfo) (map[string]map[string][]byte, error) {
	return e.exported, nil
}
func (e *editionProjects) ImportParts(_ context.Context, _ string, _ string, parts map[string]map[string][]byte,
	replace map[string]string, _ interfaces.AccountInfo) error {
	e.imported, e.replace = parts, replace
	return nil
}

func Test_ManageProject(t *testing.T) {
	Convey("Renaming and deleting a project\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		f := newFixture(ctrl)
		adm := pmock.NewMockAdminAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		f.svc.adm, f.svc.gs = adm, gs
		ctx := context.Background()
		other := interfaces.AccountInfo{ID: "u2", Type: "user"}
		stored := func() *interfaces.Project {
			return &interfaces.Project{ID: "p1", Name: "Old", KNID: "orders", Branch: "project/p1", Owner: owner, UpdateTime: 1}
		}
		visible := func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{}, nil)
		}

		Convey("The owner renames; a blank or long name is refused before any read\n", func() {
			visible()
			f.pa.EXPECT().RenameProject(gomock.Any(), "p1", "New", gomock.Any()).Return(nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)

			entry, err := f.svc.RenameProject(ctx, token, "p1", "  New ", owner)
			So(err, ShouldBeNil)
			So(entry.Name, ShouldEqual, "New")

			for _, name := range []string{" ", strings.Repeat("名", maxNameRunes+1)} {
				_, err = f.svc.RenameProject(ctx, token, "p1", name, owner)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidName)
			}
		})

		Convey("An administrator may rename another's project; anyone else may not\n", func() {
			visible()
			adm.EXPECT().IsAdmin(gomock.Any(), token).Return(true)
			f.pa.EXPECT().RenameProject(gomock.Any(), "p1", "New", gomock.Any()).Return(nil)
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)
			_, err := f.svc.RenameProject(ctx, token, "p1", "New", other)
			So(err, ShouldBeNil)

			visible()
			adm.EXPECT().IsAdmin(gomock.Any(), token).Return(false)
			_, err = f.svc.RenameProject(ctx, token, "p1", "New", other)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
		})

		Convey("Deleting withdraws the project's prepared release, then deletes rows and branch\n", func() {
			hooks := &editionProjects{}
			defer projecthooks.SetForTest(projecthooks.Hooks{Projects: hooks})()
			visible()
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{
				{ProjectID: "p2", Version: "1.0.0", DraftRevision: 3},
				{ProjectID: "p1", Version: "1.1.0", DraftRevision: 7},
			}, nil)
			f.kva.EXPECT().GetVersioning(gomock.Any(), token, "orders").
				Return(&interfaces.KNVersioning{RunningVersion: 4, Draft: interfaces.KNDraftState{Revision: 7}}, nil)
			reset := f.kva.EXPECT().ResetDraft(gomock.Any(), token, "orders", 4, int64(7), true).Return(nil)
			del := f.pa.EXPECT().DeleteProject(gomock.Any(), "p1").Return(nil).After(reset)
			gs.EXPECT().DeleteBranch(gomock.Any(), "orders", "project/p1").Return(nil).After(del)

			So(f.svc.DeleteProject(ctx, token, "p1", owner), ShouldBeNil)
			So(hooks.deleted, ShouldResemble, []string{"p1"})
		})

		Convey("A draft edited since preparing stays; a branch that will not go is no failure\n", func() {
			visible()
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return([]*interfaces.Release{{ProjectID: "p1", DraftRevision: 7}}, nil)
			f.kva.EXPECT().GetVersioning(gomock.Any(), token, "orders").
				Return(&interfaces.KNVersioning{RunningVersion: 4, Draft: interfaces.KNDraftState{Revision: 8}}, nil)
			f.pa.EXPECT().DeleteProject(gomock.Any(), "p1").Return(nil)
			gs.EXPECT().DeleteBranch(gomock.Any(), "orders", "project/p1").Return(errors.New("locked"))

			So(f.svc.DeleteProject(ctx, token, "p1", owner), ShouldBeNil)
		})

		Convey("A release being published, an edition refusing or someone else stops the delete\n", func() {
			visible()
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).
				Return([]*interfaces.Release{{ProjectID: "p1", PublishRequest: &interfaces.KNVersionPublish{}}}, nil)
			shouldBeHTTPError(f.svc.DeleteProject(ctx, token, "p1", owner), http.StatusConflict,
				perrors.BknProject_Project_ReleasePublishing)

			hooks := &editionProjects{}
			defer projecthooks.SetForTest(projecthooks.Hooks{Projects: hooks})()
			visible()
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			f.pa.EXPECT().DeleteProject(gomock.Any(), "p1").Return(errors.New("db down"))
			shouldBeHTTPError(f.svc.DeleteProject(ctx, token, "p1", owner), http.StatusInternalServerError,
				perrors.BknProject_Project_InternalError)
			So(hooks.deleted, ShouldBeEmpty)

			visible()
			adm.EXPECT().IsAdmin(gomock.Any(), token).Return(false)
			shouldBeHTTPError(f.svc.DeleteProject(ctx, token, "p1", other), http.StatusForbidden,
				perrors.BknProject_Project_NotOwner)
		})

		Convey("The owner deletes a project whose network is gone, without touching the network\n", func() {
			f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(stored(), true, nil)
			f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").
				Return(nil, &interfaces.BknBackendError{HTTPCode: http.StatusNotFound})
			f.pa.EXPECT().DeleteProject(gomock.Any(), "p1").Return(nil)
			gs.EXPECT().DeleteBranch(gomock.Any(), "orders", "project/p1").Return(nil)

			So(f.svc.DeleteProject(ctx, token, "p1", owner), ShouldBeNil)
		})

		Convey("Manageable: own projects without asking, others as the caller administers\n", func() {
			mine := &interfaces.ProjectEntry{Project: &interfaces.Project{Owner: owner}}
			theirs := &interfaces.ProjectEntry{Project: &interfaces.Project{Owner: other}}
			adm.EXPECT().IsAdmin(gomock.Any(), token).Return(true).Times(1)

			f.svc.MarkManageable(ctx, token, owner, mine, theirs, &interfaces.ProjectEntry{Project: &interfaces.Project{Owner: other}})
			So(mine.CanManage, ShouldBeTrue)
			So(theirs.CanManage, ShouldBeTrue)

			f.svc.MarkManageable(ctx, token, owner, mine)
			So(mine.CanManage, ShouldBeTrue)
		})
	})
}

func Test_ProjectPackage(t *testing.T) {
	Convey("Exporting a project and importing its package\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		f := newFixture(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		f.svc.gs, f.svc.da, f.svc.rqa = gs, da, rqa
		ctx := context.Background()
		hooks := &editionProjects{exported: map[string]map[string][]byte{"terms": {"terms.json": []byte(`[]`)}, "../x": {"a": nil}}}
		defer projecthooks.SetForTest(projecthooks.Hooks{Projects: hooks})()

		project := &interfaces.Project{ID: "p1", Name: "订单/项目", KNID: "orders", Branch: "project/p1", Owner: owner,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}
		f.pa.EXPECT().GetProject(gomock.Any(), "p1").Return(project, true, nil)
		f.bba.EXPECT().GetKN(gomock.Any(), token, "orders").Return(&interfaces.KnowledgeNetwork{ID: "orders", Name: "Orders"}, nil).Times(2)
		f.va.EXPECT().CatalogNames(gomock.Any(), owner, []string{"c1"}).Return(map[string]string{"c1": "ERP"}, nil)
		rqa.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
			{ID: "d1", Name: "prd.md", DocType: "prd", Format: "markdown", Content: "# Orders", SHA256: "x"},
		}, nil)
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{
			Status: interfaces.REQUIREMENT_STATUS_READY, Version: 3,
			Inputs: &interfaces.RequirementInputs{CatalogIDs: []string{"c1"}, Documents: []interfaces.RequirementInputDocument{{ID: "d1"}}},
		}, true, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{{ID: "order", Name: "Order"},
				// An item whose id and name equal a catalog and a table id keeps them.
				{ID: "c1", Name: "t1", Table: interfaces.DraftTable{ResourceID: "t1", CatalogID: "c1", Name: "orders"}}},
			Inputs:       &interfaces.DraftInputs{CatalogIDs: []string{"c1"}, Tables: []interfaces.DraftInputTable{{ResourceID: "t1"}}},
			Confirmation: &interfaces.DraftConfirmation{Commit: "abc", Files: []string{"object_types/order.bkn"}},
		}}, true, nil)
		rqa.EXPECT().GetSupplement(gomock.Any(), "p1").Return(&interfaces.RequirementSupplement{Text: "Planning only."}, nil)
		rqa.EXPECT().ListConversations(gomock.Any(), "p1", interfaces.REQUIREMENT_PAGE_KINETIC).Return(
			[]*interfaces.RequirementConversation{{ID: "c1", ProjectID: "p1", Page: interfaces.REQUIREMENT_PAGE_KINETIC, Title: "Why?",
				Turns: []*interfaces.RequirementConversationTurn{{ID: "t1", Question: "Why?", Status: interfaces.REQUIREMENT_TURN_PENDING,
					RunID: "r1", By: interfaces.AccountInfo{ID: "u0"}}}, Creator: interfaces.AccountInfo{ID: "u0"}}}, nil)
		rqa.EXPECT().ListConversations(gomock.Any(), "p1", interfaces.REQUIREMENT_PAGE_SCENARIOS).Return(nil, nil)
		f.va.EXPECT().GetTables(gomock.Any(), owner, []string{"t1"}).Return([]*interfaces.VegaTable{{ID: "t1", CatalogID: "c1", Name: "orders"}}, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "orders", "abc").Return(map[string][]byte{
			"object_types/order.bkn": []byte("order"), "object_types/other.bkn": []byte("other"),
		}, nil)

		pkg, name, err := f.svc.ExportProject(ctx, token, "p1", owner)
		So(err, ShouldBeNil)
		So(name, ShouldEqual, "订单_项目.bknproj")

		preview, err := f.svc.PreviewPackage(ctx, pkg)
		So(err, ShouldBeNil)
		So(preview.Name, ShouldEqual, "订单/项目")
		So(preview.NamingLanguage, ShouldEqual, interfaces.NAMING_LANGUAGE_EN_US)
		So(preview.KN, ShouldResemble, interfaces.PackageNetwork{ID: "orders", Name: "Orders"})
		So(preview.Catalogs, ShouldResemble, []interfaces.PackageCatalog{{ID: "c1", Name: "ERP"}})
		So(preview.Contents, ShouldResemble, interfaces.PackageContents{Documents: 1, Requirement: true, Draft: true,
			ConfirmedFiles: 1, Editions: []string{"terms"}, Supplement: true, Conversations: 1})

		Convey("A package whose material does not read is refused before anything is created\n", func() {
			_, err := readMaterial(ctx, map[string][]byte{packageDraft: []byte(`{"object_types":"x"}`)})
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)
		})

		Convey("Every catalog of the package must be mapped\n", func() {
			_, err := f.svc.ImportPackage(ctx, token, pkg, interfaces.ImportProjectOptions{KNID: "orders"}, owner, author)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidCatalogMapping)
		})

		Convey("Import creates the project in the package's language, ids of this environment in place\n", func() {
			f.va.EXPECT().MissingCatalogs(gomock.Any(), owner, []string{"c9"}).Return(nil, nil)
			f.kva.EXPECT().CreateVersionedKN(gomock.Any(), token, "", "Copy").Return("kn2", nil)
			f.ls.EXPECT().CreateLineage(gomock.Any(), token, "kn2", author).Return(&interfaces.CreateLineageResult{}, nil)
			f.ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
			f.ls.EXPECT().CreateBranch(gomock.Any(), "kn2", gomock.Any(), interfaces.GIT_MAIN_BRANCH).Return(&interfaces.CreateBranchResult{}, nil)
			var created *interfaces.Project
			f.pa.EXPECT().CreateProject(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *interfaces.Project) error {
				created = p
				return nil
			})
			f.pa.EXPECT().CountUnarchivedProjects(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)
			f.va.EXPECT().ListTables(gomock.Any(), owner, "c9").Return([]*interfaces.VegaTable{{ID: "t9", Name: "orders"}}, nil)
			var doc *interfaces.RequirementDocument
			rqa.EXPECT().CreateDocument(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d *interfaces.RequirementDocument) (bool, string, error) {
				doc = d
				return true, "", nil
			})
			rqa.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), int64(0), int64(0)).Return(true, nil)
			var req *interfaces.Requirement
			rqa.EXPECT().FinishDrafting(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, r *interfaces.Requirement) (bool, error) {
				req = r
				return true, nil
			})
			var supplement *interfaces.RequirementSupplement
			rqa.EXPECT().SetSupplement(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, s *interfaces.RequirementSupplement) error {
					supplement = s
					return nil
				})
			var conversation *interfaces.RequirementConversation
			rqa.EXPECT().CreateConversation(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, c *interfaces.RequirementConversation) error {
					conversation = c
					return nil
				})
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), int64(0), false).Return(true, nil)
			var draft *interfaces.Draft
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d *interfaces.Draft) error {
				draft = d
				return nil
			})

			entry, err := f.svc.ImportPackage(ctx, token, pkg, interfaces.ImportProjectOptions{
				Name: "Copy", NewKN: &interfaces.NewKNRequest{Name: "Copy"}, CatalogMapping: map[string]string{"c1": "c9"},
			}, owner, author)

			So(err, ShouldBeNil)
			So(entry.Name, ShouldEqual, "Copy")
			So(created.NamingLanguage, ShouldEqual, interfaces.NAMING_LANGUAGE_EN_US)
			So(created.CatalogIDs, ShouldResemble, []string{"c9"})
			So(doc.ProjectID, ShouldEqual, created.ID)
			So(doc.ID, ShouldNotEqual, "d1")
			So(doc.Content, ShouldEqual, "# Orders")
			So(req.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(req.Inputs.CatalogIDs, ShouldResemble, []string{"c9"})
			So(req.Inputs.Documents[0].ID, ShouldEqual, doc.ID)
			So(draft.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
			So(draft.Items.Confirmation, ShouldBeNil)
			So(draft.Items.Inputs.Tables[0].ResourceID, ShouldEqual, "t9")
			So(draft.Items.ObjectTypes[0].Name, ShouldEqual, "Order")
			So(draft.Items.ObjectTypes[1].ID, ShouldEqual, "c1")
			So(draft.Items.ObjectTypes[1].Name, ShouldEqual, "t1")
			So(draft.Items.ObjectTypes[1].Table, ShouldResemble, interfaces.DraftTable{ResourceID: "t9", CatalogID: "c9", Name: "orders"})
			So(hooks.imported["terms"]["terms.json"], ShouldResemble, []byte(`[]`))
			// The supplement note and conversations come along, the importer's, a turn left unanswered failed.
			So(supplement.Text, ShouldEqual, "Planning only.")
			So(conversation.ProjectID, ShouldEqual, created.ID)
			So(conversation.ID, ShouldNotEqual, "c1")
			So(conversation.Creator, ShouldResemble, owner)
			So(conversation.Turns[0].Status, ShouldEqual, interfaces.REQUIREMENT_TURN_FAILED)
			So(hooks.replace["t1"], ShouldEqual, "t9")
		})
	})
}

func Test_RewriteIDs(t *testing.T) {
	Convey("Only id fields are rewritten, by what they name\n", t, func() {
		ids := idRewrite{catalogs: map[string]string{"orders": "c9"}, tables: map[string]string{"t1": "t9"},
			documents: map[string]string{"d1": "d9"}}
		out := map[string]any{}
		So(unmarshalRewritten([]byte(`{"id":"orders","name":"t1","catalog_ids":["orders"],"catalogs_unlisted":["orders"],
			"table":{"resource_id":"t1","catalog_id":"orders"},"conflicts":[{"suggested_document_id":"d1","sides":[{"document_id":"d1"}]}],
			"inputs":{"documents":[{"id":"d1"}]},"rows":12345678901234}`), ids, &out), ShouldBeNil)
		b, _ := json.Marshal(out)
		So(string(b), ShouldEqual, `{"catalog_ids":["c9"],"catalogs_unlisted":["c9"],"conflicts":[{"sides":[{"document_id":"d9"}],`+
			`"suggested_document_id":"d9"}],"id":"orders","inputs":{"documents":[{"id":"d9"}]},"name":"t1","rows":12345678901234,`+
			`"table":{"catalog_id":"c9","resource_id":"t9"}}`)
	})
}

func Test_ReadPackage(t *testing.T) {
	Convey("A package is refused when it is not one, of another version, too large or tampered with\n", t, func() {
		ctx := context.Background()
		zipOf := func(files map[string]string) []byte {
			buf := &bytes.Buffer{}
			zw := zip.NewWriter(buf)
			for name, body := range files {
				w, _ := zw.Create(name)
				_, _ = w.Write([]byte(body))
			}
			_ = zw.Close()
			return buf.Bytes()
		}
		manifest := func(version int) string {
			b, _ := json.Marshal(interfaces.PackageManifest{Format: interfaces.PROJECT_PACKAGE_FORMAT, FormatVersion: version, Name: "P",
				Files: map[string]string{}})
			return string(b)
		}

		_, _, err := readPackage(ctx, []byte("not a zip"))
		shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)

		_, _, err = readPackage(ctx, zipOf(map[string]string{"other.json": "{}"}))
		shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)

		_, _, err = readPackage(ctx, zipOf(map[string]string{"manifest.json": manifest(2)}))
		shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_UnsupportedPackageVersion)

		_, _, err = readPackage(ctx, zipOf(map[string]string{"manifest.json": manifest(1), "model/draft.json": "{}"}))
		shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)

		_, _, err = readPackage(ctx, zipOf(map[string]string{"manifest.json": manifest(1), "../evil": "x"}))
		shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Project_InvalidPackage)

		_, _, err = readPackage(ctx, make([]byte, interfaces.PROJECT_PACKAGE_MAX_BYTES+1))
		shouldBeHTTPError(err, http.StatusRequestEntityTooLarge, perrors.BknProject_Project_PackageTooLarge)

		m, _, err := readPackage(ctx, zipOf(map[string]string{"manifest.json": manifest(1)}))
		So(err, ShouldBeNil)
		So(m.NamingLanguage, ShouldEqual, interfaces.NAMING_LANGUAGE_ZH_CN)
	})
}
