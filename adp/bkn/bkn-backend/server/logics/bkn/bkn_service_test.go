// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func newTestBKNService(t *testing.T) (*bknService, *gomock.Controller, *bmock.MockKNService) {
	t.Helper()
	svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
	// The cases built on this helper are about the model, not the dependency section: nothing is
	// bound. The capability cases below set their own expectations, which is why the default
	// lives here rather than in the shared constructor — gomock takes the first match, so a
	// permissive default there would shadow them.
	cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
		Return(&interfaces.CapabilityBindingsList{}, nil).AnyTimes()
	return svc, mockCtrl, kns
}

// newTestBKNServiceWithCapabilities also hands back the capability binding service. Export reads
// the bindings to write the dependency section, so a test that exports has to say what is bound;
// the default here is "nothing", which is what the existing cases assume.
func newTestBKNServiceWithCapabilities(t *testing.T) (*bknService, *gomock.Controller,
	*bmock.MockKNService, *bmock.MockCapabilityBindingService) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	kns := bmock.NewMockKNService(mockCtrl)
	cbs := bmock.NewMockCapabilityBindingService(mockCtrl)
	svc := &bknService{
		appSetting: &common.AppSetting{},
		kns:        kns,
		cbs:        cbs,
	}
	return svc, mockCtrl, kns, cbs
}

func Test_bknService_ExportToTar(t *testing.T) {
	Convey("Test bknService ExportToTar\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		Convey("Success with empty KN (no sub-types)\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn1",
				KNName: "Test Network",
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH)

			So(err, ShouldBeNil)
			So(len(data), ShouldBeGreaterThan, 0)
		})

		Convey("Success with KN containing all sub-types\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn2",
				KNName: "Full Network",
				ObjectTypes: []*interfaces.ObjectType{
					{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1", OTName: "OT1"}},
				},
				RelationTypes: []*interfaces.RelationType{
					{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt1", RTName: "RT1"}},
				},
				ActionTypes: []*interfaces.ActionType{
					{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at1", ATName: "AT1"}},
				},
				ConceptGroups: []*interfaces.ConceptGroup{
					{CGID: "cg1", CGName: "CG1"},
				},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn2", interfaces.MAIN_BRANCH)

			So(err, ShouldBeNil)
			So(len(data), ShouldBeGreaterThan, 0)
		})

		Convey("Failed when GetKNByID returns error\n", func() {
			getErr := &rest.HTTPError{
				HTTPCode: http.StatusInternalServerError,
				Language: rest.DefaultLanguage,
				BaseError: rest.BaseError{
					ErrorCode: berrors.BknBackend_KnowledgeNetwork_InternalError,
				},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn-err", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(nil, getErr)

			data, err := svc.ExportToTar(context.Background(), "kn-err", interfaces.MAIN_BRANCH)

			So(err, ShouldNotBeNil)
			So(errors.Is(err, getErr), ShouldBeTrue)
			So(data, ShouldBeNil)
		})

		Convey("Export tar preserves metrics readable by LoadNetworkFromTar\n", func() {
			kn := &interfaces.KN{
				KNID:   "kn-metrics",
				KNName: "KN With Metrics",
				Branch: interfaces.MAIN_BRANCH,
				Metrics: []*interfaces.MetricDefinition{
					{
						ID:         "pod_running_count",
						Name:       "Running Pods",
						MetricType: interfaces.MetricTypeAtomic,
						ScopeRef:   "pod",
						CalculationFormula: &interfaces.MetricCalculationFormula{
							Aggregation: interfaces.MetricAggregation{
								Property: "id",
								Aggr:     interfaces.MetricAggrCount,
							},
						},
					},
				},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn-metrics", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(kn, nil)

			data, err := svc.ExportToTar(context.Background(), "kn-metrics", interfaces.MAIN_BRANCH)
			So(err, ShouldBeNil)
			loaded, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(len(loaded.Metrics), ShouldEqual, 1)
			So(loaded.Metrics[0].ID, ShouldEqual, "pod_running_count")
		})
	})
}

// Test_bknService_ExportCapabilities covers the dependency section riding along with the model.
// An export that dropped it would move a network to another environment with its Skills and
// functions silently unbound, and nothing in the file to say they were ever there.
func Test_bknService_ExportCapabilities(t *testing.T) {
	Convey("导出带能力依赖声明", t, func() {
		svc, mockCtrl, kns, cbs := newTestBKNServiceWithCapabilities(t)
		defer mockCtrl.Finish()

		Convey("id 与名字双写", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn1", KNName: "供应链"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, q interfaces.CapabilityBindingsQueryParams) (*interfaces.CapabilityBindingsList, error) {
					// No paging: a page of the bindings would ship a subset of what the network
					// depends on, with nothing to say so.
					So(q.Limit, ShouldEqual, 0)
					return &interfaces.CapabilityBindingsList{Entries: []*interfaces.CapabilityBinding{
						{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1", Name: "交期评估"},
						{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1",
							CapabilityID: "tool-1", Name: "BOM 展开", OwnerName: "供应链计算"},
					}}, nil
				})

			data, err := svc.ExportToTar(context.Background(), "kn1", interfaces.MAIN_BRANCH)
			So(err, ShouldBeNil)

			network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
			So(err, ShouldBeNil)
			So(network.Capabilities, ShouldNotBeNil)
			So(network.Capabilities.Skills[0].ID, ShouldEqual, "skill-1")
			So(network.Capabilities.Skills[0].Name, ShouldEqual, "交期评估")
			fn := network.Capabilities.Functions[0]
			So(fn.BoxID, ShouldEqual, "box-1")
			So(fn.ToolID, ShouldEqual, "tool-1")
			So(fn.BoxName, ShouldEqual, "供应链计算")
			So(fn.ToolName, ShouldEqual, "BOM 展开")
		})

		Convey("没有绑定时不写这一段", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", interfaces.MAIN_BRANCH, interfaces.Mode_Export).
				Return(&interfaces.KN{KNID: "kn2", KNName: "空网络"}, nil)
			cbs.EXPECT().ListCapabilities(gomock.Any(), gomock.Any()).
				Return(&interfaces.CapabilityBindingsList{}, nil)

			data, err := svc.ExportToTar(context.Background(), "kn2", interfaces.MAIN_BRANCH)
			So(err, ShouldBeNil)
			So(string(data), ShouldNotContainSubstring, "capabilities")
		})
	})
}
