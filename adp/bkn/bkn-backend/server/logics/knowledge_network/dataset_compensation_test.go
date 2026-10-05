// Copyright openbkn.ai

package knowledge_network

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestCompensateDatasetDocumentsRestoresOnlyTouchedRows(t *testing.T) {
	ctrl := gomock.NewController(t)
	objects := mock_interfaces.NewMockObjectTypeService(ctrl)
	vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
	service := &knowledgeNetworkService{appSetting: &common.AppSetting{}, ots: objects, vbs: vbs}
	snapshot := &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "old"}},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "unrelated"}},
		},
	}
	oldID := interfaces.GenerateConceptDocuemtnID(snapshot.KNID, interfaces.MODULE_TYPE_OBJECT_TYPE,
		"old", snapshot.Branch)
	newID := interfaces.GenerateConceptDocuemtnID(snapshot.KNID, interfaces.MODULE_TYPE_OBJECT_TYPE,
		"new", snapshot.Branch)
	objects.EXPECT().InsertDatasetData(gomock.Any(), []*interfaces.ObjectType{snapshot.ObjectTypes[0]}).Return(nil)
	vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), interfaces.BKN_DATASET_ID, newID).Return(nil)

	if err := service.compensateDatasetDocuments(context.Background(), []string{oldID, newID, oldID}, snapshot); err != nil {
		t.Fatalf("compensateDatasetDocuments() error = %v", err)
	}
}

func TestCompensateDatasetDocumentsForNewImportOnlyDeletesAttemptedIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
	service := &knowledgeNetworkService{vbs: vbs}
	vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), interfaces.BKN_DATASET_ID, "doc-1").Return(nil)
	vbs.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), interfaces.BKN_DATASET_ID, "doc-2").Return(nil)
	if err := service.compensateDatasetDocuments(context.Background(), []string{"doc-2", "doc-1"}, nil); err != nil {
		t.Fatalf("compensateDatasetDocuments() error = %v", err)
	}
}
