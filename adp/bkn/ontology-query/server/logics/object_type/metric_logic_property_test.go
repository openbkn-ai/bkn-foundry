// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func Test_queryLogicMetricViaKN(t *testing.T) {
	Convey("queryLogicMetricViaKN", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
		service := &objectTypeService{
			omAccess: omAccess,
		}

		logicProp := &interfaces.LogicProperty{
			Name: "risk_score",
			DataSource: &interfaces.ResourceInfo{
				ID: "metric1",
			},
		}

		Convey("rejects legacy metric references", func() {
			omAccess.EXPECT().GetObjectType(gomock.Any(), "kn1", "main", "ot1").Return(interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}, true, nil)
			_, err := service.queryLogicMetricViaKN(
				ctx, "kn1", "main", "ot1", logicProp, nil,
				interfaces.MetricPropertyDynamicParams{}, nil, nil,
			)
			So(err, ShouldNotBeNil)
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
			// end differs from now, or this cannot tell a passed-through end
			// from one the implementation replaced with now.
			s, e := int64(1754006400000), int64(1755000000000)
			start, end := logicMetricTimeWindow(
				interfaces.MetricPropertyDynamicParams{Start: &s, End: &e}, now)
			So(*start, ShouldEqual, s)
			So(*end, ShouldEqual, e)
		})
	})
}
