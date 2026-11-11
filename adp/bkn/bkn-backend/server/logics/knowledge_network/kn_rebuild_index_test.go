// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func Test_knowledgeNetworkService_RebuildBranchIndex(t *testing.T) {
	Convey("RebuildBranchIndex rewrites a branch's concept index from its rows", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		kna := bmock.NewMockKNAccess(mockCtrl)
		cga := bmock.NewMockConceptGroupAccess(mockCtrl)
		ota := bmock.NewMockObjectTypeAccess(mockCtrl)
		rta := bmock.NewMockRelationTypeAccess(mockCtrl)
		ata := bmock.NewMockActionTypeAccess(mockCtrl)
		cgs := bmock.NewMockConceptGroupService(mockCtrl)
		ots := bmock.NewMockObjectTypeService(mockCtrl)
		rts := bmock.NewMockRelationTypeService(mockCtrl)
		ats := bmock.NewMockActionTypeService(mockCtrl)
		riskTypeS := bmock.NewMockRiskTypeService(mockCtrl)
		vbs := bmock.NewMockVegaBackendService(mockCtrl)
		service := &knowledgeNetworkService{
			appSetting: &common.AppSetting{},
			kna:        kna, cga: cga, ota: ota, rta: rta, ata: ata,
			cgs: cgs, ots: ots, rts: rts, ats: ats, riskTypeS: riskTypeS, vbs: vbs,
		}

		kna.EXPECT().GetKNByID(gomock.Any(), "kn1", "v2").Return(&interfaces.KN{KNID: "kn1", KNName: "kn", Branch: "v2"}, nil)
		cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil)
		ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).Return([]*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot_a"}, KNID: "kn1", Branch: "v2"},
		}, nil)
		rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
		ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil)

		docID := func(moduleType, id string) string {
			return interfaces.GenerateConceptDocuemtnID("kn1", moduleType, id, "v2")
		}
		page := func(cursor *string, entries ...map[string]any) *interfaces.DatasetQueryResponse {
			return &interfaces.DatasetQueryResponse{Entries: entries, Paging: &interfaces.ResourceDataPagingResult{NextCursor: cursor}}
		}

		Convey("every definition and the network are written, then stale documents of the branch deleted", func() {
			cgs.EXPECT().InsertDatasetDatas(gomock.Any(), gomock.Any()).Return(nil)
			ots.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, objectTypes []*interfaces.ObjectType) error {
					So(objectTypes, ShouldHaveLength, 1)
					So(objectTypes[0].OTID, ShouldEqual, "ot_a")
					return nil
				})
			rts.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil)
			ats.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil)
			riskTypeS.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil)
			network := interfaces.GenerateConceptDocuemtnID("kn1", interfaces.MODULE_TYPE_KN, "kn1", "v2")
			vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, network, gomock.Any()).Return(nil)

			next := "c1"
			first := vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.FilterCondition["sub_conditions"], ShouldResemble, []map[string]any{
						{"field": "kn_id", "operation": "==", "value": "kn1", "value_from": "const"},
						{"field": "branch", "operation": "==", "value": "v2", "value_from": "const"},
					})
					return page(&next,
						map[string]any{"id": "ot_a", "module_type": interfaces.MODULE_TYPE_OBJECT_TYPE},
						map[string]any{"id": "ot_gone", "module_type": interfaces.MODULE_TYPE_OBJECT_TYPE},
						map[string]any{"id": "m1", "module_type": "metric"},
					), nil
				})
			vbs.EXPECT().QueryResourceData(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
					So(params.Paging.Cursor, ShouldEqual, "c1")
					return page(nil, map[string]any{"id": "rt_gone", "module_type": interfaces.MODULE_TYPE_RELATION_TYPE}), nil
				}).After(first)
			deleted := []string{}
			vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, id string) error {
					deleted = append(deleted, id)
					return nil
				}).Times(2)

			So(service.RebuildBranchIndex(ctx, "kn1", "v2"), ShouldBeNil)
			So(deleted, ShouldResemble, []string{
				docID(interfaces.MODULE_TYPE_OBJECT_TYPE, "ot_gone"),
				docID(interfaces.MODULE_TYPE_RELATION_TYPE, "rt_gone"),
			})
		})

		Convey("a failed write stops before anything is deleted", func() {
			cgs.EXPECT().InsertDatasetDatas(gomock.Any(), gomock.Any()).Return(nil)
			ots.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(errors.New("model unavailable"))

			So(service.RebuildBranchIndex(ctx, "kn1", "v2"), ShouldNotBeNil)
		})
	})
}
