// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestFunctionRuntimeProvesTheExactCodePassedToExecution(t *testing.T) {
	const version = "11111111-1111-4111-8111-111111111111"
	const code = "def handler(event): return {'shared_count': 0}"
	ctrl := gomock.NewController(t)
	service, metadata := mocks.NewMockIMetadataService(ctrl), mocks.NewMockIMetadataDB(ctrl)
	service.EXPECT().CheckMetadataExists(gomock.Any(), interfaces.MetadataTypeFunc, version).Return(true, metadata, nil)
	metadata.EXPECT().GetScriptType().Return(string(interfaces.ScriptTypePython))
	metadata.EXPECT().GetCode().Return(code).Times(1)
	metadata.EXPECT().GetDependencies().Return("")
	metadata.EXPECT().GetDependenciesURL().Return("")
	pool := functionTimeoutSessionPool{execute: func(req *interfaces.ExecuteCodeReq) *interfaces.ExecuteCodeResp {
		if req.Code != code {
			t.Fatalf("unexpected executed code: %q", req.Code)
		}
		// Neither body nor return value may override the runtime's response headers.
		return &interfaces.ExecuteCodeResp{Status: "completed", ReturnValue: map[string]any{
			"function_execution": map[string]any{"function_version": "forged", "code_sha256": "forged"},
		}}
	}}
	handler := &unifiedProxyHandler{Logger: logger.DefaultLogger(), MetadataService: service, SessionPool: pool}
	engine := gin.New()
	engine.POST("/function/exec/:version", handler.FunctionExecuteProxy)
	request := httptest.NewRequest(http.MethodPost, "/function/exec/"+version, strings.NewReader(`{"code":"forged"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(interfaces.FunctionCodeHashHeader, "forged")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get(interfaces.FunctionVersionHeader) != version ||
		response.Header().Get(interfaces.FunctionCodeHashHeader) != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(code))) {
		t.Fatalf("runtime response: %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
}
