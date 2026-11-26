// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsTools "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kntools"
)

func TestExecuteToolPreservesExactDynamicNumbers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, contentType := range []string{"application/json", "text/plain"} {
		t.Run(contentType, func(t *testing.T) {
			service := &executeToolServiceStub{execute: func(ctx context.Context, req *logicsTools.ExecuteToolReq) (map[string]any, error) {
				auth, ok := common.GetAccountAuthContextFromCtx(ctx)
				if !ok || auth.AccountID != "caller" {
					t.Fatal("requester authentication context was lost")
				}
				if req.KnID != "kn" || req.ToolboxID != "box" || req.ToolID != "tool" {
					t.Fatalf("execution target changed: %+v", req)
				}
				if req.Arguments["identity"] != json.Number("9007199254740993") || req.Arguments["amount"] != json.Number("-9007199254740993") {
					t.Fatalf("dynamic numbers were rounded: %#v", req.Arguments)
				}
				nested, ok := req.Arguments["nested"].(map[string]any)
				if !ok || nested["identity"] != json.Number("9007199254740993") {
					t.Fatalf("nested dynamic identity changed: %#v", req.Arguments["nested"])
				}
				return map[string]any{"status_code": 200, "body": req.Arguments}, nil
			}}
			handler := &knToolsHandler{tools: service}
			router := gin.New()
			router.POST("/kn/execute_tool", handler.ExecuteTool)
			request := httptest.NewRequest(http.MethodPost, "/kn/execute_tool", strings.NewReader(`{"kn_id":"kn","toolbox_id":"box","tool_id":"tool","arguments":{"identity":9007199254740993,"amount":-9007199254740993,"nested":{"identity":9007199254740993}}}`))
			request.Header.Set("Content-Type", contentType)
			request = request.WithContext(common.SetAccountAuthContextToCtx(request.Context(), &interfaces.AccountAuthContext{AccountID: "caller"}))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || service.calls != 1 || !strings.Contains(response.Body.String(), `"identity":9007199254740993`) {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body)
			}
		})
	}
}

func TestExecuteToolRejectsInvalidJSONBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &executeToolServiceStub{}
	handler := &knToolsHandler{tools: service}
	router := gin.New()
	router.POST("/kn/execute_tool", handler.ExecuteTool)
	request := httptest.NewRequest(http.MethodPost, "/kn/execute_tool", strings.NewReader(`{"arguments":`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("invalid JSON reached execution: status=%d calls=%d", response.Code, service.calls)
	}
}

type executeToolServiceStub struct {
	execute func(context.Context, *logicsTools.ExecuteToolReq) (map[string]any, error)
	calls   int
}

func (*executeToolServiceStub) SearchCapabilities(context.Context, *logicsTools.SearchCapabilitiesReq) (*logicsTools.SearchCapabilitiesResp, error) {
	return nil, nil
}

func (service *executeToolServiceStub) ExecuteTool(ctx context.Context, req *logicsTools.ExecuteToolReq) (map[string]any, error) {
	service.calls++
	return service.execute(ctx, req)
}
