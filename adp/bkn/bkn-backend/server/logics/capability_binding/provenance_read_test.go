// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability_binding

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// TestCapabilityTotalsReadNarrowModel covers #1920: the knowledge network statistics counted
// capabilities by reading every object type in full, raw import payload and data properties
// included. On a 10,000 object type network that is hundreds of megabytes per request and the
// request timed out at the gateway. Provenance needs only the references to tools, so it must
// read only those.
//
// The mocks carry no expectation for ListObjectTypes or ListActionTypes: going back to the full
// reads fails here as an unexpected call.
func TestCapabilityTotalsReadNarrowModel(t *testing.T) {
	Convey("能力统计只读取工具引用所需的列", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		cba := bmock.NewMockCapabilityBindingAccess(ctrl)
		ota := bmock.NewMockObjectTypeAccess(ctrl)
		ata := bmock.NewMockActionTypeAccess(ctrl)
		aoa := bmock.NewMockAgentOperatorAccess(ctrl)
		service := &capabilityBindingService{appSetting: &common.AppSetting{}, cba: cba, ota: ota, ata: ata, aoa: aoa}

		ota.EXPECT().ListObjectTypeLogicProperties(gomock.Any(), interfaces.ObjectTypesQueryParams{
			KNID: "kn1", Branch: "main",
		}).Return([]*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "ot1",
				OTName: "Object 1",
				LogicProperties: []*interfaces.LogicProperty{{
					Name:       "lp1",
					DataSource: &interfaces.ResourceInfo{BoxID: "box1", ToolID: "tool1"},
				}},
			},
		}}, nil)
		ata.EXPECT().ListActionTypeSummaries(gomock.Any(), gomock.Any()).Return([]*interfaces.ActionType{{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID:         "at1",
				ATName:       "Action 1",
				ActionSource: interfaces.ActionSource{McpID: "mcp1", ToolName: "search"},
			},
		}}, nil)

		cba.EXPECT().GetBindingsTotalByType(gomock.Any(), "kn1", "main").Return(map[string]int{}, nil)
		cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{}, nil)
		cba.EXPECT().GetFunctionTotalsByOwner(gomock.Any(), "kn1", "main").Return(map[string]int{}, nil)
		// The API/function split may look up the box of the unmounted tool; it is a function box.
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box1").Return([]*interfaces.ToolBrief{
			{BoxID: "box1", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION},
		}, nil).AnyTimes()

		totals, err := service.GetCapabilityTotalsByType(context.Background(), "kn1", "main")
		So(err, ShouldBeNil)
		// Neither is mounted; both are counted because the model uses them.
		So(totals[interfaces.CAPABILITY_TYPE_FUNCTION], ShouldEqual, 1)
		So(totals[interfaces.CAPABILITY_TYPE_MCP_TOOL], ShouldEqual, 1)
	})
}
