package outboundtls

import (
	"context"
	"testing"
)

func TestUpgradeIsIdempotentAndNeverRefreshesTrust(t *testing.T) {
	upstream, calls := tlsServer(t, certificate(t, "ingress.local", false))
	store := newMemory()
	m, _ := New(Config{}, store)
	ctx := context.Background()
	registrations := []Registration{{Scope: "mcp:one", URL: upstream.URL + "/mcp"}, {Scope: "mcp:two", URL: upstream.URL + "/sse"}, {Scope: "toolbox:one", URL: "http://internal"}}
	if err := m.Bootstrap(ctx, registrations); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Get(ctx, "mcp:one", upstream.URL)
	second, _ := m.Get(ctx, "mcp:two", upstream.URL)
	if first.Fingerprint == "" || first != second {
		t.Fatal("upgrade did not retain a stable fingerprint per registration")
	}
	if calls.Load() != 0 {
		t.Fatal("upgrade sent an HTTP business request")
	}
	upstream.Close()
	// Repeated upgrades and restarts need no network once a decision exists.
	if err := m.Bootstrap(ctx, registrations); err != nil {
		t.Fatal("repeated upgrade probed a trusted endpoint:", err)
	}
	if err := m.Bootstrap(ctx, []Registration{{Scope: "mcp:new", URL: upstream.URL}}); err == nil {
		t.Fatal("unreachable endpoint was silently trusted")
	}
}
