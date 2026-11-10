// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
)

func newTestBknBackendClient(t *testing.T) (*bknBackendClient, *mocks.MockHTTPClient) {
	t.Helper()
	httpClient := mocks.NewMockHTTPClient(gomock.NewController(t))
	return &bknBackendClient{
		baseURL:    "http://bkn-backend-svc:13014/api/bkn-backend/in",
		logger:     logger.DefaultLogger(),
		httpClient: httpClient,
	}, httpClient
}

func TestRunCypherSendsTheAuthenticatedAccount(t *testing.T) {
	client, httpClient := newTestBknBackendClient(t)
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "proxy-1", AccountType: interfaces.AccessorTypeApp,
	})
	req := &interfaces.CypherQueryReq{Query: "MATCH (s:supplier {id: $id}) RETURN count(s)", Parameters: map[string]any{"id": "S1"}}
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(),
		"http://bkn-backend-svc:13014/api/bkn-backend/in/v1/knowledge-networks/kn%2F1/cypher-queries",
		gomock.Any(), req).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers["x-account-id"] != "proxy-1" || headers["x-account-type"] != "app" {
				t.Fatalf("identity headers = %v, want proxy-1/app", headers)
			}
			if headers["Content-Type"] != "application/json" {
				t.Fatalf("content type = %q", headers["Content-Type"])
			}
			return http.StatusForbidden, []byte(`{"error_code":"PublicError_Forbidden"}`), nil
		})

	result, err := client.RunCypher(ctx, "kn/1", req)
	if err != nil {
		t.Fatalf("RunCypher returned error: %v", err)
	}
	// A refusal from bkn-backend is a result, not a transport failure: the
	// caller passes its status and body through unchanged.
	if result.StatusCode != http.StatusForbidden || string(result.Body) != `{"error_code":"PublicError_Forbidden"}` {
		t.Fatalf("result = %d %s", result.StatusCode, result.Body)
	}
}

// Called through a knowledge network's proxy (a logic property, or an agent's
// execute_tool), the authenticated account is the network's proxy account. The
// statement still runs as the caller the proxy acts for, exactly as a Python
// function reads BKN with that caller's own credential: the proxy account holds
// no permission on the network's concepts, and must not lend its own.
func TestRunCypherRunsAsTheCallerBehindAProxy(t *testing.T) {
	client, httpClient := newTestBknBackendClient(t)
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "proxy-1", AccountType: interfaces.AccessorTypeApp,
	})
	ctx = interfaces.WithProxyExecutionContext(ctx, interfaces.ProxyExecutionContext{
		CallerID: "user-1", CallerType: "user", ProxyID: "proxy-1", ProxyType: "app",
	})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers["x-account-id"] != "user-1" || headers["x-account-type"] != "user" {
				t.Fatalf("identity headers = %v, want the caller user-1/user", headers)
			}
			return http.StatusOK, []byte(`{"columns":[],"entries":[]}`), nil
		})
	if _, err := client.RunCypher(ctx, "kn-1", &interfaces.CypherQueryReq{Query: "MATCH (n) RETURN count(n)"}); err != nil {
		t.Fatalf("RunCypher returned error: %v", err)
	}
}

// A proxy context without its caller is never filled in with the proxy account.
func TestRunCypherBehindAProxyRefusesWithoutTheCaller(t *testing.T) {
	client, _ := newTestBknBackendClient(t)
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "proxy-1", AccountType: interfaces.AccessorTypeApp,
	})
	ctx = interfaces.WithProxyExecutionContext(ctx, interfaces.ProxyExecutionContext{ProxyID: "proxy-1", ProxyType: "app"})
	_, err := client.RunCypher(ctx, "kn-1", &interfaces.CypherQueryReq{Query: "MATCH (n) RETURN count(n)"})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401", err)
	}
}

func TestRunCypherRefusesWithoutAnAuthenticatedAccount(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no account context": context.Background(),
		"empty account id": common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
			AccountType: interfaces.AccessorTypeUser,
		}),
		"empty account type": common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
			AccountID: "user-1",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			// No EXPECT: any request reaching the HTTP client fails the test.
			client, _ := newTestBknBackendClient(t)
			_, err := client.RunCypher(ctx, "kn-1", &interfaces.CypherQueryReq{Query: "MATCH (n) RETURN n"})
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusUnauthorized {
				t.Fatalf("err = %v, want 401", err)
			}
		})
	}
}

func TestRunCypherReportsTransportFailureAsBadGateway(t *testing.T) {
	client, httpClient := newTestBknBackendClient(t)
	ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(0, nil, errors.New("connection refused"))
	_, err := client.RunCypher(ctx, "kn-1", &interfaces.CypherQueryReq{Query: "MATCH (n) RETURN n"})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadGateway {
		t.Fatalf("err = %v, want 502", err)
	}
}
