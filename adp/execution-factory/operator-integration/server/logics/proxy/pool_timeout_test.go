package proxy

import (
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
)

func TestGetClientWithGraceCapsExecutionBeforeAddingResponseTime(t *testing.T) {
	pool := &clientPool{
		logger:  logger.DefaultLogger(),
		clients: make(map[clientKey]*ProxyClient),
		config: PoolConfig{
			MaxClients: 4, MaxTimeout: 10 * time.Second, DefaultTimeout: 3 * time.Second,
		},
	}
	if got := pool.GetClientWithGrace(20*time.Second, 5*time.Second).Timeout; got != 15*time.Second {
		t.Fatalf("capped execution plus response grace = %v, want 15s", got)
	}
	if got := pool.GetClientWithGrace(0, 5*time.Second).Timeout; got != 8*time.Second {
		t.Fatalf("default execution plus response grace = %v, want 8s", got)
	}
	if got := pool.GetClient(20 * time.Second).Timeout; got != 10*time.Second {
		t.Fatalf("ordinary proxy timeout = %v, want 10s", got)
	}
}
