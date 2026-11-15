package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestForwardReportsGatewayTimeoutWhenUpstreamIsTooSlow(t *testing.T) {
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer target.Close()
	defer close(release)

	f := &forwarder{
		pool: &clientPool{
			logger:      logger.DefaultLogger(),
			clients:     make(map[clientKey]*ProxyClient),
			config:      PoolConfig{MaxClients: 4, MaxTimeout: 5 * time.Second, DefaultTimeout: 5 * time.Second},
			stopCleanup: make(chan struct{}),
		},
		logger: logger.DefaultLogger(),
	}
	resp, err := f.Forward(context.Background(), &interfaces.HTTPRequest{
		Timeout:    time.Second,
		HTTPRouter: interfaces.HTTPRouter{Method: http.MethodGet, URL: target.URL},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	if !strings.Contains(resp.Error, "timed out") {
		t.Fatalf("error should say the proxy timed out, got %q", resp.Error)
	}
}
