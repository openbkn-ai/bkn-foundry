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

// syncedDraft binds customers to t_customers, orders to t_orders, and customer tags through the
// link table t_customer_tags. customers keeps phone out by the privacy rules.
func syncedDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{
					ID: "customers", Name: "Customers", Adopted: true,
					Table:       interfaces.DraftTable{ResourceID: "t_customers", Name: "customers"},
					PrimaryKeys: []string{"id"}, DisplayKey: "name", IncrementalKey: "updated",
					Key: interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_PRIMARY_KEY},
					Properties: []*interfaces.DraftProperty{
						{Name: "id", DisplayName: "Customer ID", Type: "integer", MappedField: "id"},
						{Name: "name", DisplayName: "Customer name", Type: "string", MappedField: "name"},
						{Name: "level", DisplayName: "Level", Type: "string", MappedField: "level"},
						{Name: "updated", DisplayName: "Updated", Type: "datetime", MappedField: "updated"},
					},
				},
				{
					ID: "orders", Name: "Orders", Adopted: true,
					Table:       interfaces.DraftTable{ResourceID: "t_orders", Name: "orders"},
					PrimaryKeys: []string{"id"}, DisplayKey: "id",
					Properties: []*interfaces.DraftProperty{
						{Name: "id", DisplayName: "Order ID", Type: "integer", MappedField: "id"},
						{Name: "customer_id", DisplayName: "Customer", Type: "integer", MappedField: "customer_id"},
					},
				},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{
					ID: "orders_customers", Name: "Orders customers", Source: "orders", Target: "customers", Adopted: true,
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "customer_id", TargetProperty: "id"}},
				},
				{
					ID: "customer_tags", Name: "Customer tags", Source: "customers", Target: "orders", Adopted: true,
					LinkTable:     &interfaces.DraftTable{ResourceID: "t_customer_tags", Name: "customer_tags"},
					SourceMapping: []interfaces.DraftFieldMapping{{SourceProperty: "id", TargetProperty: "customer_id"}},
					TargetMapping: []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "id"}},
				},
			},
			ValueTypes: []*interfaces.DraftValueType{{ID: "levels", Name: "Levels", Adopted: true,
				UsedBy: []interfaces.DraftPropertyRef{{ObjectType: "customers", Property: "level"}}}},
			Excluded: []*interfaces.DraftExcludedField{{ObjectType: "customers", Field: "phone", Reason: excludedPrivacy, Keyword: "phone"}},
			Inputs: &interfaces.DraftInputs{Tables: []interfaces.DraftInputTable{
				{ResourceID: "t_customers", Fingerprint: "old"}, {ResourceID: "t_orders", Fingerprint: "old"},
			}},
			Decisions: []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "orders",
				Action: interfaces.DRAFT_DECISION_RENAME, Before: "Order", After: "Orders"}},
		},
	}
}

// Tables as the draft binds them.
func customersTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "t_customers", Name: "customers", PrimaryKeys: []string{"id"}, Columns: []interfaces.VegaColumn{
		{Name: "id", Type: "integer"}, {Name: "name", Type: "string"}, {Name: "phone", Type: "string"},
		{Name: "level", Type: "string"}, {Name: "updated", Type: "datetime"},
	}}
}

func ordersTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "t_orders", Name: "orders", PrimaryKeys: []string{"id"}, Columns: []interfaces.VegaColumn{
		{Name: "id", Type: "integer"}, {Name: "customer_id", Type: "integer"},
	}}
}

func customerTagsTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "t_customer_tags", Name: "customer_tags", Columns: []interfaces.VegaColumn{
		{Name: "customer_id", Type: "integer"}, {Name: "order_id", Type: "integer"},
	}}
}

func readOf(missing []string, tables ...*interfaces.VegaTable) *sourceTables {
	read := &sourceTables{tables: map[string]*interfaces.VegaTable{}, missing: map[string]bool{}}
	for _, t := range tables {
		read.tables[t.ID] = t
	}
	for _, id := range missing {
		read.missing[id] = true
	}
	return read
}

