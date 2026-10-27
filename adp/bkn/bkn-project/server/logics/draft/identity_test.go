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
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := identityDraft()
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
		}
		choose := func(properties ...string) interfaces.DraftIdentity {
			return interfaces.DraftIdentity{UpdateTime: readTime, Properties: properties}
		}

		Convey("A single-field identity is counted over the full table and kept even with duplicates\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).
				DoAndReturn(func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
					So(sql, ShouldContainSubstring, "COUNT(DISTINCT `order_no`)")
					So(sql, ShouldContainSubstring, "{{r_lines}}")
					return []map[string]any{{"total": float64(10), "distinct_values": float64(9), "nulls": float64(0)}}, nil
				})
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
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).Return(nil, errors.New("timeout"))
			expectWrite()

			got, err := svc.ChooseIdentity(ctx, token, "p1", "order_lines", choose("line_no"), caller)

			So(err, ShouldBeNil)
			key := got.Items.ObjectTypes[0].Key
			So(key.Computed, ShouldBeFalse)
			So(key.Field, ShouldEqual, "line_no")
			So(key.Reason, ShouldStartWith, reasonQueryFailed)
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
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, run: func(fn func()) { fn() }}
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
