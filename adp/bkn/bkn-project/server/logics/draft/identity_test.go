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
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// identityDraft is a ready draft whose order lines need a person to choose their identity, and
// whose customers lost a phone field to the privacy rules and a geometry field to its type.
func identityDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{
					ID: "order_lines", Name: "Order lines", NeedsIdentity: true,
					Table: interfaces.DraftTable{ResourceID: "r_lines", Name: "order_lines", CatalogID: "c1"},
					Properties: []*interfaces.DraftProperty{
						{Name: "order_no", Type: "string", MappedField: "order_no"},
						{Name: "line_no", Type: "integer", MappedField: "line_no"},
						{Name: "amount", Type: "decimal", MappedField: "amount"},
					},
					Key: interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_NONE},
				},
				{
					ID: "customers", Name: "Customers", Adopted: true, PrimaryKeys: []string{"id"}, DisplayKey: "id",
					Table:      interfaces.DraftTable{ResourceID: "r_customers", Name: "customers", CatalogID: "c1"},
					Properties: []*interfaces.DraftProperty{{Name: "id", Type: "string", MappedField: "ID"}},
					Key:        interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_PRIMARY_KEY},
				},
			},
			Excluded: []*interfaces.DraftExcludedField{
				{ObjectType: "customers", Field: "Phone", Reason: excludedPrivacy, Keyword: "phone"},
				{ObjectType: "customers", Field: "area", Reason: excludedUnsupported, Keyword: "geometry"},
			},
		},
	}
}