func changeOf(changes *interfaces.SourceChanges, id string, field string) *interfaces.SourceColumnChange {
	for _, ot := range changes.ObjectTypes {
		if ot.ID != id {
			continue
		}
		for i := range ot.Changes {
			if ot.Changes[i].Field == field {
				return &ot.Changes[i]
			}
		}
	}
	return nil
}

func Test_DraftSourceChangesCompare(t *testing.T) {
	Convey("Test comparing the draft with its tables\n", t, func() {
		draft := syncedDraft()

		Convey("Tables as drafted are in step, and an excluded column is not added\n", func() {
			got := draftSourceChanges(draft, readOf(nil, customersTable(), ordersTable(), customerTagsTable()), testSetting.PrivacyKeywords)

			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_IN_STEP)
			So(got.ObjectTypes, ShouldBeEmpty)
			So(got.RelationTypes, ShouldBeEmpty)
			So(got.Signature, ShouldNotBeEmpty)
		})

		Convey("Added, removed and retyped columns are listed by the object type's and fields' names\n", func() {
			customers := customersTable()
			customers.Columns = []interfaces.VegaColumn{
				{Name: "id", Type: "integer"}, {Name: "name", Type: "text"}, {Name: "phone", Type: "string"},
				{Name: "updated", Type: "datetime"},
				{Name: "segment", DisplayName: "Customer segment", Type: "string"},
				{Name: "email", Type: "string"},
			}

			got := draftSourceChanges(draft, readOf(nil, customers, ordersTable(), customerTagsTable()), testSetting.PrivacyKeywords)

			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_CHANGED)
			So(got.ObjectTypes, ShouldHaveLength, 1)
			So(got.ObjectTypes[0].Name, ShouldEqual, "Customers")
			So(got.ObjectTypes[0].InDraft, ShouldBeTrue)
			So(*changeOf(got, "customers", "name"), ShouldResemble, interfaces.SourceColumnChange{
				Change: interfaces.SOURCE_CHANGE_RETYPED, Field: "name", FieldName: "name", Property: "name",
				PropertyName: "Customer name", FromType: "string", ToType: "text", Key: true,
			})
			So(changeOf(got, "customers", "level").Change, ShouldEqual, interfaces.SOURCE_CHANGE_REMOVED)
			So(changeOf(got, "customers", "level").PropertyName, ShouldEqual, "Level")
			So(*changeOf(got, "customers", "segment"), ShouldResemble, interfaces.SourceColumnChange{
				Change: interfaces.SOURCE_CHANGE_ADDED, Field: "segment", FieldName: "Customer segment", ToType: "string",
			})
			So(changeOf(got, "customers", "email").Excluded, ShouldEqual, excludedPrivacy)
			So(changeOf(got, "customers", "phone"), ShouldBeNil)
		})

		Convey("A removed key column names the relations joining by it\n", func() {
			customers := customersTable()
			customers.Columns = customers.Columns[1:]

			got := draftSourceChanges(draft, readOf(nil, customers, ordersTable(), customerTagsTable()), testSetting.PrivacyKeywords)

			c := changeOf(got, "customers", "id")
			So(c.Change, ShouldEqual, interfaces.SOURCE_CHANGE_REMOVED)
			So(c.Key, ShouldBeTrue)
			So(c.Relations, ShouldHaveLength, 2)
		})

		Convey("A link table losing a field it joins by lists its relation type\n", func() {
			tags := customerTagsTable()
			tags.Columns = tags.Columns[:1]

			got := draftSourceChanges(draft, readOf(nil, customersTable(), ordersTable(), tags), testSetting.PrivacyKeywords)

			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_CHANGED)
			So(got.RelationTypes, ShouldResemble, []interfaces.SourceRelationTypeChange{
				{ID: "customer_tags", Name: "Customer tags", Table: "customer_tags", Removed: []string{"order_id"}},
			})
		})

		Convey("A gone table is listed once as missing, not as every column removed\n", func() {
			got := draftSourceChanges(draft, readOf([]string{"t_orders"}, customersTable(), customerTagsTable()), testSetting.PrivacyKeywords)

			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_IN_STEP)
			So(got.ObjectTypes, ShouldBeEmpty)
			So(got.MissingTables, ShouldResemble, []interfaces.ProjectStatusAffected{
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: "orders", Name: "Orders", Table: "orders"},
			})
		})

		Convey("The signature changes with the tables and with the draft\n", func() {
			read := readOf(nil, customersTable(), ordersTable(), customerTagsTable())
			before := sourceSignature(draft, read)
			customers := customersTable()
			customers.Columns[1].Type = "text"

			So(sourceSignature(draft, readOf(nil, customers, ordersTable(), customerTagsTable())), ShouldNotEqual, before)
			draft.UpdateTime++
			So(sourceSignature(draft, read), ShouldNotEqual, before)
		})
	})
}

