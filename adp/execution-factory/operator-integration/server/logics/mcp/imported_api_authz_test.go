package mcp

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestImportedMCPToolRejectsFunctionBox(t *testing.T) {
	ctrl := gomock.NewController(t)
	tools := mocks.NewMockIToolService(ctrl)
	tools.EXPECT().GetToolBox(gomock.Any(), gomock.Any(), false).Return(&interfaces.ToolBoxToolInfo{
		BoxID: "box-fn", MetadataType: interfaces.MetadataTypeFunc,
	}, nil)
	svc := &mcpServiceImpl{ToolService: tools}
	if err := svc.validateImportedAPITool(context.Background(), "user-1", "box-fn", "fn-1"); err == nil {
		t.Fatal("function box was accepted as an imported MCP API tool")
	}
}

func TestImportedMCPToolRequiresAPIBoxAuthorize(t *testing.T) {
	ctrl := gomock.NewController(t)
	tools := mocks.NewMockIToolService(ctrl)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	accessor := &interfaces.AuthAccessor{ID: "user-1"}
	tools.EXPECT().GetToolBox(gomock.Any(), gomock.Any(), false).Return(&interfaces.ToolBoxToolInfo{
		BoxID: "box-api", MetadataType: interfaces.MetadataTypeAPI,
	}, nil)
	tools.EXPECT().GetBoxTool(gomock.Any(), gomock.Any()).Return(&interfaces.ToolInfo{MetadataType: interfaces.MetadataTypeAPI}, nil)
	auth.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(accessor, nil)
	auth.EXPECT().CheckAuthorizePermission(gomock.Any(), accessor, "box-api", interfaces.AuthResourceTypeToolBox).
		Return(context.Canceled)
	svc := &mcpServiceImpl{ToolService: tools, AuthService: auth}
	ctx := common.SetPublicAPIToCtx(context.Background(), true)
	if err := svc.validateImportedAPITool(ctx, "user-1", "box-api", "api-1"); err == nil {
		t.Fatal("view-only API box access was enough to import an MCP tool")
	}
}
