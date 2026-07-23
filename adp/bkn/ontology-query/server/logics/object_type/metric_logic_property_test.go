// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"testing"

	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func Test_filtersToCondition(t *testing.T) {
	Convey("filtersToCondition", t, func() {
		Convey("empty filters", func() {
			So(filtersToCondition(nil), ShouldBeNil)
		})
		Convey("single filter", func() {
			c := filtersToCondition([]interfaces.Filter{{Name: "student_id", Operation: "==", Value: "s1"}})
			So(c, ShouldNotBeNil)
			So(c.Name, ShouldEqual, "student_id")
			So(c.Operation, ShouldEqual, cond.OperationEq)
		})
		Convey("multiple filters become AND", func() {
			c := filtersToCondition([]interfaces.Filter{
				{Name: "a", Operation: "==", Value: 1},
				{Name: "b", Operation: "=", Value: 2},
			})
			So(c.Operation, ShouldEqual, cond.OperationAnd)
			So(len(c.SubConds), ShouldEqual, 2)
		})
	})
}

func Test_queryLogicMetricViaKN(t *testing.T) {
	Convey("queryLogicMetricViaKN", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
		mqs := omock.NewMockMetricQueryService(mockCtrl)
		service := &objectTypeService{
			omAccess: omAccess,
			mqs:      mqs,
		}

		logicProp := &interfaces.LogicProperty{
			Name: "risk_score",
			DataSource: &interfaces.ResourceInfo{
				ID: "metric1",
			},
		}

		Convey("allows data_view-backed object type", func() {
			omAccess.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_DATA_VIEW,
						ID:   "view1",
					},
				},
			}, true, nil)
			omAccess.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "metric1").Return(&interfaces.MetricDefinition{
				ID:       "metric1",
				ScopeRef: "ot1",
			}, true, nil)
			mqs.EXPECT().QueryMetricData(gomock.Any(), "kn1", "main", "metric1", gomock.Any()).Return(interfaces.MetricData{
				Datas: []interfaces.Data{{Values: []interface{}{1}}},
			}, nil)

			result, err := service.queryLogicMetricViaKN(
				ctx, "kn1", "main", "ot1", logicProp, nil,
				interfaces.MetricPropertyDynamicParams{}, 0, 0, true, "",
			)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("rejects metric when scope_ref mismatches object type id", func() {
			omAccess.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1"},
			}, true, nil)
			omAccess.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "metric1").Return(&interfaces.MetricDefinition{
				ID:       "metric1",
				ScopeRef: "ot_other",
			}, true, nil)

			_, err := service.queryLogicMetricViaKN(
				ctx, "kn1", "main", "ot1", logicProp, nil,
				interfaces.MetricPropertyDynamicParams{}, 0, 0, true, "",
			)
			So(err, ShouldNotBeNil)
		})
	})
}
