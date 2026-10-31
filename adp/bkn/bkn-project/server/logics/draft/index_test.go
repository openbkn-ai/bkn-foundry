// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_IndexReadiness(t *testing.T) {
	Convey("Test the readiness of the confirmed object types' local indexes\n", t, func() {
		configured := interfaces.VegaTableIndex{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"updated_at"}}
		with := func(index interfaces.VegaTableIndex, status string, name string) interfaces.VegaTableIndex {
			index.LocalStatus, index.IndexName = status, name
			return index
		}
		objectType := func(id string, adopted bool) *interfaces.DraftObjectType {
			return &interfaces.DraftObjectType{ID: id, Name: id, Adopted: adopted,
				Table: interfaces.DraftTable{ResourceID: "r_" + id, Name: id, CatalogID: "c1"}}
		}
		items := &interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{
			objectType("ready", true), objectType("building", true), objectType("stale", true),
			objectType("not_built", true), objectType("no_key", true), objectType("no_fields", true),
			objectType("gone", true), objectType("left_out", false), objectType("rebuilding", true), objectType("no_name", true),
		}}
		tables := map[string]*interfaces.VegaTable{
			"r_ready":      {ID: "r_ready", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_AVAILABLE, "idx")},
			"r_building":   {ID: "r_building", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_UNAVAILABLE, "")},
			"r_stale":      {ID: "r_stale", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_STALE, "idx")},
			"r_not_built":  {ID: "r_not_built", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_UNAVAILABLE, "")},
			"r_no_key":     {ID: "r_no_key", Index: interfaces.VegaTableIndex{IncrementalFields: []string{"updated_at"}, LocalStatus: interfaces.VEGA_LOCAL_STATUS_STALE}},
			"r_no_fields":  {ID: "r_no_fields"},
			"r_rebuilding": {ID: "r_rebuilding", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_AVAILABLE, "idx")},
			// Usable needs the index's name too, as vega-backend reads it.
			"r_no_name": {ID: "r_no_name", Index: with(configured, interfaces.VEGA_LOCAL_STATUS_AVAILABLE, "")},
		}
		tasks := map[string]*interfaces.VegaBuildTask{
			"r_building":   {ID: "t1", ResourceID: "r_building", Status: "running"},
			"r_rebuilding": {ID: "t2", ResourceID: "r_rebuilding", Status: "pending"},
			"r_gone":       {ID: "t3", ResourceID: "r_gone", Status: "running"},
		}

		index := indexReadiness(items, tables, tasks, true)

		states := map[string]string{}
		for _, item := range index.ObjectTypes {
			states[item.ID] = item.State
		}
		So(states, ShouldResemble, map[string]string{
			"ready": interfaces.INDEX_STATE_READY, "building": interfaces.INDEX_STATE_BUILDING,
			"stale": interfaces.INDEX_STATE_STALE, "not_built": interfaces.INDEX_STATE_NOT_BUILT,
			// The configuration is what is missing, even where an old index went stale.
			"no_key": interfaces.INDEX_STATE_NOT_CONFIGURED, "no_fields": interfaces.INDEX_STATE_NOT_CONFIGURED,
			"gone": interfaces.INDEX_STATE_UNREADABLE,
			// An index rebuilt is used until the new one is done.
			"rebuilding": interfaces.INDEX_STATE_READY,
			"no_name":    interfaces.INDEX_STATE_NOT_BUILT,
		})
		byID := map[string]interfaces.ProjectIndexItem{}
		for _, item := range index.ObjectTypes {
			byID[item.ID] = item
		}
		So(byID["no_key"].Missing, ShouldResemble, []string{interfaces.INDEX_FIELD_PRIMARY_KEY})
		So(byID["no_fields"].Missing, ShouldResemble, []string{interfaces.INDEX_FIELD_PRIMARY_KEY, interfaces.INDEX_FIELD_INCREMENTAL})
		So(byID["building"].Task, ShouldResemble, &interfaces.ProjectIndexTask{ID: "t1", Status: "running"})
		So(byID["rebuilding"].Task, ShouldResemble, &interfaces.ProjectIndexTask{ID: "t2", Status: "pending"})
		So(byID["ready"].Table, ShouldResemble, interfaces.DraftTable{ResourceID: "r_ready", Name: "ready", CatalogID: "c1"})
		So(index.Counts, ShouldResemble, map[string]int{
			interfaces.INDEX_STATE_READY: 2, interfaces.INDEX_STATE_BUILDING: 1, interfaces.INDEX_STATE_STALE: 1,
			interfaces.INDEX_STATE_NOT_BUILT: 2, interfaces.INDEX_STATE_NOT_CONFIGURED: 2, interfaces.INDEX_STATE_UNREADABLE: 1,
		})
		So(index.TasksVisible, ShouldBeTrue)
		// Adopted object types only, in the draft's order.
		So(len(index.ObjectTypes), ShouldEqual, 9)
		So(index.ObjectTypes[0].ID, ShouldEqual, "ready")
	})
}

