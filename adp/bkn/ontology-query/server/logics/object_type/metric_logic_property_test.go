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

		Convey("rejects metric when scope_ref mismatches object type id", func() {
			omAccess.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}, true, nil)
			omAccess.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "metric1").Return(&interfaces.MetricDefinition{
				ID:       "metric1",
				ScopeRef: "ot_other",
			}, true, nil)

			_, err := service.queryLogicMetricViaKN(
				ctx, "kn1", "main", "ot1", logicProp, nil,
				interfaces.MetricPropertyDynamicParams{}, nil, nil, true, "",
			)
			So(err, ShouldNotBeNil)
		})
	})
}

// A caller that asks for no time range must not have one invented for it. The
// window used to default to the last thirty minutes, which told the metric
// layer a time filter had been requested: a metric without a time_dimension
// was then refused with "time range filter requires metric
// time_dimension.property" for a filter nobody asked for, and a metric with
// one answered for half an hour instead of its own default_range_policy.
func Test_buildMetricQueryRequestFromLogicProperty_TimeWindow(t *testing.T) {
	Convey("buildMetricQueryRequestFromLogicProperty", t, func() {
		Convey("no time asked for leaves the window open", func() {
			req := buildMetricQueryRequestFromLogicProperty(
				nil, interfaces.MetricPropertyDynamicParams{}, nil, nil, true, "")
			So(req.Time, ShouldNotBeNil)
			So(req.Time.Start, ShouldBeNil)
			So(req.Time.End, ShouldBeNil)
			So(*req.Time.Instant, ShouldBeTrue)
		})
		Convey("a supplied range is passed through", func() {
			start, end := int64(1754006400000), int64(1756684800000)
			req := buildMetricQueryRequestFromLogicProperty(
				nil, interfaces.MetricPropertyDynamicParams{}, &start, &end, false, "day")
			So(*req.Time.Start, ShouldEqual, start)
			So(*req.Time.End, ShouldEqual, end)
			So(*req.Time.Step, ShouldEqual, "day")
		})
	})
}

// The metric layer uses a request window only when both ends are present and
// otherwise falls back to the metric's default_range_policy, so one end on its
// own must not reach it: the filter would disappear and the number would be
// computed over everything, without an error.
func Test_logicMetricTimeWindow(t *testing.T) {
	Convey("logicMetricTimeWindow", t, func() {
		now := int64(1756684800000)

		Convey("nothing supplied asks for no window", func() {
			start, end := logicMetricTimeWindow(interfaces.MetricPropertyDynamicParams{}, now)
			So(start, ShouldBeNil)
			So(end, ShouldBeNil)
		})
		Convey("start alone runs to now", func() {
			supplied := int64(1754006400000)
			start, end := logicMetricTimeWindow(
				interfaces.MetricPropertyDynamicParams{Start: &supplied}, now)
			So(*start, ShouldEqual, supplied)
			So(*end, ShouldEqual, now)
		})
		Convey("end alone keeps the half-hour lookback", func() {
			supplied := int64(1754006400000)
			start, end := logicMetricTimeWindow(
				interfaces.MetricPropertyDynamicParams{End: &supplied}, now)
			So(*end, ShouldEqual, supplied)
			So(*start, ShouldEqual, supplied-30*60*1000)
		})
		Convey("both supplied pass through", func() {
			s, e := int64(1754006400000), int64(1756684800000)
			start, end := logicMetricTimeWindow(
				interfaces.MetricPropertyDynamicParams{Start: &s, End: &e}, now)
			So(*start, ShouldEqual, s)
			So(*end, ShouldEqual, e)
		})
	})
}
