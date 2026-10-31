// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
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

// relationDraft has orders of customers in catalog c1, tags in c2, and the relation drafting found
// from orders to customers.
func relationDraft() *interfaces.Draft {
	ot := func(id string, catalog string, keys []string, props ...*interfaces.DraftProperty) *interfaces.DraftObjectType {
		return &interfaces.DraftObjectType{ID: id, Name: "The " + id, Adopted: true, PrimaryKeys: keys, Properties: props,
			Table: interfaces.DraftTable{ResourceID: "r_" + id, Name: id, CatalogID: catalog}}
	}
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				ot("orders", "c1", []string{"order_id"},
					&interfaces.DraftProperty{Name: "order_id", Type: "integer", MappedField: "id"},
					&interfaces.DraftProperty{Name: "customer_id", Type: "unsigned integer", MappedField: "customer_id"},
					&interfaces.DraftProperty{Name: "code", Type: "string", MappedField: "code"}),
				ot("customers", "c1", []string{"id"},
					&interfaces.DraftProperty{Name: "id", Type: "integer", MappedField: "id"},
					&interfaces.DraftProperty{Name: "name", Type: "text", MappedField: "name"}),
				ot("tags", "c2", []string{"tag_id"}, &interfaces.DraftProperty{Name: "tag_id", Type: "string", MappedField: "tag_id"}),
			},
			RelationTypes: []*interfaces.DraftRelationType{{
				ID: "orders_to_customers", Name: "Customer of order", Source: "orders", Target: "customers", Adopted: true,
				Origin: interfaces.DRAFT_ORIGIN_FOREIGN_KEY, Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "customer_id", TargetProperty: "id"}},
			}},
		},
	}
}

func direct(source, target string, pairs ...[2]string) interfaces.DraftRelationTypeSave {
	rules := []interfaces.DraftRelationMapping{}
	for _, p := range pairs {
		rules = append(rules, interfaces.DraftRelationMapping{SourceProperty: interfaces.DraftMappedField{Name: p[0]},
			TargetProperty: interfaces.DraftMappedField{Name: p[1]}})
	}
	raw, _ := json.Marshal(rules)
	return interfaces.DraftRelationTypeSave{UpdateTime: readTime, Name: "Relation", SourceObjectTypeID: source,
		TargetObjectTypeID: target, Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: raw}
}

func indirect(source, target, table string, sourceRules [][2]string, targetRules [][2]string) interfaces.DraftRelationTypeSave {
	rules := func(pairs [][2]string) []interfaces.DraftRelationMapping {
		out := []interfaces.DraftRelationMapping{}
		for _, p := range pairs {
			out = append(out, interfaces.DraftRelationMapping{SourceProperty: interfaces.DraftMappedField{Name: p[0]},
				TargetProperty: interfaces.DraftMappedField{Name: p[1]}})
		}
		return out
	}
	raw, _ := json.Marshal(interfaces.DraftIndirectMapping{BackingDataSource: &interfaces.DraftBackingSource{Type: "resource", ID: table},
		SourceMappingRules: rules(sourceRules), TargetMappingRules: rules(targetRules)})
	return interfaces.DraftRelationTypeSave{UpdateTime: readTime, Name: "Tags of order", SourceObjectTypeID: source,
		TargetObjectTypeID: target, Type: interfaces.DRAFT_RELATION_INDIRECT, MappingRules: raw}
}

func orderTagsTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "r_order_tags", Name: "order_tags", CatalogID: "c2", Columns: []interfaces.VegaColumn{
		{Name: "order_id", Type: "integer"}, {Name: "tag_id", Type: "string"}, {Name: "shape", Type: "other"},
	}}
}

