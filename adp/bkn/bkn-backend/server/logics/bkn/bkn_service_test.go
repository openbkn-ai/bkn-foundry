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
	mockCtrl := gomock.NewController(t)
	kns := bmock.NewMockKNService(mockCtrl)
	svc := &bknService{
		appSetting: &common.AppSetting{},
		kns:        kns,
	}
	return svc, mockCtrl, kns
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

func Test_bknService_DiffNetworks(t *testing.T) {
	Convey("Test bknService DiffNetworks\n", t, func() {
		svc, mockCtrl, kns := newTestBKNService(t)
		defer mockCtrl.Finish()

		baseKN := &interfaces.KN{
			KNID: "kn1", KNName: "供应链主网", Branch: "main",
			ObjectTypes: []*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "bom", OTName: "产品BOM"},
			}},
		}
		targetKN := &interfaces.KN{
			KNID: "kn2", KNName: "供应链主网（试点）", Branch: "main",
			ObjectTypes: []*interfaces.ObjectType{{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "bom", OTName: "产品物料清单"},
			}},
		}

		Convey("Both sides are loaded and the definition difference is reported\n", func() {
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(baseKN, nil)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", "main", interfaces.Mode_Export).Return(targetKN, nil)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:   interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target: interfaces.KNRef{KNID: "kn2", Branch: "main"},
			})
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.Base.Name, ShouldEqual, "供应链主网")
			So(result.Target.Name, ShouldEqual, "供应链主网（试点）")
			So(result.Summary.Updated, ShouldEqual, 1)
			So(len(result.Entries), ShouldEqual, 1)
			So(result.Entries[0].ID, ShouldEqual, "bom")
			// The two networks carry different ids and names; that difference is reported on its
			// own rather than counted with the definitions.
			So(result.Network, ShouldNotBeNil)
			So(result.Network.Action, ShouldEqual, bknsdk.DiffUpdate)
			So(result.Lineage.CommonIDs, ShouldEqual, 1)
		})

		Convey("A caller who cannot read the base network is refused before the target is touched\n", func() {
			denied := rest.NewHTTPError(context.Background(), http.StatusForbidden, berrors.BknBackend_KnowledgeNetwork_InvalidParameter)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(nil, denied)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:   interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target: interfaces.KNRef{KNID: "kn2", Branch: "main"},
			})
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("Name fallback is passed through to the differ\n", func() {
			renamedID := &interfaces.KN{
				KNID: "kn2", KNName: "供应链主网（试点）", Branch: "main",
				ObjectTypes: []*interfaces.ObjectType{{
					ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "0193-uuid", OTName: "产品BOM"},
				}},
			}
			kns.EXPECT().GetKNByID(gomock.Any(), "kn1", "main", interfaces.Mode_Export).Return(baseKN, nil)
			kns.EXPECT().GetKNByID(gomock.Any(), "kn2", "main", interfaces.Mode_Export).Return(renamedID, nil)

			result, err := svc.DiffNetworks(context.Background(), interfaces.KNDiffRequest{
				Base:           interfaces.KNRef{KNID: "kn1", Branch: "main"},
				Target:         interfaces.KNRef{KNID: "kn2", Branch: "main"},
				FallbackByName: true,
			})
			So(err, ShouldBeNil)
			So(len(result.Entries), ShouldEqual, 1)
			So(result.Entries[0].PairedBy, ShouldEqual, "name")
			So(result.Entries[0].BaseID, ShouldEqual, "bom")
			So(result.Entries[0].TargetID, ShouldEqual, "0193-uuid")
		})
	})
}
