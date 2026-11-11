package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// A server that rejects the credentials with a JSON error body (as Context Loader does) must be
// reported as an authentication failure. mcp-go v0.37.0 parses any JSON error body as a JSON-RPC
// response and drops the status, which surfaced as "unexpected end of JSON input".
func TestNewMCPClientReportsJSONBodiedUnauthorizedAsAuthFailure(t *testing.T) {
	// The shared client otherwise reads the deployed config file.
	httpClientOnce.Do(func() { httpClient = &http.Client{} })
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error_code":"Public.Unauthorized","description":"token is invalid"}`))
		}))
		_, err := NewMCPClient(context.Background(), &interfaces.MCPCoreConfigInfo{
			Mode: interfaces.MCPModeStream,
			URL:  srv.URL + "/mcp",
		})
		srv.Close()

		httpErr := requireHTTPError(t, err)
		if !strings.HasSuffix(httpErr.ErrorCode, string(errors.ErrExtMCPServerAuthFailed)) {
			t.Fatalf("status %d: code = %s, want auth failure; description = %s", status, httpErr.ErrorCode, httpErr.Description)
		}
		if details := fmt.Sprint(httpErr.ErrorDetails); !strings.Contains(details, "token is invalid") {
			t.Errorf("status %d: error details = %q, want the server's message in them", status, details)
		}
	}
}

// A JSON-RPC error answered with a 4xx other than an auth rejection keeps reaching mcp-go, which
// reads its message.
func TestMCPAuthStatusTransportPassesOtherStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	resp, err := mcpAuthStatusClient(&http.Client{}).Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