func Test_ApplySourceChanges(t *testing.T) {
	Convey("Test drafting the source changes\n", t, func() {
		draft := syncedDraft()
		apply := func(tables ...*interfaces.VegaTable) []*interfaces.DraftDecision {
			read := readOf(nil, tables...)
			changes := draftSourceChanges(draft, read, testSetting.PrivacyKeywords)
			return applySourceChanges(&draft.Items, changes, read, testSetting.PrivacyKeywords, testSetting.NameFieldKeywords)
		}

		Convey("Added columns become properties or excluded fields by the drafting rules\n", func() {
			customers := customersTable()
			customers.Columns = append(customers.Columns,
				interfaces.VegaColumn{Name: "segment", DisplayName: "Customer segment", Type: "string"},
				interfaces.VegaColumn{Name: "email", Type: "string"},
				interfaces.VegaColumn{Name: "shape", Type: "geometry"})

			decisions := apply(customers, ordersTable(), customerTagsTable())

			ot := draft.Items.ObjectTypes[0]
			So(findProperty(ot, "segment"), ShouldNotBeNil)
			So(findProperty(ot, "segment").DisplayName, ShouldEqual, "Customer segment")
			So(findProperty(ot, "email"), ShouldBeNil)
			So(draft.Items.Excluded, ShouldHaveLength, 3)
			So(draft.Items.Excluded[1].Reason, ShouldEqual, excludedPrivacy)
			So(draft.Items.Excluded[2].Reason, ShouldEqual, excludedUnsupported)
			So(decisions, ShouldHaveLength, 1)
			So(decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_SYNC)
			So(draft.Items.Inputs.Tables[0].Fingerprint, ShouldEqual, fingerprint(customers))
			So(draft.Items.Inputs.Tables[1].Fingerprint, ShouldEqual, "old")
		})

		Convey("A removed column takes its property, its value type use and its update field away\n", func() {
			customers := customersTable()
			customers.Columns = []interfaces.VegaColumn{{Name: "id", Type: "integer"}, {Name: "name", Type: "string"}, {Name: "phone", Type: "string"}}

			apply(customers, ordersTable(), customerTagsTable())

			ot := draft.Items.ObjectTypes[0]
			So(findProperty(ot, "level"), ShouldBeNil)
			So(findProperty(ot, "updated"), ShouldBeNil)
			So(ot.IncrementalKey, ShouldBeEmpty)
			So(draft.Items.ValueTypes[0].UsedBy, ShouldBeEmpty)
			So(ot.NeedsIdentity, ShouldBeFalse)
			So(draft.Items.RelationTypes[0].Adopted, ShouldBeTrue)
		})

		Convey("A removed key makes the object type wait for its identity and un-adopts the relations joining by it\n", func() {
			customers := customersTable()
			customers.Columns = customers.Columns[1:]

			decisions := apply(customers, ordersTable(), customerTagsTable())

			ot := draft.Items.ObjectTypes[0]
			So(ot.NeedsIdentity, ShouldBeTrue)
			So(ot.PrimaryKeys, ShouldBeEmpty)
			So(ot.Adopted, ShouldBeTrue)
			So(ot.DisplayKey, ShouldEqual, "name")
			for _, r := range draft.Items.RelationTypes {
				So(r.Adopted, ShouldBeFalse)
				So(r.ProposedBecause, ShouldEqual, interfaces.DRAFT_PROPOSED_FIELD_REMOVED)
			}
			So(decisions, ShouldHaveLength, 3)
		})

		Convey("A retyped column retypes its property; a key retyped to a type that cannot key loses the identity\n", func() {
			customers := customersTable()
			customers.Columns[0].Type = "float"
			customers.Columns[1].Type = "text"

			apply(customers, ordersTable(), customerTagsTable())

			ot := draft.Items.ObjectTypes[0]
			So(findProperty(ot, "name").Type, ShouldEqual, "text")
			So(findProperty(ot, "id").Type, ShouldEqual, "float")
			So(ot.NeedsIdentity, ShouldBeTrue)
		})

		Convey("A column retyped to a type BKN cannot map is excluded\n", func() {
			customers := customersTable()
			customers.Columns[3].Type = "geometry"

			apply(customers, ordersTable(), customerTagsTable())

			So(findProperty(draft.Items.ObjectTypes[0], "level"), ShouldBeNil)
			So(draft.Items.Excluded[1].Field, ShouldEqual, "level")
			So(draft.Items.Excluded[1].Reason, ShouldEqual, excludedUnsupported)
		})

		Convey("A link table losing a join field un-adopts its relation only\n", func() {
			tags := customerTagsTable()
			tags.Columns = tags.Columns[:1]

			apply(customersTable(), ordersTable(), tags)

			So(draft.Items.RelationTypes[0].Adopted, ShouldBeTrue)
			So(draft.Items.RelationTypes[1].Adopted, ShouldBeFalse)
			So(draft.Items.RelationTypes[1].ProposedBecause, ShouldEqual, interfaces.DRAFT_PROPOSED_FIELD_REMOVED)
		})
	})
}

