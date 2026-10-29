package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/outboundtls"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestRetrustRequiresRegistrationModifyPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	authorization := mocks.NewMockIAuthorizationService(ctrl)
	accessor := &interfaces.AuthAccessor{ID: "user"}
	denied := errors.New("modify forbidden")
	authorization.EXPECT().GetAccessor(gomock.Any(), "user").Return(accessor, nil)
	authorization.EXPECT().CheckModifyPermission(gomock.Any(), accessor, "mcp-1", interfaces.AuthResourceTypeMCP).Return(denied)
	svc := &mcpServiceImpl{logger: logger.DefaultLogger(), AuthService: authorization}
	// No database/transaction/network dependencies are installed. Reaching any
	// trust-changing operation before authorization would panic or fail this test.
	_, err := svc.UpdateMCPServer(context.Background(), &interfaces.MCPServerUpdateRequest{UserID: "user", MCPID: "mcp-1", URL: "https://127.0.0.1:1", TLS: &outboundtls.Change{Expected: &outboundtls.Policy{}, Policy: outboundtls.Policy{SkipVerify: true}}})
	if !errors.Is(err, denied) {
		t.Fatalf("unauthorized trust change: %v", err)
	}
}
