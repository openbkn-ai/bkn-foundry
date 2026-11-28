// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// A versioned publish (draft) and a version switch (v<k>) replace main with another branch. The
// capability bindings mounted there come along with the copy, so the proxy grants planned for
// the new main must include them: a mounted knowledge base gets query_data and a mounted function
// execute (#3444).
func TestPublishKNMainGrantsCapabilitiesMountedOnTheSourceBranch(t *testing.T) {
	for _, srcBranch := range []string{interfaces.DRAFT_BRANCH, interfaces.VersionBranch(2)} {
		t.Run(srcBranch, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			kna := bmock.NewMockKNAccess(ctrl)
			cga := bmock.NewMockConceptGroupAccess(ctrl)
			ota := bmock.NewMockObjectTypeAccess(ctrl)
			rta := bmock.NewMockRelationTypeAccess(ctrl)
			ata := bmock.NewMockActionTypeAccess(ctrl)
			cba := bmock.NewMockCapabilityBindingAccess(ctrl)
			aoa := bmock.NewMockAgentOperatorAccess(ctrl)
			cgs := bmock.NewMockConceptGroupService(ctrl)
			ots := bmock.NewMockObjectTypeService(ctrl)
			rts := bmock.NewMockRelationTypeService(ctrl)
			ats := bmock.NewMockActionTypeService(ctrl)
			riskTypeS := bmock.NewMockRiskTypeService(ctrl)
			vbs := bmock.NewMockVegaBackendService(ctrl)
			db, databaseMock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })

			for _, branch := range []string{interfaces.MAIN_BRANCH, srcBranch} {
				kna.EXPECT().GetKNByID(gomock.Any(), "kn-1", branch).
					Return(&interfaces.KN{KNID: "kn-1", KNName: "network", Branch: branch}, nil)
			}
			cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
			ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).Return(nil, nil).Times(2)
			rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
			ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)

			// The rows as the store returns them: on the branch they were listed from.
			listed := []*interfaces.CapabilityBinding{
				{ID: "binding-kb", KNID: "kn-1", Branch: srcBranch,
					CapabilityType: interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, CapabilityID: "7"},
				{ID: "binding-fn", KNID: "kn-1", Branch: srcBranch,
					CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-fn", CapabilityID: "tool-fn"},
			}
			cba.EXPECT().ListBindings(gomock.Any(), interfaces.CapabilityBindingsQueryParams{KNID: "kn-1", Branch: srcBranch}).
				Return(listed, nil)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-fn").Return([]*interfaces.ToolBrief{{
				ToolID: "tool-fn", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION,
			}}, nil)

			// The concept index of main is rewritten after commit; failures there are only logged.
			cgs.EXPECT().InsertDatasetDatas(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			ots.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			rts.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			ats.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			riskTypeS.EXPECT().InsertDatasetData(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			vbs.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			databaseMock.ExpectBegin()
			databaseMock.ExpectCommit()

			kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
				KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
				SyncStatus: interfaces.KNProxySyncReady,
			}}
			mpa := &managedProxyAccessStub{allowed: true}
			outbox := &proxyOutboxAccessStub{}
			service := &knowledgeNetworkService{
				appSetting: &common.AppSetting{}, db: db,
				kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, cba: cba, aoa: aoa,
				cgs: cgs, ots: ots, rts: rts, ats: ats, riskTypeS: riskTypeS, vbs: vbs,
				kpa: kpa, mpa: mpa, kpoa: outbox,
			}
			ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "publisher-1"})
			mutated := false
			err = service.PublishKNMain(ctx, "kn-1", srcBranch, func(context.Context, *sql.Tx) error {
				mutated = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !mutated || len(outbox.events) != 1 {
				t.Fatalf("mutated = %t, staged events = %d", mutated, len(outbox.events))
			}

			want := map[string]bool{
				"binding-kb\x00" + interfaces.KNProxyTargetTypeKnowledgeBase + "\x007\x00" + interfaces.OPERATION_TYPE_QUERY_DATA: false,
				"binding-fn\x00function\x00box-fn\x00" + interfaces.OPERATION_TYPE_EXECUTE:                                        false,
			}
			for _, source := range outbox.events[0].DesiredSources {
				key := source.BindingID + "\x00" + source.ResourceType + "\x00" + source.ResourceID + "\x00" + source.Operation
				if _, ok := want[key]; ok {
					want[key] = true
				}
			}
			for key, found := range want {
				if !found {
					t.Errorf("desired proxy grant sources %#v lack %q", outbox.events[0].DesiredSources, key)
				}
			}
			for _, binding := range listed {
				if binding.Branch != srcBranch {
					t.Errorf("listed binding %s moved to branch %q", binding.ID, binding.Branch)
				}
			}
			if err := databaseMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
