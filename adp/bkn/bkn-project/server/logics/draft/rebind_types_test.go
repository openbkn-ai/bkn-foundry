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
)

// typedTable is a table of the catalog with columns of the given names and types, name then type.
func typedTable(id, catalogID, name, sourceName string, columns ...string) *interfaces.VegaTable {
	t := &interfaces.VegaTable{ID: id, CatalogID: catalogID, Name: name, SourceName: sourceName}
	for i := 0; i+1 < len(columns); i += 2 {
		t.Columns = append(t.Columns, interfaces.VegaColumn{Name: columns[i], Type: columns[i+1]})
	}
	return t
}

// regionsWithFields is existingRegions with string properties mapping region_id, its key, plus
// population and code.
func regionsWithFields() *interfaces.DraftObjectType {
	ot := existingRegions()
	ot.Properties = []*interfaces.DraftProperty{
		{Name: "region_id", Type: "string", MappedField: "region_id"},
		{Name: "population", Type: "string", MappedField: "population"},
		{Name: "code", Type: "string", MappedField: "code"},
	}
	return ot
}

func Test_RebindFollowsFieldTypes(t *testing.T) {
	Convey("Test properties follow the field types of a table bound again\n", t, func() {
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
		// Created again with its key and population now integers, and code gone.
		w.tables = append(w.tables, typedTable("new_regions", "c_new", "Region list", "regions",
			"region_id", "large_integer", "population", "integer"))
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
		draftWith := func(ot *interfaces.DraftObjectType) *interfaces.Draft {
			d := confirmedWithExisting()
			d.Items.ObjectTypes[len(d.Items.ObjectTypes)-1] = ot
			return d
		}
		typeOf := func(name string) string {
			return findProperty(findObjectType(&stored.Items, "regions"), name).Type
		}

		Convey("Binding again retypes the mapped properties, keeps a field gone, and the confirmation is to be made again\n", func() {
			store(draftWith(regionsWithFields()))

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(findObjectType(&stored.Items, "regions").Table.ResourceID, ShouldEqual, "new_regions")
			So(typeOf("region_id"), ShouldEqual, "integer")
			So(typeOf("population"), ShouldEqual, "integer")
			// The field gone is not dropped silently: the property stays, to be told as a change.
			So(typeOf("code"), ShouldEqual, "string")
			last := stored.Items.Decisions[len(stored.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_SYNC)
			So(last.ItemID, ShouldEqual, "regions")
			So(s.Confirmation.Current, ShouldBeFalse)

			Convey("Reading again writes nothing more\n", func() {
				_, err := svc.GetStatus(ctx, token, "p1", caller)
				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
			})
		})

		Convey("A key is not retyped to a type no key may have\n", func() {
			w.tables[len(w.tables)-1] = typedTable("new_regions", "c_new", "Region list", "regions",
				"region_id", "float", "population", "integer")
			store(draftWith(regionsWithFields()))

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(typeOf("region_id"), ShouldEqual, "string")
			So(typeOf("population"), ShouldEqual, "integer")
		})

		Convey("A draft bound again before, with the types of the table gone, is retyped on the owner's read\n", func() {
			ot := regionsWithFields()
			before := ot.Table
			ot.Table = interfaces.DraftTable{ResourceID: "new_regions", Name: "Region list", CatalogID: "c_new",
				CatalogName: "sample", SourceName: "regions"}
			d := draftWith(ot)
			d.Items.Decisions = append(d.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES,
				ItemID: "regions", Action: interfaces.DRAFT_DECISION_REBIND, Before: before, After: ot.Table, Time: readTime - 1})
			store(d)

			Convey("Someone not the owner reads it, and nothing is written\n", func() {
				_, err := svc.GetStatus(ctx, token, "p1", interfaces.AccountInfo{ID: "u2", Type: "user"})
				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 0)
			})

			Convey("The owner reads it: the types follow the table, once\n", func() {
				s, err := svc.GetStatus(ctx, token, "p1", caller)

				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
				So(typeOf("region_id"), ShouldEqual, "integer")
				So(s.Confirmation.Current, ShouldBeFalse)

				_, err = svc.GetStatus(ctx, token, "p1", caller)
				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
			})
		})

		Convey("A table never bound again keeps its types: a changed field is a source change for a person\n", func() {
			ot := regionsWithFields()
			ot.Table = interfaces.DraftTable{ResourceID: "new_regions", Name: "Region list", CatalogID: "c_new",
				CatalogName: "sample", SourceName: "regions"}
			store(draftWith(ot))

			_, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			So(typeOf("region_id"), ShouldEqual, "string")
		})
	})
}
