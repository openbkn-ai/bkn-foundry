// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const readTime = int64(100)

// editableDraft is a ready draft: customers and orders adopted and related, regions not
// adopted, and a relation to regions that is not adopted either.
func editableDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "customers", Name: "Customers", Adopted: true},
				{ID: "orders", Name: "Orders", Description: "Sales orders", Adopted: true},
				{ID: "regions", Name: "Regions"},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "orders_customers", Name: "Orders customers", Source: "orders", Target: "customers", Adopted: true},
				{ID: "customers_regions", Name: "Customers regions", Source: "customers", Target: "regions"},
			},
			ValueTypes: []*interfaces.DraftValueType{{ID: "levels", Name: "Levels", Adopted: true}},
		},
	}
}

func boolPtr(b bool) *bool           { return &b }
func strPtr(s string) *string        { return &s }
func edit() interfaces.DraftItemEdit { return interfaces.DraftItemEdit{UpdateTime: readTime} }

func Test_EditDraftItem(t *testing.T) {
	Convey("Test editing a draft item\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := editableDraft()
		var written *interfaces.Draft
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})
		}

		Convey("Not adopting an object type stops adopting the relation types that connect it\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(false)

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "customers", e, caller)

			So(err, ShouldBeNil)
			So(got, ShouldEqual, written)
			So(got.Items.ObjectTypes[0].Adopted, ShouldBeFalse)
			So(got.Items.RelationTypes[0].Adopted, ShouldBeFalse)
			// The update time moves on, so the next edit must be made on this draft.
			So(got.UpdateTime, ShouldBeGreaterThan, readTime)
			decisions := got.Items.Decisions
			So(len(decisions), ShouldEqual, 2)
			So(*decisions[0], ShouldResemble, interfaces.DraftDecision{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "customers", Action: interfaces.DRAFT_DECISION_UNADOPT,
				Before: true, After: false, By: caller, Time: got.UpdateTime,
			})
			So(decisions[1].Kind, ShouldEqual, interfaces.DRAFT_KIND_RELATION_TYPES)
			So(decisions[1].ItemID, ShouldEqual, "orders_customers")
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_UNADOPT)
		})

		Convey("A relation type is adopted only when both its object types are\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_RELATION_TYPES, "customers_regions", e, caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_EndpointNotAdopted)
		})

		Convey("Adopting a relation type of adopted object types records the decision\n", func() {
			draft.Items.ObjectTypes[2].Adopted = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(true)

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_RELATION_TYPES, "customers_regions", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.RelationTypes[1].Adopted, ShouldBeTrue)
			So(got.Items.Decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADOPT)
		})

		Convey("Adopting an object type adopts the relations proposed only for it, on their counts\n", func() {
			counted := func(rate float64) interfaces.DraftJoinEvidence {
				return interfaces.DraftJoinEvidence{Computed: true, Rows: 100, Hits: int64(rate * 100), HitRate: rate, SourceDistinct: 50}
			}
			relate := func(id, target, field, key string, rate float64) *interfaces.DraftRelationType {
				return &interfaces.DraftRelationType{
					ID: id, Source: "orders", Target: target, Origin: interfaces.DRAFT_ORIGIN_NAME_MATCH,
					Mapping:  []interfaces.DraftFieldMapping{{SourceProperty: field, TargetProperty: key}},
					Evidence: counted(rate), ProposedBecause: proposedEndNotAdopted,
				}
			}
			draft.Items.ObjectTypes = []*interfaces.DraftObjectType{
				{ID: "orders", Name: "Orders", PrimaryKeys: []string{"order_id"}},
				{ID: "suppliers", Name: "Suppliers", Adopted: true, PrimaryKeys: []string{"supplier_code"}},
				{ID: "materials", Name: "Materials", Adopted: true, PrimaryKeys: []string{"material_code"}},
				{ID: "regions", Name: "Regions", PrimaryKeys: []string{"region_id"}},
			}
			draft.Items.RelationTypes = []*interfaces.DraftRelationType{
				relate("by_supplier", "suppliers", "supplier_number", "supplier_code", 1),
				relate("by_material", "materials", "material_number", "material_code", 0.85),
				// Counted against a key the target no longer has.
				relate("by_old_key", "materials", "material_name", "material_name", 1),
				// Its other end is not adopted.
				relate("by_region", "regions", "region_id", "region_id", 1),
				// A person decided on it.
				relate("decided", "suppliers", "vendor_code", "supplier_code", 1),
			}
			draft.Items.Decisions = []*interfaces.DraftDecision{{
				Kind: interfaces.DRAFT_KIND_RELATION_TYPES, ItemID: "decided", Action: interfaces.DRAFT_DECISION_RENAME,
			}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(true)

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			state := map[string]string{}
			for _, r := range got.Items.RelationTypes {
				state[r.ID] = strings.TrimSpace(map[bool]string{true: "adopted", false: ""}[r.Adopted] + " " + r.ProposedBecause)
			}
			So(state, ShouldResemble, map[string]string{
				"by_supplier": "adopted",
				"by_material": proposedBelowAdoptRate,
				"by_old_key":  proposedEndNotAdopted,
				"by_region":   proposedEndNotAdopted,
				"decided":     proposedEndNotAdopted,
			})
			decisions := got.Items.Decisions[1:]
			So(decisions, ShouldHaveLength, 2)
			So(decisions[0].ItemID, ShouldEqual, "orders")
			So(decisions[1].ItemID, ShouldEqual, "by_supplier")
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADOPT)
		})

		Convey("Renaming and describing record what they replaced\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Name = strPtr("  Sales orders  ")
			e.Description = strPtr("Orders customers place")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[1].Name, ShouldEqual, "Sales orders")
			So(got.Items.ObjectTypes[1].Description, ShouldEqual, "Orders customers place")
			decisions := got.Items.Decisions
			So(len(decisions), ShouldEqual, 2)
			So(decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_RENAME)
			So(decisions[0].Before, ShouldEqual, "Orders")
			So(decisions[0].After, ShouldEqual, "Sales orders")
			So(decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_DESCRIBE)
			So(decisions[1].Before, ShouldEqual, "Sales orders")
		})

		Convey("An object type's update field is one of its properties of a type the index accepts\n", func() {
			draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r_orders", CatalogID: "c1"}
			draft.Items.ObjectTypes[1].Properties = []*interfaces.DraftProperty{
				{Name: "id", Type: "integer", MappedField: "id"},
				{Name: "changed_at", Type: "datetime", MappedField: "updated_at"},
				{Name: "amount", Type: "float", MappedField: "amount"},
				// vega's large_integer drafts as integer, which vega does not refresh an index by.
				{Name: "seq", Type: "integer", MappedField: "seq"},
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			// The field's type is read from the table as the caller: the draft keeps only BKN types.
			table := &interfaces.VegaTable{ID: "r_orders", Columns: []interfaces.VegaColumn{
				{Name: "id", Type: "integer"}, {Name: "updated_at", Type: "datetime"}, {Name: "seq", Type: "large_integer"},
			}}
			e := edit()

			Convey("setting it records what it replaced\n", func() {
				va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders"}).Return([]*interfaces.VegaTable{table}, nil)
				expectWrite()
				e.IncrementalKey = strPtr(" changed_at ")

				got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

				So(err, ShouldBeNil)
				So(got.Items.ObjectTypes[1].IncrementalKey, ShouldEqual, "changed_at")
				So(written.Items.Decisions, ShouldHaveLength, 1)
				d := written.Items.Decisions[0]
				So([]any{d.Kind, d.ItemID, d.Action, d.Before, d.After}, ShouldResemble, []any{
					interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", interfaces.DRAFT_DECISION_INCREMENTAL, "", "changed_at"})
			})

			Convey("an empty value clears it\n", func() {
				draft.Items.ObjectTypes[1].IncrementalKey = "changed_at"
				expectWrite()
				e.IncrementalKey = strPtr("")

				got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

				So(err, ShouldBeNil)
				So(got.Items.ObjectTypes[1].IncrementalKey, ShouldEqual, "")
				So(written.Items.Decisions[0].Before, ShouldEqual, "changed_at")
			})

			Convey("the same value writes nothing\n", func() {
				draft.Items.ObjectTypes[1].IncrementalKey = "changed_at"
				e.IncrementalKey = strPtr("changed_at")

				got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

				So(err, ShouldBeNil)
				So(got.Items.Decisions, ShouldBeEmpty)
			})

			Convey("an unknown property, another type or another kind of item is refused\n", func() {
				for _, c := range []struct{ kind, id, key string }{
					{interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", "updated_at"},
					{interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", "amount"},
					{interfaces.DRAFT_KIND_RELATION_TYPES, "orders_customers", "changed_at"},
				} {
					e.IncrementalKey = strPtr(c.key)
					_, err := svc.EditDraftItem(ctx, token, "p1", c.kind, c.id, e, caller)
					shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIncrementalKey)
				}
				So(written, ShouldBeNil)
			})

			Convey("a field whose vega type the index cannot be refreshed by is refused\n", func() {
				va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders"}).Return([]*interfaces.VegaTable{table}, nil)
				e.IncrementalKey = strPtr("seq")

				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIncrementalKey)
				So(written, ShouldBeNil)
			})

			Convey("a field gone from its table, or a table the caller cannot read, is not found\n", func() {
				e.IncrementalKey = strPtr("changed_at")
				for _, read := range [][]*interfaces.VegaTable{
					{{ID: "r_orders", Columns: []interfaces.VegaColumn{{Name: "id", Type: "integer"}}}}, {},
				} {
					// The refused edit changed the draft read in memory only; read it afresh.
					draft.Items.ObjectTypes[1].IncrementalKey = ""
					va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders"}).Return(read, nil)
					_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
					shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
				}
				So(written, ShouldBeNil)
			})

			Convey("a failure reading the table is an internal error\n", func() {
				va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders"}).Return(nil, errors.New("vega is down"))
				e.IncrementalKey = strPtr("changed_at")

				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

				shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			})
		})

		Convey("Renaming an object type renames the relations still named after it, and no other\n", func() {
			orders := draft.Items.ObjectTypes[1]
			orders.Name = "采购单"
			orders.Properties = []*interfaces.DraftProperty{
				{Name: "supplier_no", DisplayName: "供应商编号", MappedField: "supplier_no"},
				{Name: "buyer_id", DisplayName: "buyer_id", MappedField: "buyer_id"},
			}
			relation := func(id, property, name string) *interfaces.DraftRelationType {
				return &interfaces.DraftRelationType{ID: id, Name: name, Source: "orders", Target: "customers",
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: property, TargetProperty: "id"}}}
			}
			draft.Items.RelationTypes = []*interfaces.DraftRelationType{
				relation("orders_to_customers", "supplier_no", "采购单的供应商编号"),
				// A person renamed it: it stays.
				relation("orders_to_customers_2", "supplier_no", "采购来源"),
				// Named by its id, as its field has only a physical name: it stays.
				relation("orders_to_customers_3", "buyer_id", "orders_to_customers_3"),
				// Drafted from a link table, named after it: it stays.
				{ID: "order_tags", Name: "采购单的供应商编号", Source: "orders", Target: "customers",
					LinkTable: &interfaces.DraftTable{ResourceID: "r_tags"}},
				// Another object type's relation a person named like one of these: it stays.
				{ID: "customers_to_orders", Name: "采购单的供应商编号", Source: "customers", Target: "orders",
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "supplier_no", TargetProperty: "id"}}},
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Name = strPtr("采购订单")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			names := []string{}
			for _, r := range got.Items.RelationTypes {
				names = append(names, r.Name)
			}
			So(names, ShouldResemble, []string{"采购订单的供应商编号", "采购来源", "orders_to_customers_3",
				"采购单的供应商编号", "采购单的供应商编号"})
			last := written.Items.Decisions[len(written.Items.Decisions)-1]
			So([]any{last.Kind, last.ItemID, last.Action, last.Before, last.After}, ShouldResemble, []any{
				interfaces.DRAFT_KIND_RELATION_TYPES, "orders_to_customers", interfaces.DRAFT_DECISION_RENAME,
				"采购单的供应商编号", "采购订单的供应商编号"})
		})

		Convey("An object type without an identity cannot be adopted\n", func() {
			draft.Items.ObjectTypes[2].NeedsIdentity = true
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "regions", e, caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_NeedsIdentity)
		})

		Convey("Value types can be renamed and not adopted\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Adopted = boolPtr(false)
			e.Name = strPtr("Customer levels")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_VALUE_TYPES, "levels", e, caller)

			So(err, ShouldBeNil)
			So(got.Items.ValueTypes[0].Adopted, ShouldBeFalse)
			So(got.Items.ValueTypes[0].Name, ShouldEqual, "Customer levels")
			So(len(got.Items.Decisions), ShouldEqual, 2)
		})

		Convey("An edit that changes nothing writes nothing\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := edit()
			e.Adopted = boolPtr(true)
			e.Name = strPtr("Orders")

			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
			So(got.UpdateTime, ShouldEqual, readTime)
			So(got.Items.Decisions, ShouldBeEmpty)
		})

		Convey("Invalid edits are refused before the draft is read\n", func() {
			cases := []struct {
				edit interfaces.DraftItemEdit
				code string
			}{
				{edit(), perrors.BknProject_Draft_EmptyEdit},
				// Without the draft's update time, reloading would never make the edit pass.
				{interfaces.DraftItemEdit{Adopted: boolPtr(true)}, perrors.BknProject_Draft_MissingUpdateTime},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Name: strPtr("  ")}, perrors.BknProject_Draft_InvalidName},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Name: strPtr(strings.Repeat("\u540d", 129))}, perrors.BknProject_Draft_InvalidName},
				{interfaces.DraftItemEdit{UpdateTime: readTime, Description: strPtr(strings.Repeat("\u8ff0", 1001))}, perrors.BknProject_Draft_InvalidDescription},
			}
			for _, c := range cases {
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", c.edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, c.code)
			}
		})

		Convey("A name of exactly the limit is accepted\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()
			e := edit()
			e.Name = strPtr(strings.Repeat("\u540d", interfaces.DRAFT_NAME_MAX_LENGTH))

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			So(err, ShouldBeNil)
		})

		Convey("An edit made on an older draft is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			e := interfaces.DraftItemEdit{UpdateTime: readTime - 1, Adopted: boolPtr(false)}

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})

		Convey("An edit racing another one is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(false, nil)
			e := edit()
			e.Adopted = boolPtr(false)

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})

		Convey("Only a ready draft of an unarchived project can be edited\n", func() {
			e := edit()
			e.Adopted = boolPtr(false)

			Convey("archived project\n", func() {
				archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p2", Archived: true}}
				ps.EXPECT().GetProject(gomock.Any(), token, "p2").Return(archived, nil)
				_, err := svc.EditDraftItem(ctx, token, "p2", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
			})
			Convey("drafting\n", func() {
				draft.Status = interfaces.DRAFT_STATUS_DRAFTING
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_NotReady)
			})
			Convey("no draft\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_NotFound)
			})
			Convey("unknown item or kind\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).Times(2)
				_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "missing", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
				_, err = svc.EditDraftItem(ctx, token, "p1", "excluded", "orders", e, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
			})
		})
	})
}

func Test_GenerateDraftGuards(t *testing.T) {
	Convey("Test what keeps drafting from starting\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, ira: &fakeIDRegistry{}, run: func(fn func()) {}}
		ctx := context.Background()

		Convey("An archived project is not drafted\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"}, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
		})

		Convey("Drafting again keeps people's decisions unless told to discard them\n", func() {
			project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"}}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c1"}).Return(nil, nil).Times(2)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(false, interfaces.ErrDraftHasDecisions)
			da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), true).Return(true, nil)

			_, err := svc.GenerateDraft(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_HasDecisions)

			_, err = svc.GenerateDraft(ctx, token, "p1", caller, true)
			So(err, ShouldBeNil)
		})
	})
}