func Test_ChooseIdentity(t *testing.T) {
	Convey("Test choosing an object type's identity\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, ira: &fakeIDRegistry{}, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := identityDraft()
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
		}
		// The other object types' tables, read to draft relations to a single-field identity.
		noTables := func() {
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil)
		}
		choose := func(properties ...string) interfaces.DraftIdentity {
			return interfaces.DraftIdentity{UpdateTime: readTime, Properties: properties}
		}

		Convey("A single-field identity is counted over the full table and kept even with duplicates\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				DoAndReturn(func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
					So(sql, ShouldContainSubstring, "COUNT(DISTINCT `order_no`)")
					So(sql, ShouldContainSubstring, "{{r_lines}}")
					return []map[string]any{{"total": float64(10), "distinct_values": float64(9), "nulls": float64(0)}}, nil
				})
			noTables()
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

			So(err, ShouldBeNil)
			lines := got.Items.ObjectTypes[0]
			So(lines.PrimaryKeys, ShouldResemble, []string{"order_no"})
			So(lines.NeedsIdentity, ShouldBeFalse)
			// Adopting stays a decision of its own.
			So(lines.Adopted, ShouldBeFalse)
			So(lines.DisplayKey, ShouldEqual, "order_no")
			So(lines.Key, ShouldResemble, interfaces.DraftKeyEvidence{
				Source: interfaces.DRAFT_KEY_CHOSEN, Field: "order_no", Computed: true, Rows: 10, Distinct: 9,
			})
			decision := got.Items.Decisions[0]
			So(decision.Action, ShouldEqual, interfaces.DRAFT_DECISION_IDENTITY)
			So(decision.Before, ShouldResemble, []string{})
			So(decision.After, ShouldResemble, []string{"order_no"})
		})

		Convey("A composite identity is kept without counts\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no", "line_no"), caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].PrimaryKeys, ShouldResemble, []string{"order_no", "line_no"})
			So(got.Items.ObjectTypes[0].Key, ShouldResemble, interfaces.DraftKeyEvidence{
				Source: interfaces.DRAFT_KEY_CHOSEN, Reason: reasonCompositeKey,
			})
		})

		Convey("A count that fails keeps the choice and says why there are no counts\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).Return(nil, errors.New("timeout"))
			noTables()
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("line_no"), caller)

			So(err, ShouldBeNil)
			key := got.Items.ObjectTypes[0].Key
			So(key.Computed, ShouldBeFalse)
			So(key.Field, ShouldEqual, "line_no")
			So(key.Reason, ShouldStartWith, reasonQueryFailed)
		})

		Convey("Keeping a declared key that counted duplicates is a decision, counted again\n", func() {
			lines := draft.Items.ObjectTypes[0]
			lines.PrimaryKeys = []string{"order_no"}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				Return([]map[string]any{{"total": float64(10), "distinct_values": float64(9), "nulls": float64(0)}}, nil)
			noTables()
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].NeedsIdentity, ShouldBeFalse)
			So(got.Items.ObjectTypes[0].Key.Computed, ShouldBeTrue)
			decision := got.Items.Decisions[0]
			So(decision.Action, ShouldEqual, interfaces.DRAFT_DECISION_IDENTITY)
			So(decision.Before, ShouldResemble, []string{"order_no"})
			So(decision.After, ShouldResemble, []string{"order_no"})
		})

		Convey("A slow count gives up well before the gateway does, so the choice is still written\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				DoAndReturn(func(qctx context.Context, _ interfaces.VegaAccount, _ string, _ int) ([]map[string]any, error) {
					deadline, ok := qctx.Deadline()
					So(ok, ShouldBeTrue)
					So(time.Until(deadline), ShouldBeLessThanOrEqualTo, identityCountTimeout)
					return nil, context.DeadlineExceeded
				})
			noTables()
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].Key.Reason, ShouldStartWith, reasonQueryFailed)
		})

		Convey("A single-field identity takes the relations other object types have to it, gated by the counts\n", func() {
			// Shipments name their order line by order_no, the key just chosen.
			withShipments := func() *interfaces.Draft {
				d := identityDraft()
				d.Items.ObjectTypes = append(d.Items.ObjectTypes, &interfaces.DraftObjectType{
					ID: "shipments", Name: "Shipments", Adopted: true, PrimaryKeys: []string{"shipment_id"},
					Table: interfaces.DraftTable{ResourceID: "r_ship", Name: "shipments", CatalogID: "c1"},
					Properties: []*interfaces.DraftProperty{
						{Name: "shipment_id", Type: "string", MappedField: "shipment_id"},
						{Name: "order_no", Type: "string", MappedField: "order_no"},
					},
				})
				return d
			}
			tables := []*interfaces.VegaTable{
				table("r_lines", "c1", "order_lines", 0, nil,
					col("order_no", "string", ""), col("line_no", "integer", ""), col("amount", "decimal", "")),
				table("r_customers", "c1", "customers", 0, []string{"ID"}, col("ID", "string", "")),
				table("r_ship", "c1", "shipments", 0, []string{"shipment_id"},
					col("shipment_id", "string", ""), col("order_no", "string", "")),
			}
			counts := func(orphans float64) {
				va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_lines", "r_customers", "r_ship"}).Return(tables, nil)
				va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
					DoAndReturn(func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
						if strings.Contains(sql, "LEFT JOIN") {
							So(sql, ShouldContainSubstring, "{{r_ship}}")
							So(sql, ShouldContainSubstring, "{{r_lines}}")
							return []map[string]any{{"total": float64(50), "nulls": float64(0), "orphans": orphans,
								"distinct_values": float64(20)}}, nil
						}
						return []map[string]any{{"total": float64(10), "distinct_values": float64(10), "nulls": float64(0)}}, nil
					}).Times(2)
			}

			Convey("Borne out, it is drafted with its counts, proposed while the order lines are not adopted\n", func() {
				d := withShipments()
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
				counts(0)
				expectWrite()

				got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

				So(err, ShouldBeNil)
				So(got.Items.RelationTypes, ShouldHaveLength, 1)
				r := got.Items.RelationTypes[0]
				So(r.Source, ShouldEqual, "shipments")
				So(r.Target, ShouldEqual, "order_lines")
				So(r.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "order_no", TargetProperty: "order_no"}})
				So(r.Evidence, ShouldResemble, interfaces.DraftJoinEvidence{
					Computed: true, Rows: 50, Hits: 50, HitRate: 1, SourceDistinct: 20,
				})
				So(r.Adopted, ShouldBeFalse)
				So(r.ProposedBecause, ShouldEqual, proposedEndNotAdopted)
			})

			Convey("Refuted by the counts, it is not drafted\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(withShipments(), true, nil)
				counts(30)
				expectWrite()

				got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

				So(err, ShouldBeNil)
				So(got.Items.RelationTypes, ShouldBeEmpty)
			})

			Convey("A relation the draft has is not drafted again\n", func() {
				d := withShipments()
				d.Items.RelationTypes = []*interfaces.DraftRelationType{{
					ID: "kept", Source: "shipments", Target: "order_lines",
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "order_no", TargetProperty: "order_no"}},
				}}
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
				counts(0)
				expectWrite()

				got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

				So(err, ShouldBeNil)
				So(got.Items.RelationTypes, ShouldHaveLength, 1)
				So(got.Items.RelationTypes[0].ID, ShouldEqual, "kept")
			})

			Convey("Tables that cannot be read draft no relation and keep the choice\n", func() {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(withShipments(), true, nil)
				va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
					Return([]map[string]any{{"total": float64(10), "distinct_values": float64(10), "nulls": float64(0)}}, nil)
				va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("vega is down"))
				expectWrite()

				got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("order_no"), caller)

				So(err, ShouldBeNil)
				So(got.Items.ObjectTypes[0].PrimaryKeys, ShouldResemble, []string{"order_no"})
				So(got.Items.RelationTypes, ShouldBeEmpty)
			})
		})

		Convey("Choosing the identity an object type already has changes nothing\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			got, err := svc.ChooseIdentity(ctx, token, "p1", "customers", choose("id"), caller)

			So(err, ShouldBeNil)
			So(got.UpdateTime, ShouldEqual, readTime)
			So(got.Items.Decisions, ShouldBeEmpty)
		})

		Convey("Only distinct key-typed properties of the object type identify it\n", func() {
			for _, properties := range [][]string{nil, {"order_no", "order_no"}, {"missing"}, {"amount"}} {
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(identityDraft(), true, nil)
				_, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose(properties...), caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
			}
		})

		Convey("A choice without the draft's update time is refused before the draft is read\n", func() {
			_, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", interfaces.DraftIdentity{Properties: []string{"order_no"}}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime)

			_, err = svc.RestoreExcluded(ctx, token, "p1", interfaces.DraftRestore{ObjectType: "customers", Field: "Phone"}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_MissingUpdateTime)
		})

		Convey("An unknown object type is not found and a stale choice is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).Times(2)
			_, err := svc.ChooseIdentity(ctx, token, "p1", "missing", choose("id"), caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)

			stale := choose("order_no")
			stale.UpdateTime = readTime - 1
			_, err = svc.ChooseIdentity(ctx, token, "p1", "order_lines", stale, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_Stale)
		})
	})
}

