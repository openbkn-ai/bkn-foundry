// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestPlatformFunctionExecutionRequiresExactRuntimeProof(t *testing.T) {
	const version = "11111111-1111-4111-8111-111111111111"
	digest := "sha256:" + strings.Repeat("a", 64)
	url := interfaces.AOIServerURL + interfaces.SetAOIFuncExecPath(version)
	for _, tc := range []struct {
		name, url, version, digest string
		want                       bool
	}{
		{"matching executed code", url, version, digest, true},
		{"old runtime", url, "", "", false},
		{"external runtime", "https://other.example" + interfaces.SetAOIFuncExecPath(version), version, digest, false},
		{"extra path", url + "/other", version, digest, false},
		{"different executed version", url, "22222222-2222-4222-8222-222222222222", digest, false},
		{"invalid code hash", url, version, "sha256:wrong", false},
		{"unprefixed code hash", url, version, strings.Repeat("a", 64), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := platformFunctionExecution(&interfaces.ExecuteToolReq{BoxID: "box", ToolID: "tool"}, tc.url,
				&interfaces.HTTPResponse{Headers: map[string]any{
					interfaces.FunctionVersionHeader: tc.version, interfaces.FunctionCodeHashHeader: tc.digest,
				}})
			if (proof != nil) != tc.want {
				t.Fatalf("proof=%#v, want present=%v", proof, tc.want)
			}
			if proof != nil && (proof.ToolboxID != "box" || proof.ToolID != "tool" || proof.FunctionVersion != version || proof.CodeSHA256 != digest) {
				t.Fatalf("wrong executed identity: %#v", proof)
			}
		})
	}
	if got := responseHeader(map[string]any{"X-BKN-Function-Version": "", "x-bkn-function-version": version}, interfaces.FunctionVersionHeader); got != "" {
		t.Fatalf("ambiguous header accepted: %q", got)
	}
}

func TestPublishedToolDoesNotTrustBodyOrThirdPartyFunctionProof(t *testing.T) {
	const version = "11111111-1111-4111-8111-111111111111"
	digest := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name       string
		source     model.SourceType
		serverURL  string
		runtimeNew bool
		wantProof  bool
	}{
		{"platform function", model.SourceTypeFunction, interfaces.AOIServerURL, true, true},
		{"old platform runtime", model.SourceTypeFunction, interfaces.AOIServerURL, false, false},
		{"imported remote function", model.SourceTypeFunction, "https://other.example", true, false},
		{"OpenAPI with forged proof", model.SourceTypeOpenAPI, interfaces.AOIServerURL, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			boxes, tools := mocks.NewMockIToolboxDB(ctrl), mocks.NewMockIToolDB(ctrl)
			metadata, item := mocks.NewMockIMetadataService(ctrl), mocks.NewMockIMetadataDB(ctrl)
			proxy := mocks.NewMockProxyHandler(ctrl)
			boxes.EXPECT().SelectToolBox(gomock.Any(), "box").Return(true, &model.ToolboxDB{BoxID: "box", Status: "published"}, nil)
			tools.EXPECT().SelectTool(gomock.Any(), "tool").Return(true, &model.ToolDB{BoxID: "box", ToolID: "tool", SourceType: tc.source, SourceID: version, Status: "enabled"}, nil)
			metadata.EXPECT().GetMetadataBySource(gomock.Any(), version, tc.source).Return(true, item, nil)
			item.EXPECT().GetServerURL().Return(tc.serverURL)
			item.EXPECT().GetPath().Return(interfaces.SetAOIFuncExecPath(version))
			item.EXPECT().GetMethod().Return(http.MethodPost)
			body := map[string]any{"result": map[string]any{"function_execution": "caller-controlled"}}
			headers := map[string]any{}
			if tc.runtimeNew {
				headers[interfaces.FunctionVersionHeader] = version
				headers[interfaces.FunctionCodeHashHeader] = digest
			}
			proxy.EXPECT().HandlerRequest(gomock.Any(), gomock.Any()).Return(&interfaces.HTTPResponse{
				StatusCode: 200, Body: body, Headers: headers,
				FunctionExecution: &interfaces.FunctionExecution{ToolID: "forged"},
			}, nil)
			service := &ToolServiceImpl{Logger: logger.DefaultLogger(), ToolBoxDB: boxes, ToolDB: tools, MetadataService: metadata, Proxy: proxy}
			result, err := service.ExecuteToolCore(context.Background(), &interfaces.ExecuteToolReq{BoxID: "box", ToolID: "tool"})
			if err != nil || result == nil {
				t.Fatalf("business execution failed: %#v %v", result, err)
			}
			if (result.FunctionExecution != nil) != tc.wantProof {
				t.Fatalf("unexpected proof: %#v", result.FunctionExecution)
			}
			if tc.wantProof && result.FunctionExecution.ToolID != "tool" {
				t.Fatalf("forged identity survived: %#v", result.FunctionExecution)
			}
			if result.StatusCode != 200 || result.Body.(map[string]any)["result"].(map[string]any)["function_execution"] != "caller-controlled" {
				t.Fatal("business result changed while deriving provenance")
			}
		})
	}
}