func Test_IndexUpdateField(t *testing.T) {
	Convey("Test what an index item says of its object type's update field\n", t, func() {
		ot := func(incremental string, props ...*interfaces.DraftProperty) *interfaces.DraftObjectType {
			return &interfaces.DraftObjectType{ID: "records", Name: "Records", Adopted: true, IncrementalKey: incremental,
				Table: interfaces.DraftTable{ResourceID: "r_rec", Name: "records", CatalogID: "c1"}, PrimaryKeys: []string{"record_key"},
				Properties: append([]*interfaces.DraftProperty{
					{Name: "record_key", Type: "string", MappedField: "record_key"},
					{Name: "amount", Type: "integer", MappedField: "amount"},
				}, props...)}
		}
		read := func(o *interfaces.DraftObjectType, index interfaces.VegaTableIndex) interfaces.ProjectIndexItem {
			items := &interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{o}}
			return indexReadiness(items, map[string]*interfaces.VegaTable{"r_rec": {ID: "r_rec", Index: index}}, nil, true).ObjectTypes[0]
		}
		standIn := interfaces.VegaTableIndex{PrimaryKeyFields: []string{"record_key"}, IncrementalFields: []string{"record_key"},
			LocalStatus: interfaces.VEGA_LOCAL_STATUS_AVAILABLE, IndexName: "idx"}

		Convey("No date or time property, whatever else it has: the index is only ever rebuilt in full, and nothing is asked\n", func() {
			item := read(ot(""), standIn)

			So(item.State, ShouldEqual, interfaces.INDEX_STATE_READY)
			So(item.FullRebuildOnly, ShouldBeTrue)
			So(item.UpdateField, ShouldResemble, interfaces.ProjectIndexUpdateField{Candidates: []string{}})
		})

		Convey("A time property not chosen yet: the pending item asks for it\n", func() {
			item := read(ot("", &interfaces.DraftProperty{Name: "changed", Type: "timestamp", MappedField: "changed"}), standIn)

			So(item.FullRebuildOnly, ShouldBeTrue)
			So(item.UpdateField, ShouldResemble, interfaces.ProjectIndexUpdateField{Candidates: []string{"changed"}, Pending: true})
		})

		Convey("An update field chosen and configured refreshes by what changed\n", func() {
			item := read(ot("changed", &interfaces.DraftProperty{Name: "changed", Type: "datetime", MappedField: "changed"}),
				interfaces.VegaTableIndex{PrimaryKeyFields: []string{"record_key"}, IncrementalFields: []string{"changed"}})

			So(item.FullRebuildOnly, ShouldBeFalse)
			So(item.UpdateField.Property, ShouldEqual, "changed")
			So(item.UpdateField.Pending, ShouldBeFalse)
		})
	})
}

