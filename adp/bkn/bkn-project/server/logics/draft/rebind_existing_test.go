// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// existingRegions is the draft's copy of an object type the network already has, reading a table
// of the catalog "sample" that is gone: the catalog was created again. The draft recorded the
// table's display name, which is not its physical name.
func existingRegions() *interfaces.DraftObjectType {
	return &interfaces.DraftObjectType{ID: "regions", Name: "Regions", Adopted: true, Existing: true,
		PrimaryKeys: []string{"region_id"}, Properties: []*interfaces.DraftProperty{{Name: "region_id", MappedField: "region_id"}},
		Table: interfaces.DraftTable{ResourceID: "old_regions", Name: "Sales regions", CatalogID: "cat",
			CatalogName: "sample", SourceName: "regions"}}
}

// confirmedWithExisting is a confirmed draft whose own tables are all there, holding the network's
// object type existingRegions.
func confirmedWithExisting() *interfaces.Draft {
	d := recordedDraft()
	for _, ref := range draftTableRefs(&d.Items) {
		ref.table.ResourceID, ref.table.CatalogID = "new_"+ref.table.Name, "c_new"
	}
	d.Items.ObjectTypes = append(d.Items.ObjectTypes, existingRegions())
	d.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "c1", UpdateTime: d.UpdateTime}
	return d
}

func Test_RebindExistingOnStatus(t *testing.T) {
	Convey("Test the draft's copies of the network's existing items follow their tables created again\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ca := pmock.NewMockCheckAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		ra := pmock.NewMockReleaseAccess(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ra.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&interfaces.DefinitionsPreflight{}, nil).AnyTimes()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: caller, CatalogIDs: []string{"c_new", "c_en"},
		}}
		ps.EXPECT().GetProjectForStatus(gomock.Any(), token, "p1", gomock.Any()).Return(project, false, nil).AnyTimes()
		w := reinstalledWorld()
		// The table the network's object type read, created again under its physical name.
		w.tables = append(w.tables, vegaTableOf("new_regions", "c_new", "Region list", "regions", "region_id"))
		svc := &draftService{setting: testSetting, da: da, ca: ca, ps: ps, ls: ls, ra: ra, rqa: rqa, va: w.mock(ctrl)}
		ctx := context.Background()

		var stored *interfaces.Draft
		store := func(d *interfaces.Draft) {
			data, err := json.Marshal(d)
			So(err, ShouldBeNil)
			stored = &interfaces.Draft{}
			So(json.Unmarshal(data, stored), ShouldBeNil)
		}
		da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
			copied := *stored
			store(&copied)
			return stored, true, nil
		}).AnyTimes()
		var writes int
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, d *interfaces.Draft, read int64) (bool, error) {
				writes++
				store(d)
				return true, nil
			}).AnyTimes()

		Convey("A gone table is bound again by catalog name and physical table name, though its own tables are all there\n", func() {
			store(confirmedWithExisting())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			regions := findObjectType(&stored.Items, "regions")
			So(regions.Existing, ShouldBeTrue)
			So(regions.Table, ShouldResemble, interfaces.DraftTable{ResourceID: "new_regions", Name: "Region list",
				CatalogID: "c_new", CatalogName: "sample", SourceName: "regions"})
			So(regions.Properties[0].MappedField, ShouldEqual, "region_id")
			So(s.DataSources.Reconnected, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
			// What was confirmed read the gone table: it is to be confirmed again.
			So(s.Confirmation.Current, ShouldBeFalse)

			Convey("Reading again writes nothing more\n", func() {
				_, err := svc.GetStatus(ctx, token, "p1", caller)
				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
			})
		})

		Convey("The table the network's object type reads now comes first, in whatever catalog and name\n", func() {
			w.catalogs["c_other"] = "master data"
			w.tables = append(w.tables, vegaTableOf("moved_regions", "c_other", "Region master", "region_master", "region_id"))
			bba := pmock.NewMockBknBackendAccess(ctrl)
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), token, "supply").Return([]*interfaces.KNObjectTypeSource{
				{ID: "regions", DataSource: &interfaces.KNDataSource{Type: interfaces.KN_DATA_SOURCE_RESOURCE, ID: "moved_regions"}},
			}, nil).AnyTimes()
			svc.pc = &logics.ProjectCatalogs{BBA: bba, VA: svc.va, DA: da}
			store(confirmedWithExisting())

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			table := findObjectType(&stored.Items, "regions").Table
			So(table.ResourceID, ShouldEqual, "moved_regions")
			So(table.CatalogID, ShouldEqual, "c_other")
			So(table.SourceName, ShouldEqual, "region_master")
		})

		Convey("A table the network reads that vega does not give is passed over for the name match\n", func() {
			bba := pmock.NewMockBknBackendAccess(ctrl)
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), token, "supply").Return([]*interfaces.KNObjectTypeSource{
				{ID: "regions", DataSource: &interfaces.KNDataSource{Type: interfaces.KN_DATA_SOURCE_RESOURCE, ID: "unreadable"}},
			}, nil).AnyTimes()
			svc.pc = &logics.ProjectCatalogs{BBA: bba, VA: svc.va, DA: da}
			store(confirmedWithExisting())

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(findObjectType(&stored.Items, "regions").Table.ResourceID, ShouldEqual, "new_regions")
		})

		Convey("With no table found the copy stays as it is, blocks nothing, and nothing is written\n", func() {
			w.tables = w.tables[:len(w.tables)-1]
			store(confirmedWithExisting())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			So(findObjectType(&stored.Items, "regions").Table.ResourceID, ShouldEqual, "old_regions")
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
			So(s.Confirmation.Current, ShouldBeTrue)
		})

		Convey("Looked for in vain, in the network and by name, the next reads do not ask again for a while\n", func() {
			w.tables = w.tables[:len(w.tables)-1]
			bba := pmock.NewMockBknBackendAccess(ctrl)
			// The network still reads the table that is gone.
			asked := 0
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), token, "supply").DoAndReturn(
				func(context.Context, string, string) ([]*interfaces.KNObjectTypeSource, error) {
					asked++
					return []*interfaces.KNObjectTypeSource{
						{ID: "regions", DataSource: &interfaces.KNDataSource{Type: interfaces.KN_DATA_SOURCE_RESOURCE, ID: "old_regions"}},
					}, nil
				}).AnyTimes()
			svc.pc = &logics.ProjectCatalogs{BBA: bba, VA: svc.va, DA: da}
			store(confirmedWithExisting())

			read := func() int {
				before := asked
				_, err := svc.GetStatus(ctx, token, "p1", caller)
				So(err, ShouldBeNil)
				return asked - before
			}
			first := read()
			// The status reads the network for other parts too; the search for the gone table is
			// what the later reads leave out.
			So(read(), ShouldEqual, first-1)
			So(read(), ShouldEqual, first-1)
			So(writes, ShouldEqual, 0)
		})

		Convey("Someone not the owner reads it, and nothing is written\n", func() {
			store(confirmedWithExisting())

			_, err := svc.GetStatus(ctx, token, "p1", interfaces.AccountInfo{ID: "u2", Type: "user"})

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
		})
	})
}
