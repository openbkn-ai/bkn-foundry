//go:build !ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"log/slog"
	"os"

	"github.com/openbkn-ai/licverify/keys"
)

// DefaultGate is the gate a production build installs: pull the signed licence
// from bkn-safe, verify it here against the official public keys compiled into
// this binary, serve from the last verified snapshot.
//
// Configuration is only ever about where to fetch the certificate — never about
// which capabilities are on. That distinction is the whole discipline: the
// moment a deployment can set something like audit.enabled=true, every gate in
// the product is decorative. So this reads two values and no more:
//
//	BKN_SAFE_URL        in-cluster address of the licence hub
//	BKN_SAFE_APPKEY     service credential for the distribution endpoint
//
// Either one missing means the process cannot obtain a licence, and a process
// that cannot obtain one behaves as community. That is a legitimate steady
// state — community deployments never set these — so it is not an error and
// must not stop the service from starting.
//
// The caller is expected to run the returned gate's Run method in a goroutine;
// GateWithRunner exists so a caller can do that without type-asserting.
// DefaultGate discards the refresher, so the snapshot it hands back is whatever
// the one synchronous fetch at construction produced. Prefer GateWithRunner in
// a service: without the background loop the gate never learns about a
// certificate installed later, nor about one that expires.
//
// It delegates rather than building its own gate — calling both would otherwise
// produce two HubGates polling the same hub, each with its own snapshot, and
// two synchronous fetches during boot.
func DefaultGate() Gate {
	g, _ := GateWithRunner()
	return g
}

// defaultHubGate resolves the hub configuration, or reports that this
// deployment has none.
//
// Every path that ends in "no gate" is logged except the one that is genuinely
// normal — neither variable set, i.e. a community deployment. The others look
// identical from the outside (all paid capability off, no certificate fetched),
// and without a line in the log an operator cannot tell "this is a community
// install" from "the helm upgrade dropped BKN_SAFE_APPKEY".
func defaultHubGate() (*HubGate, error) {
	base, appkey := os.Getenv("BKN_SAFE_URL"), os.Getenv("BKN_SAFE_APPKEY")
	switch {
	case base == "" && appkey == "":
		return nil, nil
	case base == "" || appkey == "":
		slog.Warn("entitlement: licence hub is half-configured; running as community",
			"has_url", base != "", "has_appkey", appkey != "")
		return nil, nil
	}

	g, err := NewHubGate(HubConfig{
		BaseURL: base,
		AppKey:  appkey,
		Keys:    keys.Official(),
	})
	if err != nil {
		// The realistic cause is an empty verification key table, which means
		// this build cannot verify a certificate even if it fetches one.
		// Community behaviour is the right outcome; silence is not.
		slog.Error("entitlement: licence hub unusable; running as community", "err", err)
		return nil, err
	}
	return g, nil
}

// GateWithRunner returns the production gate together with its background
// refresher, or (deniedGate, nil) when this deployment has no hub configured.
//
//	g, run := entitlement.GateWithRunner()
//	entitlement.SetGate(g)
//	if run != nil { go run(ctx.Done()) }
func GateWithRunner() (Gate, func(stop <-chan struct{})) {
	g, _ := defaultHubGate()
	if g == nil {
		return deniedGate{}, nil
	}
	return g, g.Run
}
