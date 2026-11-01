// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// Tested on a logistics draft: shipments are connected to nothing yet; stations, routes and
// deliveries are; carriers are in another catalog.

func logisticsTables() []*interfaces.VegaTable {
	return []*interfaces.VegaTable{
		table("r_shipments", "c1", "shipments", 100, []string{"waybill"},
			col("waybill", "string", ""), col("origin_code", "string", ""), col("carrier_id", "string", ""),
			col("weight", "decimal", "")),
		table("r_stations", "c1", "stations", 20, []string{"station_code"},
			col("station_code", "string", ""), col("station_name", "string", "")),
		table("r_routes", "c1", "routes", 30, []string{"route_id"},
			col("route_id", "string", ""), col("from_station", "string", "")),
		table("r_deliveries", "c1", "deliveries", 80, []string{"delivery_id"},
			col("delivery_id", "string", ""), col("waybill", "string", ""), col("route_id", "string", "")),
		table("r_carriers", "c2", "carriers", 5, []string{"carrier_id"}, col("carrier_id", "string", "")),
	}
}

func logisticsObjectType(id string, table string, key string, fields ...string) *interfaces.DraftObjectType {
	ot := &interfaces.DraftObjectType{ID: id, Name: strings.ToUpper(id[:1]) + id[1:], Adopted: true, PrimaryKeys: []string{key},
		Table: interfaces.DraftTable{ResourceID: table, Name: id, CatalogID: "c1"}}
	for _, f := range fields {
		ot.Properties = append(ot.Properties, &interfaces.DraftProperty{Name: f, DisplayName: f, MappedField: f, Type: "string"})
	}
	return ot
}

func logisticsDraft() *interfaces.Draft {
	carriers := logisticsObjectType("carriers", "r_carriers", "carrier_id", "carrier_id")
	carriers.Table.CatalogID = "c2"
	return &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime, Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			logisticsObjectType("shipments", "r_shipments", "waybill", "waybill", "origin_code", "carrier_id"),
			logisticsObjectType("stations", "r_stations", "station_code", "station_code", "station_name"),
			logisticsObjectType("routes", "r_routes", "route_id", "route_id", "from_station"),
			logisticsObjectType("deliveries", "r_deliveries", "delivery_id", "delivery_id", "waybill", "route_id"),
			carriers,
			{ID: "depots", Name: "Depots"},
		},
		RelationTypes: []*interfaces.DraftRelationType{
			{ID: "routes_to_stations", Source: "routes", Target: "stations", Adopted: true,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "from_station", TargetProperty: "station_code"}}},
			{ID: "deliveries_to_routes", Source: "deliveries", Target: "routes", Adopted: true,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "route_id", TargetProperty: "route_id"}}},
			// Drafted below the adopt rate and left for a person.
			{ID: "shipments_to_stations", Name: "Origin station", Source: "shipments", Target: "stations",
				ProposedBecause: proposedBelowAdoptRate,
				Mapping:         []interfaces.DraftFieldMapping{{SourceProperty: "origin_code", TargetProperty: "station_code"}},
				Evidence:        interfaces.DraftJoinEvidence{Computed: true, Rows: 100, Hits: 70, HitRate: 0.7}},
		},
	}}
}

// joinCounts answers a join count by its source table: rows, nulls and orphans.
func joinCounts(counts map[string][3]int64) func(context.Context, interfaces.VegaAccount, string, int) ([]map[string]any, error) {
	return func(_ context.Context, _ interfaces.VegaAccount, sql string, _ int) ([]map[string]any, error) {
		for source, c := range counts {
			if strings.Contains(sql, "FROM {{"+source+"}} s") {
				return []map[string]any{{"total": c[0], "nulls": c[1], "orphans": c[2], "distinct_values": c[0]}}, nil
			}
		}
		return nil, fmt.Errorf("unexpected query %s", sql)
	}
}

