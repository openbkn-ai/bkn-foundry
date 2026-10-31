// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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
		setting := testSetting
		setting.Timeout = time.Minute
		svc := &draftService{setting: setting, da: da, ps: ps, va: va, ira: ira, run: func(fn func()) { fn() }}
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
		// What the draft becomes after each write, newest last.
		var writes []interfaces.Draft
		write := func(result ...bool) {
			for _, ok := range result {
				da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
						copied := *d
						copied.Items.Decisions = slices.Clone(d.Items.Decisions)
						writes = append(writes, copied)
						return ok, nil
					})
			}
		}
		expectAccepted := func() {
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return(nil, nil)
			ps.EXPECT().SetProjectCatalogs(gomock.Any(), token, "p1", []string{"c1", "c2"}, caller).Return(project, nil)
		}
		expectTables := func() {
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return([]*interfaces.VegaTable{{ID: "r_visits"}}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_visits"}).Return([]*interfaces.VegaTable{visitsTable()}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_members"}).Return([]*interfaces.VegaTable{membersTable()}, nil)
			// Only the table added is counted.
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				Return([]map[string]any{{"rows": 10, "distinct_values": 10, "nulls": 0}}, nil).AnyTimes()
		}

		Convey("The owner adds a catalog: the draft says so and stays ready, then its tables' items join it\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			expectAccepted()
			expectTables()
			write(true, true)

			got, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(got.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
			So(writes, ShouldHaveLength, 2)
			marked := writes[0]
			So(marked.Items.Adding.CatalogIDs, ShouldResemble, []string{"c2"})
			So(marked.UpdateTime, ShouldBeGreaterThan, readTime)
			done := writes[1]
			So(done.Items.Adding, ShouldBeNil)
			So(findObjectType(&done.Items, "visits").AddedByCatalog, ShouldEqual, "c2")
			So(done.Items.ObjectTypes[0].ID, ShouldEqual, "members")
			So(done.Items.Inputs.CatalogIDs, ShouldResemble, []string{"c1", "c2"})
			So(done.Items.Inputs.Tables, ShouldHaveLength, 2)
			So(done.Items.Decisions, ShouldHaveLength, 2)
			So(done.Items.Decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADD_CATALOGS)
			So(done.Items.Decisions[1].After.(interfaces.DraftCatalogsAdded).ObjectTypes, ShouldResemble, []string{"visits"})
		})

		Convey("A new table drafting itself could not read is unreadable, and the draft partial\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			expectAccepted()
			kinds := table("r_kinds", "c2", "visit_kinds", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			// A code table nothing refers to: only reading its values fails.
			visits := visitsTable()
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return([]*interfaces.VegaTable{{ID: "r_visits"}, {ID: "r_kinds"}}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_visits", "r_kinds"}).Return([]*interfaces.VegaTable{visits, kinds}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_members"}).Return([]*interfaces.VegaTable{membersTable()}, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
					// Only the code table's values cannot be read.
					if strings.Contains(sql, "code_value") && strings.Contains(sql, "{{r_kinds}}") {
						return nil, errors.New("timeout")
					}
					return []map[string]any{{"rows": 10, "distinct_values": 10, "nulls": 0}}, nil
				}).AnyTimes()
			write(true, true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			done := writes[1]
			ids := []string{}
			for _, u := range done.Items.Unreadable {
				ids = append(ids, u.ResourceID)
			}
			So(ids, ShouldContain, "r_kinds")
			So(done.Items.Inputs.Partial, ShouldBeTrue)
		})

		Convey("An edit made while drafting is kept: the items join the draft as it is by then\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
				edited := *draft
				edited.UpdateTime++
				edited.Items.Decisions = append(slices.Clone(draft.Items.Decisions), &interfaces.DraftDecision{
					Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "members", Action: interfaces.DRAFT_DECISION_DESCRIBE})
				return &edited, true, nil
			}).Times(2)
			expectAccepted()
			expectTables()
			// The first write of the items meets yet another edit, and is tried again.
			write(true, false, true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(writes, ShouldHaveLength, 3)
			done := writes[2]
			So(done.Items.Decisions, ShouldHaveLength, 3)
			So(done.Items.Decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_DESCRIBE)
			So(done.Items.Decisions[2].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADD_CATALOGS)
		})

		Convey("A draft drafted again meanwhile takes nothing of the catalogs added\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}, true, nil)
			expectAccepted()
			expectTables()
			write(true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(writes, ShouldHaveLength, 1)
		})

		Convey("Another adding that took over meanwhile is left to record its own\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
				other := *draft
				other.Items.Adding = &interfaces.DraftAdding{CatalogIDs: []string{"c9"}, Since: draft.Items.Adding.Since + 1}
				return &other, true, nil
			})
			expectAccepted()
			expectTables()
			write(true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			So(writes, ShouldHaveLength, 1)
		})

		Convey("A table of the draft drafting could not read is no table added\n", func() {
			// The draft's tiers read as a code table now, whose values are not read again.
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "tiers", Name: "tiers",
				Adopted: true, PrimaryKeys: []string{"code"}, Table: interfaces.DraftTable{ResourceID: "r_tiers", CatalogID: "c1"},
				Properties: []*interfaces.DraftProperty{{Name: "code", Type: "string", MappedField: "code"}}})
			tiers := table("r_tiers", "c1", "tiers", 3, []string{"code"}, col("code", "string", ""), col("name", "string", ""))
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			expectAccepted()
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return([]*interfaces.VegaTable{{ID: "r_visits"}}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_visits"}).Return([]*interfaces.VegaTable{visitsTable()}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_members", "r_tiers"}).
				Return([]*interfaces.VegaTable{membersTable(), tiers}, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				Return([]map[string]any{{"rows": 10, "distinct_values": 10, "nulls": 0}}, nil).AnyTimes()
			write(true, true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			for _, u := range writes[1].Items.Unreadable {
				So(u.ResourceID, ShouldNotEqual, "r_tiers")
			}
			So(writes[1].Items.Inputs.Partial, ShouldBeFalse)
		})

		Convey("Drafting that fails leaves the draft as it is, recording why\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			expectAccepted()
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return(nil, errors.New("vega down"))
			write(true, true)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			So(err, ShouldBeNil)
			done := writes[1]
			So(done.Items.Adding, ShouldBeNil)
			So(done.Items.ObjectTypes, ShouldHaveLength, 1)
			So(done.Items.Inputs.CatalogIDs, ShouldResemble, []string{"c1"})
			last := done.Items.Decisions[len(done.Items.Decisions)-1]
			So(last.After.(interfaces.DraftCatalogsAdded).Error, ShouldContainSubstring, "vega down")
		})

		Convey("A draft changed since it was read is not marked, nothing is drafted\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectAccepted()
			write(false)

			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)

			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Stale)
		})

		Convey("Catalogs being added refuse more until done; once past the drafting timeout they hold nothing\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			draft.Items.Adding = &interfaces.DraftAdding{CatalogIDs: []string{"c3"}, Since: time.Now().UnixMilli()}
			_, err := svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)
			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Drafting)

			draft.Items.Adding.Since = time.Now().Add(-(setting.Timeout + 2*time.Minute)).UnixMilli()
			expectAccepted()
			expectTables()
			write(true, true)
			_, err = svc.AddCatalogs(ctx, token, "p1", add("c2"), caller)
			So(err, ShouldBeNil)
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

		Convey("Without a ready draft nothing is added\n", func() {
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

		Convey("A catalog the project has but the draft was not drafted from is drafted, the project keeping it once\n", func() {
			project.CatalogIDs = []string{"c1", "c2"}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c2"}).Return(nil, nil)
			expectTables()
			write(true, true)

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