func Test_RelationTypeSave(t *testing.T) {
	Convey("Test making, saving and deleting relation types a person defines\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, ps: ps, va: va}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		draft := relationDraft()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_order_tags"}).Return([]*interfaces.VegaTable{orderTagsTable()}, nil).AnyTimes()
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
		}
		detailsOf := func(err error) (int, string, any) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode, httpErr.ErrorDetails
		}
		reasonOf := func(err error) string {
			status, code, details := detailsOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)
			So(code, ShouldEqual, perrors.BknProject_Draft_InvalidRelation)
			return details.(map[string]any)["reason"].(string)
		}
		last := func(d *interfaces.Draft) *interfaces.DraftRelationType {
			return d.Items.RelationTypes[len(d.Items.RelationTypes)-1]
		}

		Convey("A direct relation is made between object types of the draft, adopted with them, and recorded\n", func() {
			expectWrite()
			save := direct("orders", "tags", [2]string{"code", "tag_id"})
			save.Comment = " Tags by code "

			got, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

			So(err, ShouldBeNil)
			rt := last(got)
			So(rt.ID, ShouldEqual, "orders_to_tags")
			So(rt.Name, ShouldEqual, "Relation")
			So(rt.Description, ShouldEqual, "Tags by code")
			So(rt.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_MANUAL)
			So(rt.Adopted, ShouldBeTrue)
			So(rt.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_MANY_TO_ONE)
			So(rt.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "code", TargetProperty: "tag_id"}})
			// Across catalogs a join is not counted, as drafting does not count it.
			So(rt.Evidence.Reason, ShouldEqual, reasonCrossCatalog)
			d := got.Items.Decisions[len(got.Items.Decisions)-1]
			So(d.Action, ShouldEqual, interfaces.DRAFT_DECISION_CREATE)
			So(d.After.(interfaces.DraftRelationTypeSnapshot).Target, ShouldEqual, "tags")
		})

		Convey("A join to the target's identity within a catalog is counted over the full table\n", func() {
			expectWrite()
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				Return([]map[string]any{{"total": int64(100), "nulls": int64(0), "orphans": int64(5), "distinct_values": int64(80)}}, nil)
			save := direct("customers", "orders", [2]string{"id", "order_id"})
			save.ID = "customer_orders"

			got, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

			So(err, ShouldBeNil)
			rt := last(got)
			So(rt.ID, ShouldEqual, "customer_orders")
			So(rt.Evidence.Computed, ShouldBeTrue)
			So(rt.Evidence.Rows, ShouldEqual, 100)
			So(rt.Evidence.Orphans, ShouldEqual, 5)
			// The source's identity maps to one record at most.
			So(rt.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_ONE_TO_ONE)
		})

		Convey("A join that is not to the target's single identity is not counted, and says why\n", func() {
			expectWrite()

			got, err := svc.CreateRelationType(ctx, token, "p1", direct("orders", "customers", [2]string{"order_id", "id"}, [2]string{"code", "name"}), caller)

			So(err, ShouldBeNil)
			So(last(got).Evidence.Reason, ShouldEqual, reasonCompositeKey)
		})

		Convey("A join to a target property that is not its identity is not counted, and says why\n", func() {
			expectWrite()

			got, err := svc.CreateRelationType(ctx, token, "p1", direct("orders", "customers", [2]string{"code", "name"}), caller)

			So(err, ShouldBeNil)
			So(last(got).Evidence.Reason, ShouldEqual, reasonNotIdentity)
		})

		Convey("An indirect relation joins through the fields of its backing table\n", func() {
			expectWrite()
			save := indirect("orders", "tags", "r_order_tags", [][2]string{{"order_id", "order_id"}}, [][2]string{{"tag_id", "tag_id"}})

			got, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

			So(err, ShouldBeNil)
			rt := last(got)
			So(rt.LinkTable.ResourceID, ShouldEqual, "r_order_tags")
			So(rt.SourceMapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "order_id"}})
			So(rt.TargetMapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "tag_id", TargetProperty: "tag_id"}})
			So(rt.Cardinality, ShouldEqual, interfaces.DRAFT_CARDINALITY_MANY_TO_MANY)
			So(rt.Evidence.Reason, ShouldEqual, reasonLinkTable)
		})

		Convey("A relation to an object type not adopted is not adopted either\n", func() {
			expectWrite()
			draft.Items.ObjectTypes[2].Adopted = false

			got, err := svc.CreateRelationType(ctx, token, "p1", direct("orders", "tags", [2]string{"code", "tag_id"}), caller)

			So(err, ShouldBeNil)
			So(last(got).Adopted, ShouldBeFalse)
			So(last(got).ProposedBecause, ShouldEqual, proposedEndNotAdopted)
		})

		Convey("A relation made before its end is adopted is adopted with the end, whatever its counts\n", func() {
			expectWrite()
			draft.Items.ObjectTypes[1].Adopted = false
			// Not to the customers' identity: drafting's gate would refuse it uncounted.
			got, err := svc.CreateRelationType(ctx, token, "p1", direct("orders", "customers", [2]string{"code", "name"}), caller)
			So(err, ShouldBeNil)
			made := last(got)
			So(made.Adopted, ShouldBeFalse)

			// Adopting the end regates the relations it was waiting for, as editing does.
			draft.Items.ObjectTypes[1].Adopted = true
			decisions := regateRelations(testSetting, &got.Items, "customers")

			So(made.Adopted, ShouldBeTrue)
			So(made.ProposedBecause, ShouldBeEmpty)
			So(decisions, ShouldNotBeEmpty)
		})

		Convey("A join another relation has already is refused, naming it\n", func() {
			_, err := svc.CreateRelationType(ctx, token, "p1", direct("orders", "customers", [2]string{"customer_id", "id"}), caller)

			_, _, details := detailsOf(err)
			So(details.(map[string]any)["reason"], ShouldEqual, relationDuplicate)
			So(details.(map[string]any)["relation"], ShouldResemble,
				interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: "orders_to_customers", Name: "Customer of order"})
		})

		Convey("A definition the draft or the backing table cannot hold is refused, saying why\n", func() {
			badType := direct("orders", "tags", [2]string{"code", "tag_id"})
			badType.Type = "graph"
			takenID := direct("orders", "tags", [2]string{"code", "tag_id"})
			takenID.ID = "orders_to_customers"
			badID := direct("orders", "tags", [2]string{"code", "tag_id"})
			badID.ID = "Bad Id"
			for reason, save := range map[string]interfaces.DraftRelationTypeSave{
				relationUnknownEnd:      direct("orders", "nope", [2]string{"code", "tag_id"}),
				relationUnknownProperty: direct("orders", "tags", [2]string{"nope", "tag_id"}),
				relationTypeMismatch:    direct("orders", "customers", [2]string{"code", "id"}),
				relationNoMapping:       direct("orders", "tags"),
				relationInvalidType:     badType,
				relationInvalidID:       badID,
				relationUnknownField: indirect("orders", "tags", "r_order_tags",
					[][2]string{{"order_id", "nope"}}, [][2]string{{"tag_id", "tag_id"}}),
				relationTypeMismatch + " through the backing table": indirect("orders", "tags", "r_order_tags",
					[][2]string{{"code", "order_id"}}, [][2]string{{"tag_id", "tag_id"}}),
				relationNoMapping + " of an end": indirect("orders", "tags", "r_order_tags", [][2]string{{"order_id", "order_id"}}, nil),
				"unknown_field of no BKN type": indirect("orders", "tags", "r_order_tags",
					[][2]string{{"order_id", "order_id"}}, [][2]string{{"shape", "tag_id"}}),
			} {
				Convey(reason+"\n", func() {
					_, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

					So(reason, ShouldStartWith, reasonOf(err))
				})
			}
			Convey("an id taken\n", func() {
				_, err := svc.CreateRelationType(ctx, token, "p1", takenID, caller)
				So(reasonOf(err), ShouldEqual, relationInvalidID)
			})
		})

		Convey("A backing table the caller cannot read is refused\n", func() {
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_gone"}).Return(nil, nil)

			_, err := svc.CreateRelationType(ctx, token, "p1",
				indirect("orders", "tags", "r_gone", [][2]string{{"order_id", "order_id"}}, [][2]string{{"tag_id", "tag_id"}}), caller)

			status, code, _ := detailsOf(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Draft_FieldNotFound)
		})

		Convey("A name that is empty is refused\n", func() {
			save := direct("orders", "tags", [2]string{"code", "tag_id"})
			save.Name = " "

			_, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

			_, code, _ := detailsOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_InvalidName)
		})

		Convey("Saving a relation records its new join beside its renaming, and keeps its origin\n", func() {
			expectWrite()
			// Its new join is to the customers' identity within the catalog: counted again.
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				Return([]map[string]any{{"total": int64(10), "nulls": int64(0), "orphans": int64(0), "distinct_values": int64(10)}}, nil)
			save := direct("orders", "customers", [2]string{"order_id", "id"})
			save.Name = "Order's customer by number"

			got, err := svc.SaveRelationType(ctx, token, "p1", "orders_to_customers", save, caller)

			So(err, ShouldBeNil)
			rt := got.Items.RelationTypes[0]
			So(rt.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "order_id", TargetProperty: "id"}})
			So(rt.Origin, ShouldEqual, interfaces.DRAFT_ORIGIN_FOREIGN_KEY)
			So(rt.Name, ShouldEqual, "Order's customer by number")
			So(rt.Evidence.Computed, ShouldBeTrue)
			actions := []string{}
			for _, d := range got.Items.Decisions {
				actions = append(actions, d.Action)
			}
			So(actions, ShouldResemble, []string{interfaces.DRAFT_DECISION_RENAME, interfaces.DRAFT_DECISION_SAVE})
			saved := got.Items.Decisions[1]
			So(saved.Before.(interfaces.DraftRelationTypeSnapshot).Mapping[0].SourceProperty, ShouldEqual, "customer_id")
		})

		Convey("Saving a relation onto an end not adopted takes it out\n", func() {
			expectWrite()
			draft.Items.ObjectTypes[2].Adopted = false

			got, err := svc.SaveRelationType(ctx, token, "p1", "orders_to_customers",
				direct("orders", "tags", [2]string{"code", "tag_id"}), caller)

			So(err, ShouldBeNil)
			So(got.Items.RelationTypes[0].Adopted, ShouldBeFalse)
			So(got.Items.RelationTypes[0].ProposedBecause, ShouldEqual, proposedEndNotAdopted)
		})

		Convey("Saving a relation as it is writes nothing; saving its own join is no duplicate\n", func() {
			save := direct("orders", "customers", [2]string{"customer_id", "id"})
			save.Name = "Customer of order"

			got, err := svc.SaveRelationType(ctx, token, "p1", "orders_to_customers", save, caller)

			So(err, ShouldBeNil)
			So(got.Items.Decisions, ShouldBeEmpty)
		})

		Convey("Saving a relation onto another's join, or an unknown one, is refused\n", func() {
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{
				ID: "order_tags", Name: "Tags", Source: "orders", Target: "tags", Origin: interfaces.DRAFT_ORIGIN_MANUAL,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "code", TargetProperty: "tag_id"}}})

			_, err := svc.SaveRelationType(ctx, token, "p1", "orders_to_customers", direct("orders", "tags", [2]string{"code", "tag_id"}), caller)
			So(reasonOf(err), ShouldEqual, relationDuplicate)

			_, err = svc.SaveRelationType(ctx, token, "p1", "nope", direct("orders", "tags", [2]string{"code", "tag_id"}), caller)
			status, _, _ := detailsOf(err)
			So(status, ShouldEqual, http.StatusNotFound)
		})

		Convey("A relation a person made is deleted and recorded; one drafting found is left out instead\n", func() {
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{
				ID: "order_tags", Name: "Tags", Source: "orders", Target: "tags", Origin: interfaces.DRAFT_ORIGIN_MANUAL,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "code", TargetProperty: "tag_id"}}})

			_, err := svc.DeleteRelationType(ctx, token, "p1", "orders_to_customers", readTime, caller)
			So(reasonOf(err), ShouldEqual, relationNotManual)

			_, err = svc.DeleteRelationType(ctx, token, "p1", "nope", readTime, caller)
			status, _, _ := detailsOf(err)
			So(status, ShouldEqual, http.StatusNotFound)

			expectWrite()
			got, err := svc.DeleteRelationType(ctx, token, "p1", "order_tags", readTime, caller)
			So(err, ShouldBeNil)
			So(got.Items.RelationTypes, ShouldHaveLength, 1)
			d := got.Items.Decisions[len(got.Items.Decisions)-1]
			So(d.Action, ShouldEqual, interfaces.DRAFT_DECISION_DELETE)
			So(d.Before.(interfaces.DraftRelationTypeSnapshot).Name, ShouldEqual, "Tags")
		})

		Convey("A draft changed since it was read is refused\n", func() {
			save := direct("orders", "tags", [2]string{"code", "tag_id"})
			save.UpdateTime = readTime - 1

			_, err := svc.CreateRelationType(ctx, token, "p1", save, caller)

			_, code, _ := detailsOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Stale)
		})
	})
}
