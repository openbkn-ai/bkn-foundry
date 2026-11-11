// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// An edit that leaves script_type out keeps the function's own type. Filling
// in python would turn every such edit of a Cypher function into a refused
// change of language.
func TestUpdateToolLeavesAnOmittedScriptTypeToTheStoredFunction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   interfaces.ScriptType
	}{
		{"omitted", `{"code":"MATCH (s:supplier) RETURN count(s)"}`, http.StatusOK, ""},
		{"cypher", `{"script_type":"cypher","code":"MATCH (s:supplier) RETURN count(s)"}`, http.StatusOK, interfaces.ScriptTypeCypher},
		{"python", `{"script_type":"python","code":"def handler(event):\n    return 1"}`, http.StatusOK, interfaces.ScriptTypePython},
		{"unknown", `{"script_type":"ruby","code":"x"}`, http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctrl := gomock.NewController(t)
			toolService := mocks.NewMockIToolService(ctrl)
			validator := mocks.NewMockValidator(ctrl)
			validator.EXPECT().ValidatorToolName(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			validator.EXPECT().ValidatorToolDesc(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			var got *interfaces.UpdateToolReq
			toolService.EXPECT().UpdateTool(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *interfaces.UpdateToolReq) (*interfaces.UpdateToolResp, error) {
					got = req
					return &interfaces.UpdateToolResp{BoxID: req.BoxID, ToolID: req.ToolID}, nil
				}).AnyTimes()
			handler := &toolBoxHandler{ToolService: toolService, Validator: validator}
			engine := gin.New()
			engine.POST("/tool-box/:box_id/tool/:tool_id", handler.UpdateTool)
			body := `{"name":"supplier_count","description":"count suppliers","metadata_type":"function","function_input":` + tc.body + `}`
			request := httptest.NewRequest(http.MethodPost, "/tool-box/box-1/tool/tool-1", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("user_id", "user-1")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			if got == nil || got.FunctionInputEdit == nil {
				t.Fatal("the edit did not reach the tool service")
			}
			if got.FunctionInputEdit.ScriptType != tc.want {
				t.Fatalf("script_type = %q, want %q", got.FunctionInputEdit.ScriptType, tc.want)
			}
		})
	}
}
