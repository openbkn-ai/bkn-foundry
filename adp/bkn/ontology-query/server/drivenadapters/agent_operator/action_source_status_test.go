// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package agent_operator

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"
	"net/http"
	"strings"
	"testing"
)

func TestSourcePermissionStatusIsTypedWithoutRemoteDetails(t *testing.T) {
	for _, kind := range []string{"tool", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := rmock.NewMockHTTPClient(ctrl)
			access := &agentOperatorAccess{httpClient: client, appSetting: &commonSettingForProxyTest}
			client.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(http.StatusForbidden, []byte(`{"code":"SOURCE_FORBIDDEN","description":"private remote description","detail":"credential-sensitive"}`), nil)
			var err error
			if kind == "tool" {
				_, err = access.executeTool(context.Background(), "box", "tool", interfaces.ToolExecutionRequest{}, nil)
			} else {
				_, err = access.executeMCP(context.Background(), "mcp", "tool", interfaces.MCPExecutionRequest{}, nil)
			}
			var status *httperrors.HTTPError
			if !errors.As(err, &status) || status.HTTPCode != 403 || status.ErrorCode != "SOURCE_FORBIDDEN" {
				t.Fatalf("source status was lost: %v", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "credential") {
				t.Fatal("source response details entered action error")
			}
		})
	}
}
