// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_data_stats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func objectTypeFixture(otID string, resourceID string, primaryKeys []string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:        otID,
			OTName:      "产品BOM",
			DataSource:  &interfaces.ResourceInfo{Type: "resource", ID: resourceID, Name: "erp_material_bom"},
			PrimaryKeys: primaryKeys,
			DataProperties: []*interfaces.DataProperty{
				{Name: "bom_material_code", MappedField: &interfaces.Field{Name: "bom_material_code"}},
				{Name: "bom_version", MappedField: &interfaces.Field{Name: "bom_version"}},
			},
		},
	}
}

func countRow(rowCount, distinct int64, withDistinct bool) *interfaces.RawQueryResponse {
	entry := map[string]any{"row_count": rowCount}
	if withDistinct {
		entry["primary_key_distinct"] = distinct
	}
	return &interfaces.RawQueryResponse{Entries: []map[string]any{entry}}
}

func Test_objectDataStatsService_ObjectDataStats(t *testing.T) {
	Convey("Test ObjectDataStats\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ps := bmock.NewMockPermissionService(mockCtrl)
		ots := bmock.NewMockObjectTypeService(mockCtrl)
		vba := bmock.NewMockVegaBackendAccess(mockCtrl)
		svc := NewObjectDataStatsServiceWith(ps, ots, vba)

		req := interfaces.ObjectDataStatsRequest{
			Base:   interfaces.ObjectTypeRef{KNID: "kn1", Branch: "main", OTID: "bom"},
			Target: interfaces.ObjectTypeRef{KNID: "kn2", Branch: "main", OTID: "bom"},
		}

		Convey("Both sides are counted and the delta is reported\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn1", "main", "bom").
				Return(objectTypeFixture("bom", "res-1", []string{"bom_material_code"}), nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn2", "main", "bom").
				Return(objectTypeFixture("bom", "res-2", []string{"bom_material_code"}), nil)

			vba.EXPECT().RawQuery(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, q *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
					So(q.Query, ShouldContainSubstring, "{{.res-1}}")
					So(q.Query, ShouldContainSubstring, "COUNT(DISTINCT `bom_material_code`)")
					return countRow(1000, 1000, true), nil
				})
			vba.EXPECT().RawQuery(gomock.Any(), gomock.Any()).Return(countRow(1200, 1150, true), nil)

			result, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldBeNil)
			So(result.Base.RowCount, ShouldEqual, 1000)
			So(result.Target.RowCount, ShouldEqual, 1200)
			So(result.Delta.RowCount, ShouldEqual, 200)
			So(*result.Delta.PrimaryKeyDistinct, ShouldEqual, 150)
			So(result.SameResource, ShouldBeFalse)
			// 1200 rows behind 1150 distinct keys is 50 rows the model believes are the same object.
			So(*result.Target.DuplicateKeys, ShouldEqual, 50)
		})

		// Two object types bound to one resource read the same table, so every number matches by
		// construction. Saying so keeps the caller from presenting a zero delta as a finding.
		Convey("A shared resource is reported as such\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn1", "main", "bom").
				Return(objectTypeFixture("bom", "res-1", []string{"bom_material_code"}), nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn2", "main", "bom").
				Return(objectTypeFixture("bom", "res-1", []string{"bom_material_code"}), nil)
			vba.EXPECT().RawQuery(gomock.Any(), gomock.Any()).Times(2).Return(countRow(1000, 1000, true), nil)

			result, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldBeNil)
			So(result.SameResource, ShouldBeTrue)
			So(result.Delta.RowCount, ShouldEqual, 0)
		})

		Convey("A composite key is concatenated rather than passed as several arguments\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), gomock.Any(), gomock.Any(), gomock.Any()).Times(2).
				Return(objectTypeFixture("bom", "res-1", []string{"bom_material_code", "bom_version"}), nil)
			vba.EXPECT().RawQuery(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
				func(_ any, q *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
					So(q.Query, ShouldContainSubstring,
						"COUNT(DISTINCT CONCAT_WS(CHAR(31), `bom_material_code`, `bom_version`))")
					return countRow(10, 10, true), nil
				})

			_, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldBeNil)
		})

		// Without a primary key there is nothing to count distinct values of, and reporting zero
		// would read as "every row is a duplicate".
		Convey("An object type with no primary key reports rows only\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), gomock.Any(), gomock.Any(), gomock.Any()).Times(2).
				Return(objectTypeFixture("bom", "res-1", nil), nil)
			vba.EXPECT().RawQuery(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
				func(_ any, q *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
					So(q.Query, ShouldNotContainSubstring, "DISTINCT")
					return countRow(42, 0, false), nil
				})

			result, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldBeNil)
			So(result.Base.RowCount, ShouldEqual, 42)
			So(result.Base.PrimaryKeyDistinct, ShouldBeNil)
			So(result.Delta.PrimaryKeyDistinct, ShouldBeNil)
		})

		Convey("An object type bound to no resource is refused\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			unbound := objectTypeFixture("bom", "", nil)
			unbound.DataSource = nil
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn1", "main", "bom").Return(unbound, nil)

			_, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldNotBeNil)
		})

		// The declared keys are property names; the statement has to name the columns underneath
		// them. Counting on a key with no mapped column would count the wrong thing silently.
		Convey("A primary key with no mapped column is refused\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn1", "main", "bom").
				Return(objectTypeFixture("bom", "res-1", []string{"not_a_property"}), nil)

			_, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldNotBeNil)
		})

		Convey("A caller without query_data on the object type is refused before any query runs\n", func() {
			// The check names the object type, not the network: a grant on the network reaches it
			// through inheritance, and a grant on this object type alone must be enough.
			ps.EXPECT().CheckPermission(gomock.Any(),
				interfaces.KNChildPermissionResource(interfaces.RESOURCE_TYPE_OBJECT_TYPE, "kn1", "bom"),
				[]string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(errors.New("denied"))

			_, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldNotBeNil)
		})

		Convey("The object type not existing is a not-found, not a count of zero\n", func() {
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Nil(), "kn1", "main", "bom").Return(nil, nil)

			_, err := svc.ObjectDataStats(context.Background(), req)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_asInt64_ReadsEveryDriverShape(t *testing.T) {
	Convey("Counts come back shaped differently per driver\n", t, func() {
		// The vega adapter decodes with UseNumber, so this is the shape that actually arrives in
		// production. Missing it reported an empty table for a resource with thirty rows, and
		// nothing failed while it did so.
		So(asInt64(json.Number("30")), ShouldEqual, 30)
		So(asInt64(json.Number("30.0")), ShouldEqual, 30)
		So(asInt64(json.Number("not a number")), ShouldEqual, 0)
		So(asInt64(int64(7)), ShouldEqual, 7)
		So(asInt64(7), ShouldEqual, 7)
		So(asInt64(float64(7)), ShouldEqual, 7)
		So(asInt64("7"), ShouldEqual, 7)
		So(asInt64([]byte("7")), ShouldEqual, 7)
		So(asInt64(nil), ShouldEqual, 0)
	})
}
