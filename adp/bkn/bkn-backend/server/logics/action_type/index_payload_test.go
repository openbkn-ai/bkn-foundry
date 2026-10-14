// Copyright openbkn.ai

package action_type

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func TestActionTypeIndexPayloadKeepsExistingStringFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
	service := &actionTypeService{appSetting: &common.AppSetting{}, vbs: vbs}
	action := &interfaces.ActionType{
		ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", ATName: "action",
			Parameters: []interfaces.Parameter{{Name: "input", Type: "string"}},
			Condition:  &interfaces.ActionCondCfg{Field: "state", Operation: "=="},
		},
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	}
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, document map[string]any) error {
			if _, ok := document["parameters"].(string); !ok {
				t.Fatalf("parameters = %#v, want encoded string", document["parameters"])
			}
			if _, ok := document["condition"].(string); !ok {
				t.Fatalf("condition = %#v, want encoded string", document["condition"])
			}
			if document["_id"] == "" {
				t.Fatal("missing document ID")
			}
			return nil
		})
	if err := service.InsertDatasetData(context.Background(), []*interfaces.ActionType{action}); err != nil {
		t.Fatal(err)
	}
}
