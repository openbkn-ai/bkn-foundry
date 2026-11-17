// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package agent_operator

import (
	"net/http"
	"strings"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// A failed tool call names the platform's error code, so an action task shows why the tool was
// refused instead of a bare status (#3236). Nothing else of the body is taken: it may hold the
// tool's own values (#1374).
func TestToolFailureNamesThePlatformErrorCode(t *testing.T) {
	const watermark = "raw-detail-watermark-3236"
	tests := []struct {
		name     string
		status   int
		response string
		want     string
	}{
		{
			name:   "function runtime refused the call",
			status: http.StatusOK,
			response: `{"status_code":400,"body":{"error_code":"AgentOperatorIntegration.BadRequest.DebugParamsInvalid",` +
				`"description":"` + watermark + `","error_details":"` + watermark + `"}}`,
			want: "execute tool failed with status 400 (AgentOperatorIntegration.BadRequest.DebugParamsInvalid)",
		},
		{
			name:     "proxy refused the call",
			status:   http.StatusForbidden,
			response: `{"error_code":"Public.Forbidden","description":"` + watermark + `","error_details":"` + watermark + `"}`,
			want:     "proxy tool execution returned status 403 (Public.Forbidden)",
		},
		{
			name:     "a tool's own error code is not the platform's",
			status:   http.StatusOK,
			response: `{"status_code":400,"body":{"error_code":"` + watermark + `","code":"Vendor.Bad"}}`,
			want:     "execute tool failed with status 400",
		},
		{
			name:     "a body that is not an object",
			status:   http.StatusOK,
			response: `{"status_code":502,"body":"` + watermark + `"}`,
			want:     "execute tool failed with status 502",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			httpClient := rmock.NewMockHTTPClient(ctrl)
			access := &agentOperatorAccess{httpClient: httpClient, appSetting: &commonSettingForProxyTest}
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://operator/tool-box/box-1/proxy/tool-1", gomock.Any(), gomock.Any()).
				Return(test.status, []byte(test.response), nil)

			_, err := access.ExecuteToolAsProxy(actionProxyContext(interfaces.ProxyTargetTypeFunction, "box-1"),
				"box-1", "tool-1", interfaces.ToolExecutionRequest{})
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), watermark) {
				t.Fatalf("failure exposed the body: %v", err)
			}
		})
	}
}
