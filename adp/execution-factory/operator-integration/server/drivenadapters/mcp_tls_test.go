package drivenadapters

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/outboundtls"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestMCPTransportRejectsUnknownCertificateAndAcceptsInspectedPin(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	manager, err := outboundtls.New(outboundtls.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	previous := outboundtls.Default()
	outboundtls.SetDefault(manager)
	defer outboundtls.SetDefault(previous)
	core := &interfaces.MCPCoreConfigInfo{URL: server.URL}
	strict := newMCPHTTPClient(core, time.Second)
	defer strict.CloseIdleConnections()
	resp, err := strict.Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || calls.Load() != 0 {
		t.Fatal("MCP sent HTTP data to an untrusted endpoint")
	}
	core.TrustPreview = &outboundtls.Policy{Fingerprint: outboundtls.Fingerprint(server.Certificate())}
	pinned := newMCPHTTPClient(core, time.Second)
	defer pinned.CloseIdleConnections()
	resp, err = pinned.Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil || calls.Load() != 1 {
		t.Fatalf("MCP rejected the inspected certificate: %v", err)
	}
}