const publishedRegions = "---\ntype: object_type\nid: regions\nname: Regions\n---\n\n## ObjectType: Regions\n\n" +
	"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | t_regions | regions |\n\n" +
	"### Data Properties\n\n| Name | Display Name | Type | Mapped Field |\n|------|------|------|------|\n" +
	"| code | Region code | string | code |\n| label | Region label | string | label |\n"

const publishedCustomers = "---\ntype: object_type\nid: customers\nname: Customers\n---\n\n## ObjectType: Customers\n\n" +
	"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | t_customers | customers |\n"

func Test_GetSourceChanges(t *testing.T) {
	Convey("Test reading a project's source changes\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, gs: gs}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(syncedDraft(), true, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/main").Return(map[string][]byte{
			"object_types/regions.bkn":   []byte(publishedRegions),
			"object_types/customers.bkn": []byte(publishedCustomers),
		}, nil)
		ids := []string{"t_customers", "t_orders", "t_customer_tags", "t_regions"}

		Convey("A type only published is compared as published; one in the draft is marked published\n", func() {
			customers := customersTable()
			customers.Columns = append(customers.Columns, interfaces.VegaColumn{Name: "segment", Type: "string"})
			regions := &interfaces.VegaTable{ID: "t_regions", Columns: []interfaces.VegaColumn{{Name: "code", Type: "integer"}}}
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return(nil, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return([]*interfaces.VegaTable{customers, ordersTable(), customerTagsTable(), regions}, nil)

			got, err := svc.GetSourceChanges(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.ProjectID, ShouldEqual, "p1")
			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_CHANGED)
			So(got.ObjectTypes, ShouldHaveLength, 2)
			So(got.ObjectTypes[0].ID, ShouldEqual, "customers")
			So(got.ObjectTypes[0].InDraft && got.ObjectTypes[0].Published, ShouldBeTrue)
			So(got.ObjectTypes[1].ID, ShouldEqual, "regions")
			So(got.ObjectTypes[1].InDraft, ShouldBeFalse)
			So(got.ObjectTypes[1].Published, ShouldBeTrue)
			So(changeOf(got, "regions", "code").Change, ShouldEqual, interfaces.SOURCE_CHANGE_RETYPED)
			So(changeOf(got, "regions", "label").Change, ShouldEqual, interfaces.SOURCE_CHANGE_REMOVED)
			So(changeOf(got, "regions", "label").PropertyName, ShouldEqual, "Region label")
		})

		Convey("Gone tables come from MissingTables and are not read\n", func() {
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return([]string{"t_regions"}, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, ids[:3]).Return([]*interfaces.VegaTable{customersTable(), ordersTable(), customerTagsTable()}, nil)

			got, err := svc.GetSourceChanges(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_IN_STEP)
			So(got.MissingTables, ShouldResemble, []interfaces.ProjectStatusAffected{
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: "regions", Name: "Regions", Table: "regions"},
			})
		})

		Convey("vega not answering is unreachable, not in step\n", func() {
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return(nil, errors.New("down"))

			got, err := svc.GetSourceChanges(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, interfaces.SOURCE_CHANGES_UNREACHABLE)
			So(got.Signature, ShouldBeEmpty)
		})
	})
}

