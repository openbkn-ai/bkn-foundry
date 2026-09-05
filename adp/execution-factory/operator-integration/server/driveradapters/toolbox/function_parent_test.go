package toolbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestExecuteToolCapturesFunctionParentFromRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mocks.NewMockIToolService(ctrl)
	service.EXPECT().ExecuteTool(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *interfaces.ExecuteToolReq) (*interfaces.HTTPResponse, error) {
			if req.BKNParentOperationID != "op_function" {
				t.Fatalf("parent=%q", req.BKNParentOperationID)
			}
			if req.BKNConversationID != "conv_1" || req.BKNInteractionID != "int_1" {
				t.Fatal("lost interaction")
			}
			return &interfaces.HTTPResponse{StatusCode: http.StatusOK}, nil
		},
	)
	handler := &toolBoxHandler{ToolService: service}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"body":{},"BKNParentOperationID":"wrong-parent"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("user_id", "user_1")
	c.Request.Header.Set("Authorization", "Bearer test-token")
	c.Request.Header.Set("bkn-conversation-id", "conv_1")
	c.Request.Header.Set("bkn-interaction-id", "int_1")
	c.Request.Header.Set("bkn-parent-operation-id", "op_function")
	c.Params = gin.Params{{Key: "box_id", Value: "box_1"}, {Key: "tool_id", Value: "tool_1"}}
	handler.ExecuteTool(c)
}
