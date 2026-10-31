// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// The draft's catalog c1 has members; the catalog added, c2, has visits of members and its own
// members table.
func membersTable() *interfaces.VegaTable {
	return table("r_members", "c1", "members", 0, []string{"member_id"},
		col("member_id", "string", "Member number"), col("member_name", "string", "Member name"))
}

func visitsTable() *interfaces.VegaTable {
	// Its member_id names the members of the draft's catalog: a relation across catalogs.
	return table("r_visits", "c2", "visits", 0, []string{"visit_id"},
		col("visit_id", "string", ""), col("member_id", "string", ""), col("visited_at", "datetime", ""))
}

func otherMembersTable() *interfaces.VegaTable {
	return table("r_members2", "c2", "members", 0, []string{"code"}, col("code", "string", ""), col("label", "string", ""))
}

func Test_MergeDrafted(t *testing.T) {
	Convey("Test merging what added catalogs drafted into a draft\n", t, func() {
		ctx := context.Background()
		base, err := newDrafter(testSetting, sampleCounter()).draft(ctx, []*interfaces.VegaTable{membersTable()}, nil)
		So(err, ShouldBeNil)
		// A person's edits on the draft, which adding catalogs must leave as they are.
		members := base.ObjectTypes[0]
		members.Name = "Club members"
		base.Decisions = []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "members",
			Action: interfaces.DRAFT_DECISION_RENAME, Before: "members", After: "Club members"}}
		draftWith := func(items interfaces.DraftItems, tables ...*interfaces.VegaTable) (interfaces.DraftItems, *fakeCounter) {
			c := sampleCounter()
			drafted, err := newDrafter(testSetting, &draftedCounter{counter: c, keys: draftedKeys(&items)}).
				draft(ctx, append(tables, membersTable()), nil)
			So(err, ShouldBeNil)
			return drafted, c
		}
		added := []*interfaces.VegaTable{visitsTable(), otherMembersTable()}

		Convey("New tables add new object types and relations to the draft's, marked with their catalog\n", func() {
			drafted, _ := draftWith(base, added...)
			record := &interfaces.DraftCatalogsAdded{}

			merged := mergeDrafted(base, drafted, added, nil, record)

			So(merged.ObjectTypes, ShouldHaveLength, 3)
			So(merged.ObjectTypes[0], ShouldEqual, members)
			So(merged.ObjectTypes[0].Name, ShouldEqual, "Club members")
			So(merged.ObjectTypes[0].AddedByCatalog, ShouldBeEmpty)
			So(merged.Decisions, ShouldResemble, base.Decisions)
			visits := findObjectType(&merged, "visits")
			So(visits.AddedByCatalog, ShouldEqual, "c2")
			// The other catalog's members meet the draft's id and take the next one.
			So(findObjectType(&merged, "members_2").Table.ResourceID, ShouldEqual, "r_members2")
			So(findObjectType(&merged, "members_2").AddedByCatalog, ShouldEqual, "c2")
			So(merged.RelationTypes, ShouldHaveLength, 1)
			rt := merged.RelationTypes[0]
			So(rt.Source, ShouldEqual, "visits")
			So(rt.Target, ShouldEqual, "members")
			So(rt.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "member_id", TargetProperty: "member_id"}})
			So(rt.AddedByCatalog, ShouldEqual, "c2")
			// Across catalogs a relation is not counted: drafting proposes it for a person.
			So(rt.Adopted, ShouldBeFalse)
			So(record.ObjectTypes, ShouldResemble, []string{"members_2", "visits"})
			So(record.RelationTypes, ShouldResemble, []string{rt.ID})
		})

		Convey("A relation adopted by drafting to an object type of the draft that is not adopted is not adopted\n", func() {
			members.Adopted = false
			drafted, _ := draftWith(base, added...)
			drafted.RelationTypes[0].Adopted, drafted.RelationTypes[0].ProposedBecause = true, ""

			merged := mergeDrafted(base, drafted, added, nil, &interfaces.DraftCatalogsAdded{})

			So(merged.RelationTypes[0].Adopted, ShouldBeFalse)
			So(merged.RelationTypes[0].ProposedBecause, ShouldEqual, proposedEndNotAdopted)

			members.Adopted = true
			drafted, _ = draftWith(base, added...)
			drafted.RelationTypes[0].Adopted, drafted.RelationTypes[0].ProposedBecause = true, ""
			So(mergeDrafted(base, drafted, added, nil, &interfaces.DraftCatalogsAdded{}).RelationTypes[0].Adopted, ShouldBeTrue)
		})

		Convey("A relation by a field the draft's object type no longer maps is left out\n", func() {
			drafted, _ := draftWith(base, added...)
			members.Properties = members.Properties[1:]

			merged := mergeDrafted(base, drafted, added, nil, &interfaces.DraftCatalogsAdded{})

			So(merged.RelationTypes, ShouldBeEmpty)
			So(merged.ObjectTypes, ShouldHaveLength, 3)
		})

		Convey("A code table added becomes a value type of the new object types only\n", func() {
			kinds := table("r_kinds", "c2", "visit_kinds", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			visits := visitsTable()
			visits.Columns = append(visits.Columns, col("kind_code", "string", ""))
			visits.ForeignKeys = []interfaces.VegaForeignKey{fk("kind_code", "visit_kinds", "code")}
			tables := []*interfaces.VegaTable{visits, kinds}
			drafted, _ := draftWith(base, tables...)
			record := &interfaces.DraftCatalogsAdded{}

			merged := mergeDrafted(base, drafted, tables, nil, record)

			So(record.ValueTypes, ShouldHaveLength, 1)
			vt := merged.ValueTypes[len(merged.ValueTypes)-1]
			So(vt.AddedByCatalog, ShouldEqual, "c2")
			So(vt.UsedBy, ShouldResemble, []interfaces.DraftPropertyRef{{ObjectType: "visits", Property: "kind_code"}})
		})

		Convey("A new item meeting an id of the draft whose table drafting did not read takes the next one\n", func() {
			// The draft's members table is not readable now: the other members table drafts as members.
			drafted, err := newDrafter(testSetting, sampleCounter()).draft(ctx, []*interfaces.VegaTable{otherMembersTable()}, nil)
			So(err, ShouldBeNil)

			merged := mergeDrafted(base, drafted, []*interfaces.VegaTable{otherMembersTable()}, nil, &interfaces.DraftCatalogsAdded{})

			So(findObjectType(&merged, "members"), ShouldEqual, members)
			So(findObjectType(&merged, "members_2").Table.ResourceID, ShouldEqual, "r_members2")
		})

		Convey("A relation's end at the draft's object type takes the property the draft maps the field by\n", func() {
			members.Properties[0].Name = "member_no"
			members.PrimaryKeys = []string{"member_no"}
			drafted, _ := draftWith(base, added...)

			merged := mergeDrafted(base, drafted, added, nil, &interfaces.DraftCatalogsAdded{})

			So(merged.RelationTypes[0].Mapping[0].TargetProperty, ShouldEqual, "member_no")
		})

		Convey("A code table added is used by the new object types only, not by the draft's\n", func() {
			kinds := table("r_kinds", "c2", "visit_kinds", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			visits := visitsTable()
			visits.Columns = append(visits.Columns, col("kind_code", "string", ""))
			visits.ForeignKeys = []interfaces.VegaForeignKey{fk("kind_code", "visit_kinds", "code")}
			// The draft's members table, as read now, refers to the code table too.
			old := membersTable()
			old.Columns = append(old.Columns, col("visit_kinds_code", "string", ""))
			c := sampleCounter()
			drafted, err := newDrafter(testSetting, &draftedCounter{counter: c, keys: draftedKeys(&base)}).
				draft(ctx, []*interfaces.VegaTable{visits, kinds, old}, nil)
			So(err, ShouldBeNil)
			vt := drafted.ValueTypes[0]
			usedByOld := false
			for _, ref := range vt.UsedBy {
				usedByOld = usedByOld || ref.ObjectType == "members"
			}

			merged := mergeDrafted(base, drafted, []*interfaces.VegaTable{visits, kinds}, nil, &interfaces.DraftCatalogsAdded{})

			So(usedByOld, ShouldBeTrue)
			So(merged.ValueTypes[len(merged.ValueTypes)-1].UsedBy, ShouldResemble,
				[]interfaces.DraftPropertyRef{{ObjectType: "visits", Property: "kind_code"}})
		})

		Convey("The draft's tables are not counted again, but for the field each is identified by\n", func() {
			c := &draftedCounter{counter: &fakeCounter{failed: map[string]bool{"r_members": true}}, keys: draftedKeys(&base)}
			members := membersTable()

			rows, distinct, nulls, err := c.keyStats(ctx, members, "member_id")
			So(err, ShouldBeNil)
			So([3]int64{rows, distinct, nulls}, ShouldResemble, [3]int64{1, 1, 0})
			_, _, _, err = c.keyStats(ctx, members, "member_name")
			So(err, ShouldEqual, errNotCounted)
			_, _, _, err = c.keyTupleStats(ctx, members, []string{"member_id", "member_name"})
			So(err, ShouldEqual, errNotCounted)
			_, _, _, _, err = c.joinStats(ctx, members, "member_id", visitsTable(), "visit_id")
			So(err, ShouldEqual, errNotCounted)
			// A table added is counted.
			_, _, _, err = c.keyStats(ctx, visitsTable(), "visit_id")
			So(err, ShouldBeNil)
		})

		Convey("Unreadable tables are added to those of the draft\n", func() {
			drafted, _ := draftWith(base, added...)
			gone := &interfaces.DraftTable{ResourceID: "r_x", Name: "x", CatalogID: "c2", Reason: unreadableNotVisible}

			merged := mergeDrafted(base, drafted, added, []*interfaces.DraftTable{gone}, &interfaces.DraftCatalogsAdded{})

			So(merged.Unreadable, ShouldContain, gone)
		})
	})
}

