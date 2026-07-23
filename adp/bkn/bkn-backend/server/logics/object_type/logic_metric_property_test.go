// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func Test_validateLogicMetricProperty(t *testing.T) {
	Convey("Test validateLogicMetricProperty\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ma := bmock.NewMockMetricAccess(mockCtrl)
		service := &objectTypeService{ma: ma}

		baseOT := &interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "ot1",
				OTName: "Order",
			},
			KNID:   "kn1",
			Branch: interfaces.MAIN_BRANCH,
		}

		Convey("Skips when logic property or metric id is empty\n", func() {
			err := service.validateLogicMetricProperty(ctx, baseOT, nil)
			So(err, ShouldBeNil)

			err = service.validateLogicMetricProperty(ctx, baseOT, &interfaces.LogicProperty{Name: "m1"})
			So(err, ShouldBeNil)

			err = service.validateLogicMetricProperty(ctx, baseOT, &interfaces.LogicProperty{
				Name:       "m1",
				DataSource: &interfaces.ResourceInfo{ID: "   "},
			})
			So(err, ShouldBeNil)
		})

		Convey("Fails when GetMetricByID returns error\n", func() {
			lp := &interfaces.LogicProperty{
				Name: "risk_score",
				DataSource: &interfaces.ResourceInfo{
					ID: "metric1",
				},
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError))

			err := service.validateLogicMetricProperty(ctx, baseOT, lp)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_InvalidParameter)
		})

		Convey("Fails when KN metric does not exist\n", func() {
			lp := &interfaces.LogicProperty{
				Name: "risk_score",
				DataSource: &interfaces.ResourceInfo{
					ID: "metric1",
				},
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(nil, nil)

			err := service.validateLogicMetricProperty(ctx, baseOT, lp)
			So(err, ShouldNotBeNil)
		})

		Convey("Fails when metric scope_ref does not match object type id\n", func() {
			lp := &interfaces.LogicProperty{
				Name: "risk_score",
				DataSource: &interfaces.ResourceInfo{
					ID: "metric1",
				},
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(&interfaces.MetricDefinition{
					ID:       "metric1",
					ScopeRef: "ot_other",
				}, nil)

			err := service.validateLogicMetricProperty(ctx, baseOT, lp)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorDetails, ShouldContainSubstring, "scope_ref")
		})

		Convey("Success when metric scope_ref matches object type id\n", func() {
			lp := &interfaces.LogicProperty{
				Name: "risk_score",
				DataSource: &interfaces.ResourceInfo{
					ID: "metric1",
				},
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(&interfaces.MetricDefinition{
					ID:       "metric1",
					ScopeRef: "ot1",
				}, nil)

			err := service.validateLogicMetricProperty(ctx, baseOT, lp)
			So(err, ShouldBeNil)
		})
	})
}

func Test_enrichLogicMetricProperty(t *testing.T) {
	Convey("Test enrichLogicMetricProperty\n", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ma := bmock.NewMockMetricAccess(mockCtrl)
		service := &objectTypeService{ma: ma}

		Convey("No-op when metric id is empty\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					LogicProperties: []*interfaces.LogicProperty{
						{Name: "risk_score"},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}

			service.enrichLogicMetricProperty(ctx, objectType, objectType.LogicProperties[0], 0)
			So(objectType.LogicProperties[0].DataSource, ShouldBeNil)
		})

		Convey("No-op when metric lookup fails\n", func() {
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "risk_score",
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(nil, nil)

			service.enrichLogicMetricProperty(ctx, objectType, objectType.LogicProperties[0], 0)
			So(objectType.LogicProperties[0].DataSource.Name, ShouldEqual, "")
		})

		Convey("Enriches metric name, analysis dimensions and parameter comments\n", func() {
			regionComment := "Region"
			objectType := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "risk_score",
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
							Parameters: []interfaces.Parameter{
								{Name: "region"},
								{Name: "instant"},
							},
						},
					},
				},
				KNID:   "kn1",
				Branch: interfaces.MAIN_BRANCH,
			}
			ma.EXPECT().GetMetricByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "metric1").
				Return(&interfaces.MetricDefinition{
					ID:       "metric1",
					Name:     "Risk Hit Rate",
					ScopeRef: "ot1",
					AnalysisDimensions: []interfaces.MetricAnalysisDimension{
						{Name: "region", DisplayName: regionComment},
					},
				}, nil)

			service.enrichLogicMetricProperty(ctx, objectType, objectType.LogicProperties[0], 0)

			lp := objectType.LogicProperties[0]
			So(lp.DataSource.Name, ShouldEqual, "Risk Hit Rate")
			So(len(lp.AnalysisDims), ShouldEqual, 1)
			So(lp.AnalysisDims[0].Name, ShouldEqual, "region")
			So(lp.AnalysisDims[0].DisplayName, ShouldEqual, regionComment)
			So(lp.Parameters[0].Comment, ShouldNotBeNil)
			So(*lp.Parameters[0].Comment, ShouldEqual, regionComment)
			So(lp.Parameters[1].Comment, ShouldNotBeNil)
			So(*lp.Parameters[1].Comment, ShouldContainSubstring, "instant")
		})
	})
}
