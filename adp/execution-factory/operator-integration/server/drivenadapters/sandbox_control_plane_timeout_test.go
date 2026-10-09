package drivenadapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestExecuteCodeSyncAllowsSandboxTimeoutResponse(t *testing.T) {
	for _, tc := range []struct {
		timeout int
		url     string
	}{
		{timeout: 60, url: "http://sandbox/api/v1/executions/sessions/s1/execute-sync"},
		{timeout: 300, url: "http://sandbox/api/v1/executions/sessions/s1/execute-sync?sync_timeout=305"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			executeHTTPClient := mocks.NewMockHTTPClient(ctrl)
			executeHTTPClient.EXPECT().PostNoUnmarshal(gomock.Any(), tc.url, gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte(`{"status":"timeout"}`), nil)
			client := &sandBoxControlPlaneClient{
				baseURL: "http://sandbox/api/v1", logger: logger.DefaultLogger(),
				httpClient: mocks.NewMockHTTPClient(ctrl), executeHTTPClient: executeHTTPClient,
			}
			resp, err := client.ExecuteCodeSync(context.Background(), "s1", &interfaces.ExecuteCodeReq{Timeout: tc.timeout})
			if err != nil || resp == nil || resp.Status != "timeout" {
				t.Fatalf("sandbox response = %+v, %v; want timeout status", resp, err)
			}
		})
	}
}