func Test_AddCatalogs(t *testing.T) {
	Convey("Test adding catalogs to a project's draft\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ira := &fakeIDRegistry{}
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, ira: ira, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		items, err := newDrafter(testSetting, sampleCounter()).draft(ctx, []*interfaces.VegaTable{membersTable()}, nil)
		So(err, ShouldBeNil)
		items.Inputs = &interfaces.DraftInputs{CatalogIDs: []string{"c1"}, Tables: []interfaces.DraftInputTable{{ResourceID: "r_members"}}}
		items.Decisions = []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "members",
			Action: interfaces.DRAFT_DECISION_RENAME}}
		draft := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime, Items: items}
		codeOf := func(err error) (int, string) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode
		}
		add := func(ids ...string) interfaces.DraftAddCatalogs { return interfaces.DraftAddCatalogs{CatalogIDs: ids} }
		var finished *interfaces.Draft
		expectStart := func() {
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return(nil, nil)
			ps.EXPECT().SetProjectCatalogs(gomock.Any(), token, "p1", []string{"c1", "c2"}, caller).Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), true).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64, _ bool) (bool, error) {
					// The draft's items stay while the catalogs are drafted.
					So(d.Status, ShouldEqual, interfaces.DRAFT_STATUS_DRAFTING)
					So(d.Items.ObjectTypes, ShouldHaveLength, 1)
					So(d.Items.Decisions, ShouldHaveLength, 1)
					return true, nil
				})
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d *interfaces.Draft) error {
				finished = d
				return nil
			})
		}

		Convey("The owner adds a catalog: its tables are drafted into the draft, which keeps its items and decisions\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectStart()
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return([]*interfaces.VegaTable{{ID: "r_visits"}}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_visits"}).Return([]*interfaces.VegaTable{visitsTable()}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_members"}).Return([]*interfaces.VegaTable{membersTable()}, nil)
			// Only the table added is counted: its key, and its relation to the draft's members.
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				Return([]map[string]any{{"rows": 10, "distinct_values": 10, "nulls": 0, "orphans": 0, "distinct_sources": 10}}, nil).AnyTimes()

			got, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(got.Status, ShouldEqual, interfaces.DRAFT_STATUS_DRAFTING)
			So(finished.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
			So(finished.UpdateTime, ShouldBeGreaterThan, readTime)
			So(findObjectType(&finished.Items, "visits").AddedByCatalog, ShouldEqual, "c2")
			So(finished.Items.ObjectTypes[0].ID, ShouldEqual, "members")
			So(finished.Items.Inputs.CatalogIDs, ShouldResemble, []string{"c1", "c2"})
			So(finished.Items.Inputs.Tables, ShouldHaveLength, 2)
			decisions := finished.Items.Decisions
			So(decisions, ShouldHaveLength, 2)
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADD_CATALOGS)
			So(decisions[1].After.(interfaces.DraftCatalogsAdded).ObjectTypes, ShouldResemble, []string{"visits"})
		})

		Convey("Drafting that fails leaves the draft as it was, ready, recording why\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectStart()
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return(nil, errors.New("vega down"))

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
			So(finished.Items.ObjectTypes, ShouldHaveLength, 1)
			So(finished.Items.Inputs.CatalogIDs, ShouldResemble, []string{"c1"})
			last := finished.Items.Decisions[len(finished.Items.Decisions)-1]
			So(last.After.(interfaces.DraftCatalogsAdded).Error, ShouldContainSubstring, "vega down")
		})

		Convey("Catalogs that are none, repeated, empty or drafted already are refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			for _, ids := range [][]string{nil, {"c2", "c2"}, {" "}, {"c1"}} {
				_, err := svc.AddCatalogs(ctx, token, "p1", add(ids...), caller)

				status, code := codeOf(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Project_InvalidCatalogIDs)
			}
		})

		Convey("A catalog that is gone is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return([]string{"c2"}, nil)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_CatalogMissing)
		})

		Convey("Without a ready draft, or with drafting under way, nothing is added\n", func() {
			drafting := *draft
			drafting.Status = interfaces.DRAFT_STATUS_DRAFTING
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&drafting, true, nil)
			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)
			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_NotReady)

			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			_, err = svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)
			_, code = codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_NotReady)
		})

		Convey("Drafting that starts meanwhile wins\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return(nil, nil)
			ps.EXPECT().SetProjectCatalogs(gomock.Any(), token, "p1", []string{"c1", "c2"}, caller).Return(project, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), true).Return(false, nil)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Drafting)
		})

		Convey("A catalog the project has but the draft was not drafted from is drafted, the project keeping it once\n", func() {
			project.CatalogIDs = []string{"c1", "c2"}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return(nil, nil)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), true).Return(true, nil)
			da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).Return(nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return(nil, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil).AnyTimes()

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			project.CatalogIDs = []string{"c1"}
		})

		Convey("Someone other than the owner is refused\n", func() {
			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), interfaces.AccountInfo{ID: "u2", Type: "user"})

			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusForbidden)
		})
	})
}