func Test_GetIndex(t *testing.T) {
	Convey("Test reading the readiness of a project's local indexes\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		iba := pmock.NewMockIndexBuildAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, iba: iba}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		objectType := func(id, catalog string, adopted bool) *interfaces.DraftObjectType {
			return &interfaces.DraftObjectType{ID: id, Name: id, Adopted: adopted,
				Table: interfaces.DraftTable{ResourceID: "r_" + id, Name: id, CatalogID: catalog}}
		}
		draft := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{objectType("orders", "c1", true), objectType("parts", "c2", true),
				objectType("audit", "c1", false), objectType("lines", "c1", true)},
		}}
		configured := interfaces.VegaTableIndex{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"updated_at"},
			LocalStatus: interfaces.VEGA_LOCAL_STATUS_UNAVAILABLE}

		Convey("The adopted object types' tables are read as the caller, and each catalog's tasks once\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders", "r_parts", "r_lines"}).Return([]*interfaces.VegaTable{
				{ID: "r_orders", Index: configured}, {ID: "r_parts", Index: configured}, {ID: "r_lines", Index: configured},
			}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").
				Return([]*interfaces.VegaBuildTask{{ID: "t1", ResourceID: "r_orders", Status: "running"}}, true, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c2").Return(nil, true, nil)

			index, err := svc.GetIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(index.TasksVisible, ShouldBeTrue)
			So(index.Counts, ShouldResemble, map[string]int{interfaces.INDEX_STATE_BUILDING: 1, interfaces.INDEX_STATE_NOT_BUILT: 2})
		})

		Convey("Each object type carries the build the last confirmation started for its table\n", func() {
			confirmed := *draft
			confirmed.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "c0ffee"}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&confirmed, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return([]*interfaces.VegaTable{
				{ID: "r_orders", Index: configured}, {ID: "r_parts", Index: configured},
			}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, gomock.Any()).Return(nil, true, nil).Times(2)
			iba.EXPECT().ListBuilds(gomock.Any(), "p1", "c0ffee").Return([]*interfaces.IndexBuild{
				{ResourceID: "r_orders", ObjectTypeID: "orders", TaskID: "t9", Outcome: interfaces.INDEX_BUILD_CREATED, CreateTime: 5},
				{ResourceID: "r_parts", ObjectTypeID: "parts", Outcome: interfaces.INDEX_BUILD_FAILED, Reason: "vega is down", CreateTime: 6},
			}, nil)

			index, err := svc.GetIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			builds := map[string]*interfaces.ProjectIndexBuild{}
			for _, item := range index.ObjectTypes {
				builds[item.ID] = item.Build
			}
			So(builds, ShouldResemble, map[string]*interfaces.ProjectIndexBuild{
				"orders": {Outcome: interfaces.INDEX_BUILD_CREATED, TaskID: "t9", Time: 5},
				"parts":  {Outcome: interfaces.INDEX_BUILD_FAILED, Reason: "vega is down", Time: 6},
				"lines":  nil,
			})
		})

		Convey("A failure reading the builds leaves them out, the rest stands\n", func() {
			confirmed := *draft
			confirmed.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "c0ffee"}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&confirmed, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return([]*interfaces.VegaTable{
				{ID: "r_orders", Index: configured},
			}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, gomock.Any()).Return(nil, true, nil).Times(2)
			iba.EXPECT().ListBuilds(gomock.Any(), "p1", "c0ffee").Return(nil, errors.New("db is down"))

			index, err := svc.GetIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(index.ObjectTypes[0].Build, ShouldBeNil)
			So(index.Counts[interfaces.INDEX_STATE_NOT_BUILT], ShouldEqual, 1)
		})

		Convey("Build tasks the caller may not list leave building unknown, the rest stands\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return([]*interfaces.VegaTable{
				{ID: "r_orders", Index: configured},
			}, nil)
			// No task_manage on c1: vega-backend would list nothing, which must not read as no task.
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").Return(nil, false, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c2").Return(nil, true, nil)

			index, err := svc.GetIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(index.TasksVisible, ShouldBeFalse)
			So(index.Counts, ShouldResemble, map[string]int{interfaces.INDEX_STATE_NOT_BUILT: 1, interfaces.INDEX_STATE_UNREADABLE: 2})
		})

		Convey("A failure listing build tasks leaves building unknown, the rest stands\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").Return(nil, false, errors.New("vega is down"))
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c2").Return(nil, true, nil)

			index, err := svc.GetIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(index.TasksVisible, ShouldBeFalse)
		})

		Convey("Without a draft there is nothing to read\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			_, err := svc.GetIndex(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
		})

		Convey("A failure reading the tables is an internal error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("vega is down"))

			_, err := svc.GetIndex(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
		})
	})
}