func Test_DraftSourceChanges(t *testing.T) {
	Convey("Test drafting a project's source changes\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		// No git store or lineage service: drafting the changes writes neither.
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller}}
		draft := syncedDraft()
		draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: readTime, Commit: "c1"}
		ids := []string{"t_customers", "t_orders", "t_customer_tags"}
		customers := customersTable()
		customers.Columns = append(customers.Columns, interfaces.VegaColumn{Name: "segment", Type: "string"})
		tables := []*interfaces.VegaTable{customers, ordersTable(), customerTagsTable()}
		signature := sourceSignature(draft, readOf(nil, tables...))
		expectRead := func() {
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return(nil, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return(tables, nil)
		}
		codeOf := func(err error) (int, string) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode
		}

		Convey("The owner drafts the changes read: decisions are kept and the confirmation goes out of date\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			expectRead()
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)

			got, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: signature}, caller)

			So(err, ShouldBeNil)
			So(findProperty(got.Items.ObjectTypes[0], "segment"), ShouldNotBeNil)
			So(got.Items.Decisions, ShouldHaveLength, 2)
			So(got.Items.Decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_RENAME)
			So(got.Items.Decisions[1].Action, ShouldEqual, interfaces.DRAFT_DECISION_SYNC)
			So(got.UpdateTime, ShouldBeGreaterThan, readTime)
			So(got.Items.Confirmation.UpdateTime, ShouldNotEqual, got.UpdateTime)
		})

		Convey("A signature of other changes, or none, is refused\n", func() {
			for _, sig := range []string{"", "other"} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(syncedDraft(), true, nil)
				expectRead()

				_, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: sig}, caller)

				code, errCode := codeOf(err)
				So(code, ShouldEqual, http.StatusConflict)
				So(errCode, ShouldEqual, perrors.BknProject_Draft_SourceChangesOutdated)
			}
		})

		Convey("Tables in step leave nothing to draft\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			inStep := []*interfaces.VegaTable{customersTable(), ordersTable(), customerTagsTable()}
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return(nil, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return(inStep, nil)

			_, err := svc.DraftSourceChanges(ctx, token, "p1",
				interfaces.SourceChangesDraft{Signature: sourceSignature(draft, readOf(nil, inStep...))}, caller)

			_, errCode := codeOf(err)
			So(errCode, ShouldEqual, perrors.BknProject_Draft_NoSourceChanges)
		})

		Convey("vega not answering is unavailable\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().MissingTables(gomock.Any(), caller, ids).Return(nil, errors.New("down"))

			_, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: signature}, caller)

			code, errCode := codeOf(err)
			So(code, ShouldEqual, http.StatusServiceUnavailable)
			So(errCode, ShouldEqual, perrors.BknProject_Draft_SourceUnreachable)
		})

		Convey("Someone other than the owner is refused\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)

			_, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: signature},
				interfaces.AccountInfo{ID: "u2", Type: "user"})

			code, _ := codeOf(err)
			So(code, ShouldEqual, http.StatusForbidden)
		})

		Convey("An archived project is refused\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: signature}, caller)

			_, errCode := codeOf(err)
			So(errCode, ShouldEqual, perrors.BknProject_Draft_ProjectArchived)
		})

		Convey("A draft still drafting is refused\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			drafting := syncedDraft()
			drafting.Status = interfaces.DRAFT_STATUS_DRAFTING
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(drafting, true, nil)

			_, err := svc.DraftSourceChanges(ctx, token, "p1", interfaces.SourceChangesDraft{Signature: signature}, caller)

			_, errCode := codeOf(err)
			So(errCode, ShouldEqual, perrors.BknProject_Draft_NotReady)
		})
	})
}
