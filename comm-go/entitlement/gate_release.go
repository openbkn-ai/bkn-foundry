//go:build !ee_dev

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
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
func DefaultGate() Gate {
	g, _ := defaultHubGate()
	if g == nil {
		return deniedGate{}
	}
	return g
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

func defaultHubGate() (*HubGate, error) {
	base, appkey := os.Getenv("BKN_SAFE_URL"), os.Getenv("BKN_SAFE_APPKEY")
	if base == "" || appkey == "" {
		return nil, nil
	}
	return NewHubGate(HubConfig{
		BaseURL: base,
		AppKey:  appkey,
		Keys:    keys.Official(),
	})
}