func Test_RestoreExcluded(t *testing.T) {
	Convey("Test restoring an excluded field\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, ira: &fakeIDRegistry{}, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := identityDraft()
		restore := func(objectType, field string) interfaces.DraftRestore {
			return interfaces.DraftRestore{UpdateTime: readTime, ObjectType: objectType, Field: field}
		}
		customers := &interfaces.VegaTable{ID: "r_customers", Name: "customers", CatalogID: "c1", Columns: []interfaces.VegaColumn{
			{Name: "ID", Type: "string"},
			{Name: "Phone", Type: "string", DisplayName: "Mobile phone", Description: "Contact number"},
		}}

		Convey("A privacy exclusion becomes a property read from the table as the caller\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers"}).Return([]*interfaces.VegaTable{customers}, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)

			got, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Phone"), caller)

			So(err, ShouldBeNil)
			properties := got.Items.ObjectTypes[1].Properties
			So(len(properties), ShouldEqual, 2)
			So(*properties[1], ShouldResemble, interfaces.DraftProperty{
				Name: "phone", DisplayName: "Mobile phone", Type: "string", Description: "Contact number",
				MappedField: "Phone", SourceOfRecord: interfaces.SOURCE_OF_RECORD_TABLE,
			})
			So(len(got.Items.Excluded), ShouldEqual, 1)
			So(got.Items.Excluded[0].Field, ShouldEqual, "area")
			decision := got.Items.Decisions[0]
			So(decision.Kind, ShouldEqual, interfaces.DRAFT_KIND_EXCLUDED)
			So(decision.ItemID, ShouldEqual, "customers")
			So(decision.Action, ShouldEqual, interfaces.DRAFT_DECISION_RESTORE)
			So(decision.Before, ShouldResemble, interfaces.DraftExcludedField{
				ObjectType: "customers", Field: "Phone", Reason: excludedPrivacy, Keyword: "phone",
			})
			So(decision.After, ShouldEqual, "phone")
		})

		Convey("A restored field is named in the language the draft named in\n", func() {
			customers.Columns[1].Comment = "手机号码"
			for language, want := range map[string]string{
				// Contact number is the other property's display name: not an alias.
				interfaces.NAMING_LANGUAGE_ZH_CN: "手机号码 | Mobile phone",
				interfaces.NAMING_LANGUAGE_EN_US: "Mobile phone | 手机号码",
			} {
				d := identityDraft()
				d.Items.ObjectTypes[1].Properties[0].DisplayName = "Contact number"
				d.Items.Inputs = &interfaces.DraftInputs{NamingLanguage: language}
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
				va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers"}).Return([]*interfaces.VegaTable{customers}, nil)
				da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)

				got, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Phone"), caller)

				So(err, ShouldBeNil)
				restored := got.Items.ObjectTypes[1].Properties[1]
				So(restored.DisplayName+" | "+strings.Join(restored.Aliases, ","), ShouldEqual, want)
			}
		})

		Convey("A restored field gets a property name no other property has\n", func() {
			draft.Items.ObjectTypes[1].Properties = append(draft.Items.ObjectTypes[1].Properties,
				&interfaces.DraftProperty{Name: "phone", Type: "string", MappedField: "phone_no"})
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers"}).Return([]*interfaces.VegaTable{customers}, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)

			got, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Phone"), caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[1].Properties[2].Name, ShouldEqual, "phone_2")
		})

		Convey("A field excluded for its type cannot be restored\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			_, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "area"), caller)

			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_NotRestorable)
		})

		Convey("A field no longer in its table, or a table the caller cannot read, is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).Times(2)
			gone := &interfaces.VegaTable{ID: "r_customers", Columns: []interfaces.VegaColumn{{Name: "ID", Type: "string"}}}
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers"}).Return([]*interfaces.VegaTable{gone}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_customers"}).Return(nil, nil)

			_, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Phone"), caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
			_, err = svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Phone"), caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
		})

		Convey("A field that is not excluded is not found\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			_, err := svc.RestoreExcluded(ctx, token, "p1", restore("customers", "Email"), caller)

			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
		})
	})
}
