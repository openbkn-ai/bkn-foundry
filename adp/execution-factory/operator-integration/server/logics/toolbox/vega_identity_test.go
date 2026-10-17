package toolbox

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestVegaToolIdentity(t *testing.T) {
	const origin = "http://configured-vega:13014"
	for _, tc := range []struct {
		name, target string
		auth         *interfaces.AccountAuthContext
		wantIdentity bool
	}{
		{"configured internal target", origin, &interfaces.AccountAuthContext{AccountID: "caller", AccountType: interfaces.AccessorTypeUser}, true},
		{"different host", "http://third-party:13014", &interfaces.AccountAuthContext{AccountID: "caller", AccountType: interfaces.AccessorTypeUser}, false},
		{"different port", "http://configured-vega:13015", &interfaces.AccountAuthContext{AccountID: "caller", AccountType: interfaces.AccessorTypeUser}, false},
		{"different scheme", "https://configured-vega:13014", &interfaces.AccountAuthContext{AccountID: "caller", AccountType: interfaces.AccessorTypeUser}, false},
		{"missing authentication", origin, nil, false},
		{"missing account type", origin, &interfaces.AccountAuthContext{AccountID: "caller"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			metadataService := mocks.NewMockIMetadataService(ctrl)
			metadata := mocks.NewMockIMetadataDB(ctrl)
			proxy := mocks.NewMockProxyHandler(ctrl)
			metadataService.EXPECT().GetMetadataBySource(gomock.Any(), "source", model.SourceTypeOpenAPI).Return(true, metadata, nil)
			metadata.EXPECT().GetPath().Return("/api/vega-backend/in/v1/resources/query")
			metadata.EXPECT().GetMethod().Return(http.MethodPost)
			proxy.EXPECT().HandlerRequest(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *interfaces.HTTPRequest) (*interfaces.HTTPResponse, error) {
				if req.Headers["x-account-id"] != "caller" && tc.wantIdentity {
					t.Errorf("identity was not rebuilt: %#v", req.Headers)
				}
				if req.Headers["x-account-type"] != string(interfaces.AccessorTypeUser) && tc.wantIdentity {
					t.Errorf("account type was not rebuilt")
				}
				for _, key := range []string{"X-Account-Id", "X-Account-Type", "X-Authorization"} {
					if _, ok := req.Headers[key]; ok {
						t.Errorf("caller identity/credential survived sanitization: %s", key)
					}
				}
				if !tc.wantIdentity {
					if _, ok := req.Headers["x-account-id"]; ok {
						t.Error("identity leaked to untrusted or unauthenticated request")
					}
				}
				if req.Headers["X-Api-Key"] != "business-key" {
					t.Error("business header lost")
				}
				return &interfaces.HTTPResponse{StatusCode: http.StatusOK}, nil
			})
			service := &ToolServiceImpl{MetadataService: metadataService, Proxy: proxy, VegaBackendURL: origin}
			ctx := common.SetPublicAPIToCtx(context.Background(), true)
			if tc.auth != nil {
				ctx = common.SetAccountAuthContextToCtx(ctx, tc.auth)
			}
			_, err := service.executeTool(ctx, &interfaces.ExecuteToolReq{ToolID: "tool", HTTPRequestParams: interfaces.HTTPRequestParams{Headers: map[string]any{"X-Account-Id": "forged", "X-Account-Type": "app", "X-Authorization": "forged-token", "X-Api-Key": "business-key"}}}, &model.ToolDB{SourceID: "source", SourceType: model.SourceTypeOpenAPI}, tc.target)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVegaInternalTargetBoundary(t *testing.T) {
	s := &ToolServiceImpl{VegaBackendURL: "http://configured-vega:13014"}
	for _, target := range []string{
		"http://configured-vega:13014/api/vega-backend/v1/resources/query",
		"http://configured-vega:13014/api/vega-backend/internal-v1/query",
		"http://configured-vega:13014/other",
		"http://configured-vega:13014.evil/api/vega-backend/in/v1/resources/query",
		"http://user@configured-vega:13014/api/vega-backend/in/v1/resources/query",
	} {
		if s.isVegaInternalTarget(target) {
			t.Errorf("untrusted target accepted: %s", target)
		}
	}
	s.VegaBackendURL = ""
	if s.isVegaInternalTarget("http://configured-vega:13014/api/vega-backend/in/v1/resources/query") {
		t.Error("unconfigured target accepted")
	}
}