func Test_RelationSuggestions(t *testing.T) {
	Convey("Test suggesting relations for unconnected object types\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// Suggestions only read: any write of the draft fails the test, as no such call is expected.
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, ps: ps, va: va}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		readDraft := func(d *interfaces.Draft) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
		}

		Convey("An isolated object type is an adopted one no adopted relation has as an end\n", func() {
			readDraft(logisticsDraft())

			got, err := svc.IsolatedObjectTypes(ctx, token, "p1")

			So(err, ShouldBeNil)
			// stations, routes and deliveries are connected; the drafted shipments relation is not
			// adopted; depots are not adopted.
			So(got.Isolated, ShouldResemble, []interfaces.ReleaseItem{
				{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "shipments", Name: "Shipments"},
				{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "carriers", Name: "Carriers"},
			})
		})

		Convey("An object type's candidates: the drafted one first, then both directions, counted within a catalog\n", func() {
			d := logisticsDraft()
			// To an object type no one adopted: not a relation a person can make.
			d.Items.RelationTypes = append(d.Items.RelationTypes, &interfaces.DraftRelationType{ID: "shipments_to_depots",
				Source: "shipments", Target: "depots", Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "origin_code", TargetProperty: "id"}}})
			readDraft(d)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(logisticsTables(), nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				DoAndReturn(joinCounts(map[string][3]int64{"r_deliveries": {80, 0, 2}})).Times(1)

			got, err := svc.RelationSuggestions(ctx, token, "p1", "shipments", caller)

			So(err, ShouldBeNil)
			So(got.ObjectType.ID, ShouldEqual, "shipments")
			So(len(got.Candidates), ShouldEqual, 3)
			drafted, incoming, carrier := got.Candidates[0], got.Candidates[1], got.Candidates[2]
			// Drafted and not adopted: adopted as it is.
			So(drafted.Origin, ShouldEqual, interfaces.DRAFT_RELATION_CANDIDATE_DRAFTED)
			So(drafted.RelationTypeID, ShouldEqual, "shipments_to_stations")
			So(drafted.Evidence.HitRate, ShouldEqual, 0.7)
			// A delivery names its shipment by the waybill: counted over the full table.
			So(incoming, ShouldResemble, interfaces.DraftRelationCandidate{
				SourceObjectTypeID: "deliveries", TargetObjectTypeID: "shipments", Type: interfaces.DRAFT_RELATION_DIRECT,
				MappingRules: []interfaces.DraftRelationMapping{{SourceProperty: interfaces.DraftMappedField{Name: "waybill"},
					TargetProperty: interfaces.DraftMappedField{Name: "waybill"}}},
				Name: incoming.Name, Origin: interfaces.DRAFT_ORIGIN_NAME_MATCH,
				Evidence: interfaces.DraftJoinEvidence{Computed: true, Rows: 80, Hits: 78, Orphans: 2, HitRate: 78.0 / 80, SourceDistinct: 80},
			})
			So(incoming.Name, ShouldNotBeBlank)
			// A carrier in another catalog: listed, not counted.
			So(carrier.TargetObjectTypeID, ShouldEqual, "carriers")
			So(carrier.Evidence.Computed, ShouldBeFalse)
			So(carrier.Evidence.Reason, ShouldEqual, reasonCrossCatalog)
		})

		Convey("A relation the draft has is listed once, as drafted; an identity field refers to nothing\n", func() {
			d := logisticsDraft()
			d.Items.RelationTypes = append(d.Items.RelationTypes, &interfaces.DraftRelationType{ID: "deliveries_to_shipments",
				Source: "deliveries", Target: "shipments", ProposedBecause: proposedFewValues,
				Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "waybill", TargetProperty: "waybill"}}})
			// Labels extend shipments one to one: their identity is the waybill too.
			d.Items.ObjectTypes = append(d.Items.ObjectTypes, logisticsObjectType("labels", "r_labels", "waybill", "waybill"))
			tables := append(logisticsTables(), table("r_labels", "c1", "labels", 100, []string{"waybill"}, col("waybill", "string", "")))
			readDraft(d)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(tables, nil)

			got, err := svc.RelationSuggestions(ctx, token, "p1", "shipments", caller)

			So(err, ShouldBeNil)
			pairs := []string{}
			for _, c := range got.Candidates {
				pairs = append(pairs, c.Origin+":"+c.SourceObjectTypeID+">"+c.TargetObjectTypeID)
			}
			So(pairs, ShouldResemble, []string{"drafted:shipments>stations", "drafted:deliveries>shipments",
				"name_match:shipments>carriers"})
		})

		Convey("An inference the counts refute is listed too, after the ones they bear out\n", func() {
			// shipments name their origin station by its own key name, which the data mostly lacks.
			d := logisticsDraft()
			d.Items.RelationTypes = d.Items.RelationTypes[:2]
			d.Items.ObjectTypes[0].Properties[1] = &interfaces.DraftProperty{Name: "station_code", MappedField: "station_code", Type: "string"}
			tables := logisticsTables()
			tables[0].Columns[1] = col("station_code", "string", "")
			// Carriers listed before stations: counted or not, not the order read, sets theirs.
			tables[1], tables[4] = tables[4], tables[1]
			readDraft(d)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(tables, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).
				DoAndReturn(joinCounts(map[string][3]int64{"r_deliveries": {80, 0, 2}, "r_shipments": {100, 0, 100}})).AnyTimes()

			got, err := svc.RelationSuggestions(ctx, token, "p1", "shipments", caller)

			So(err, ShouldBeNil)
			origins := []string{}
			for _, c := range got.Candidates {
				origins = append(origins, c.SourceObjectTypeID+">"+c.TargetObjectTypeID)
			}
			// shipments.station_code to stations: hits none, which drafting drops; still before the uncounted.
			So(origins, ShouldResemble, []string{"deliveries>shipments", "shipments>stations", "shipments>carriers"})
			So(got.Candidates[1].Evidence.Computed, ShouldBeTrue)
			So(got.Candidates[1].Evidence.HitRate, ShouldEqual, 0)
		})

		Convey("At most suggestionsMaxCounts candidates are counted, the rest listed uncounted\n", func() {
			d := logisticsDraft()
			tables := logisticsTables()
			for i := range suggestionsMaxCounts + 1 {
				id := fmt.Sprintf("scan%d", i)
				tables = append(tables, table("r_"+id, "c1", id, 10, []string{"scan_id"},
					col("scan_id", "string", ""), col("waybill", "string", "")))
				d.Items.ObjectTypes = append(d.Items.ObjectTypes, logisticsObjectType(id, "r_"+id, "scan_id", "scan_id", "waybill"))
			}
			readDraft(d)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(tables, nil)
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 30).Return([]map[string]any{
				{"total": int64(10), "nulls": int64(0), "orphans": int64(0), "distinct_values": int64(10)}}, nil).Times(suggestionsMaxCounts)

			got, err := svc.RelationSuggestions(ctx, token, "p1", "shipments", caller)

			So(err, ShouldBeNil)
			uncounted := 0
			for _, c := range got.Candidates {
				if c.Evidence.Reason == reasonNotCounted {
					uncounted++
				}
			}
			So(uncounted, ShouldEqual, 2)
		})

		Convey("A candidate, as given, is a body the relation save takes\n", func() {
			readDraft(logisticsDraft())
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(logisticsTables(), nil).AnyTimes()
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				DoAndReturn(joinCounts(map[string][3]int64{"r_deliveries": {80, 0, 2}})).AnyTimes()
			got, err := svc.RelationSuggestions(ctx, token, "p1", "shipments", caller)
			So(err, ShouldBeNil)
			candidate := got.Candidates[1]
			rules, _ := json.Marshal(candidate.MappingRules)
			readDraft(logisticsDraft())
			var written *interfaces.Draft
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).DoAndReturn(
				func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})

			_, err = svc.CreateRelationType(ctx, token, "p1", interfaces.DraftRelationTypeSave{UpdateTime: readTime,
				Name: candidate.Name, SourceObjectTypeID: candidate.SourceObjectTypeID,
				TargetObjectTypeID: candidate.TargetObjectTypeID, Type: candidate.Type, MappingRules: rules}, caller)

			So(err, ShouldBeNil)
			made := written.Items.RelationTypes[len(written.Items.RelationTypes)-1]
			So(made.Source+">"+made.Target, ShouldEqual, "deliveries>shipments")
			So(made.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "waybill", TargetProperty: "waybill"}})
		})

		Convey("Only an adopted object type of a ready draft has suggestions\n", func() {
			readDraft(logisticsDraft())
			_, err := svc.RelationSuggestions(ctx, token, "p1", "depots", caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidItem)

			readDraft(logisticsDraft())
			_, err = svc.RelationSuggestions(ctx, token, "p1", "ghost", caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)

			drafting := logisticsDraft()
			drafting.Status = interfaces.DRAFT_STATUS_DRAFTING
			readDraft(drafting)
			_, err = svc.IsolatedObjectTypes(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_NotReady)
		})
	})
}
