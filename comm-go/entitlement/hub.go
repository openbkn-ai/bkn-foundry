// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/licverify"
)

// The hub gate is how a production build learns what the deployment paid for.
//
// bkn-safe holds the certificate and is the only component that talks to the
// outside; every other service pulls the signed text from it and verifies that
// text locally against a public key compiled into the binary. So a module never
// trusts bkn-safe's judgement, only its delivery — a tampered or impersonated
// hub can withhold a licence but cannot manufacture one.
//
//	GET /api/safe/v1/internal/license/current    (AppKey authenticated)
//	200 {"license": "<signed text>", "etag": "..."}   + ETag header
//	304                                                on If-None-Match
//	404 {"error": "no license installed"}
//
// # Behaviour when the hub is unreachable
//
// Neither extreme is acceptable. Serving paid capability without a licence
// gives the product away; dropping it the instant a fetch fails means one
// restart of bkn-safe takes every paid capability in the cluster down at once,
// which is an outage caused by the licensing system rather than by the licence.
//
// So: keep serving from the last snapshot that verified, and let licverify's
// grace window decide when that stops being true. A cold start that never
// reached the hub has no snapshot at all, which is community behaviour — the
// zero value already points that way.
//
// # Two-stage polling
//
// Cold start retries in seconds, steady state in hours. Both are needed: during
// a rolling restart bkn-safe and the business services come up together, so a
// service will normally fail its first few fetches. With only the hourly
// cadence, bkn-safe being thirty seconds late would leave a service in
// community behaviour until the next hour — a licensed deployment quietly
// serving unlicensed behaviour for most of an hour.

// HubConfig configures the production gate.
type HubConfig struct {
	// BaseURL is bkn-safe's in-cluster address, e.g.
	// http://bkn-safe:8080. Required.
	BaseURL string
	// AppKey authenticates this service to the distribution endpoint. The
	// surface is deliberately not anonymous. Required.
	AppKey string
	// Keys are the verification public keys (kid → key), compiled into the
	// binary. Required — without them nothing can be verified and every fetch
	// would be pointless.
	Keys map[string]ed25519.PublicKey
	// Interval is the steady-state re-check cadence. Default 1h.
	Interval time.Duration
	// ColdInterval is the initial retry delay before the first successful
	// verification; it doubles up to ColdMaxInterval. Defaults 1s / 30s.
	ColdInterval    time.Duration
	ColdMaxInterval time.Duration
	// HTTPClient defaults to a 10s-timeout client. In-cluster hop; a long
	// timeout here just delays the retry that is going to happen anyway.
	HTTPClient *http.Client
	// Logf receives operational messages. Optional.
	Logf func(format string, args ...any)
}

// HubGate polls bkn-safe, verifies locally, and publishes the result as an
// atomically-swapped snapshot.
type HubGate struct {
	cfg  HubConfig
	keys map[string]ed25519.PublicKey

	snap atomic.Pointer[Snapshot]

	mu   sync.Mutex
	etag string
	text string
}

// NewHubGate builds the gate and performs one fetch synchronously, so that a
// process which starts with a reachable hub is correctly licensed by the time
// it serves its first request. A failed first fetch is not an error: the gate
// starts at community and the background loop keeps trying.
func NewHubGate(cfg HubConfig) (*HubGate, error) {
	if cfg.BaseURL == "" || cfg.AppKey == "" {
		return nil, errors.New("entitlement: hub gate needs BaseURL and AppKey")
	}
	if len(cfg.Keys) == 0 {
		return nil, errors.New("entitlement: hub gate needs verification keys — a build that cannot verify must not pretend to be licensed")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.ColdInterval <= 0 {
		cfg.ColdInterval = time.Second
	}
	if cfg.ColdMaxInterval <= 0 {
		cfg.ColdMaxInterval = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Logf == nil {
		cfg.Logf = func(format string, args ...any) { slog.Debug(fmt.Sprintf(format, args...)) }
	}
	g := &HubGate{cfg: cfg, keys: cfg.Keys}
	g.publish(Snapshot{Edition: licverify.EditionCommunity})
	_ = g.refresh()
	return g, nil
}

// Snapshot implements Gate: one atomic pointer read, no I/O.
func (g *HubGate) Snapshot() Snapshot {
	if s := g.snap.Load(); s != nil {
		return *s
	}
	return Snapshot{Edition: licverify.EditionCommunity}
}

// Run keeps the snapshot current until the context is cancelled. Start it in a
// goroutine during boot.
func (g *HubGate) Run(stop <-chan struct{}) {
	delay := g.cfg.ColdInterval
	for {
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}
		err := g.refresh()
		switch {
		case err != nil && !g.Snapshot().Licensed:
			// Still cold: back off quickly, we have nothing to serve from.
			delay *= 2
			if delay > g.cfg.ColdMaxInterval {
				delay = g.cfg.ColdMaxInterval
			}
			g.cfg.Logf("entitlement: licence not available yet (%v); retrying in %s", err, delay)
		case err != nil:
			// We have a verified snapshot; a failed refresh is not a reason to
			// drop it. licverify decides when grace runs out.
			delay = g.cfg.Interval
			g.cfg.Logf("entitlement: licence refresh failed (%v); serving the last verified snapshot", err)
		default:
			delay = g.cfg.Interval
		}
	}
}

// refresh fetches, verifies and publishes. It returns an error only to drive
// the retry cadence — callers never gate on it.
func (g *HubGate) refresh() error {
	text, changed, err := g.fetch()
	if err != nil {
		return err
	}
	if !changed && g.Snapshot().Licensed {
		// 304 and we already hold a verified snapshot. Re-evaluate anyway:
		// an unchanged certificate still expires with the passage of time.
		text = g.currentText()
	}
	if text == "" {
		return errors.New("no licence text")
	}
	snap := evaluate(text, g.keys)
	g.publish(snap)
	return nil
}

// fetch returns the licence text, whether it changed, and any transport error.
func (g *HubGate) fetch() (text string, changed bool, err error) {
	req, err := http.NewRequest(http.MethodGet, g.cfg.BaseURL+"/api/safe/v1/internal/license/current", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+g.cfg.AppKey)
	if et := g.currentETag(); et != "" {
		req.Header.Set("If-None-Match", `"`+et+`"`)
	}

	resp, err := g.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return "", false, nil
	case http.StatusNotFound:
		// No licence installed in the cluster. That is a legitimate steady
		// state (community deployment), not a transport failure — but there is
		// nothing to verify, so the snapshot stays community.
		g.publish(Snapshot{Edition: licverify.EditionCommunity})
		return "", false, nil
	case http.StatusOK:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", false, fmt.Errorf("licence hub returned %s: %s", resp.Status, body)
	}

	var payload struct {
		License string `json:"license"`
		ETag    string `json:"etag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false, err
	}
	g.remember(payload.ETag, payload.License)
	return payload.License, true, nil
}

func (g *HubGate) publish(s Snapshot) { g.snap.Store(&s) }

func (g *HubGate) remember(etag, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.etag, g.text = etag, text
}

func (g *HubGate) currentETag() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.etag
}

func (g *HubGate) currentText() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.text
}
