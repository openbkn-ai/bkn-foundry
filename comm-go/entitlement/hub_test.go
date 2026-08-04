// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/licverify"
)

// fakeHub stands in for bkn-safe's distribution endpoint. It reproduces the
// three behaviours the client depends on, taken from
// bkn-safe/server/internal/httpapi/license.go:
//
//	200  {"license": …, "etag": …}  with the ETag header quoted
//	304  when If-None-Match carries the current validator
//	404  when no certificate is installed
//
// The ETag is a hash of the text, exactly as the hub computes it — which is
// what makes "revoke, then re-install the same certificate" produce the same
// validator, and is the reason the 404 path has to drop the cached one.
type fakeHub struct {
	mu       sync.Mutex
	licence  string // empty means "none installed" → 404
	requests int
	// authz records the Authorization header of every request. The endpoint is
	// tokenless, so the assertion on this is that it stays empty — kept as a
	// test rather than a comment because a credential quietly reappearing here
	// is exactly the regression that would put every consuming service back to
	// waiting on an issuer/rotation/revocation story.
	authz []string
	// conditional records, per request, whether the client sent a validator.
	// After the hub reports 404 the client must have dropped its cached ETag,
	// so the next fetch is unconditional — otherwise recovery depends on text
	// the hub has said no longer exists.
	conditional []bool
	status      int // when non-zero, answer with this instead (transport failure simulation)
}

func (h *fakeHub) set(text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.licence = text
}

func (h *fakeHub) fail(status int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = status
}

func (h *fakeHub) hits() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests
}

func (h *fakeHub) lastWasConditional() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conditional[len(h.conditional)-1]
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests++
	h.authz = append(h.authz, r.Header.Get("Authorization"))
	h.conditional = append(h.conditional, r.Header.Get("If-None-Match") != "")

	if h.status != 0 {
		w.WriteHeader(h.status)
		return
	}
	if h.licence == "" {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no license installed"})
		return
	}

	sum := sha256.Sum256([]byte(h.licence))
	etag := hex.EncodeToString(sum[:])[:8]
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.Header().Set("ETag", `"`+etag+`"`)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	_ = json.NewEncoder(w).Encode(map[string]string{"license": h.licence, "etag": etag})
}

// enterpriseLicence mints a certificate valid for a month, with the key table
// the gate should be built with.
func enterpriseLicence(t *testing.T) (string, map[string]ed25519.PublicKey) {
	t.Helper()
	now := time.Now()
	return mintLicence(t, licverify.Payload{
		LicID:     "hub-test",
		Edition:   licverify.EditionEnterprise,
		IssuedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
		Features:  []string{"audit"},
	})
}

func newTestGate(t *testing.T, hub *fakeHub, keys map[string]ed25519.PublicKey) (*HubGate, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	g, err := NewHubGate(HubConfig{
		BaseURL: srv.URL,
		Keys:    keys,
		Logf:    func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewHubGate: %v", err)
	}
	return g, srv
}

func TestHubGateFetchesAndVerifies(t *testing.T) {
	text, keys := enterpriseLicence(t)
	hub := &fakeHub{licence: text}
	g, _ := newTestGate(t, hub, keys)

	// NewHubGate fetches synchronously, so a process that starts with a
	// reachable hub is correctly licensed before it serves anything.
	snap := g.Snapshot()
	if !snap.Licensed || snap.Edition != licverify.EditionEnterprise {
		t.Fatalf("snapshot = %+v, want licensed enterprise", snap)
	}
	// No credential travels with the fetch: the distribution endpoint is
	// tokenless by design, and a service needs BKN_SAFE_URL and nothing else.
	if hub.authz[0] != "" {
		t.Fatalf("Authorization = %q, want none — the endpoint is tokenless and the client must not require a credential to reach it", hub.authz[0])
	}
}

func TestHubGateReusesLocalTextOn304(t *testing.T) {
	text, keys := enterpriseLicence(t)
	hub := &fakeHub{licence: text}
	g, _ := newTestGate(t, hub, keys)

	// Second poll sends If-None-Match and gets 304. The gate must re-evaluate
	// the text it already holds rather than treat "unchanged" as "gone":
	// nothing changed about the certificate, but time has passed and it may
	// have crossed into grace.
	if err := g.refresh(); err != nil {
		t.Fatalf("refresh after 304: %v", err)
	}
	if snap := g.Snapshot(); !snap.Licensed {
		t.Fatalf("a 304 must not drop the licence, got %+v", snap)
	}
	if hub.hits() != 2 {
		t.Fatalf("expected a conditional second request, got %d hits", hub.hits())
	}
}

func TestHubGateRecoversAfterRevokeAndReinstall(t *testing.T) {
	// The regression this test exists for. The hub's ETag is a hash of the
	// text, so re-installing the *same* certificate produces the same
	// validator. If the 404 path left the old validator cached, the re-install
	// would be answered 304 against text the gate no longer holds, and the
	// deployment would stay community until someone restarted the process —
	// exactly the "fix a licence problem by restarting on a customer site"
	// failure this design set out to avoid.
	text, keys := enterpriseLicence(t)
	hub := &fakeHub{licence: text}
	g, _ := newTestGate(t, hub, keys)

	if !g.Snapshot().Licensed {
		t.Fatal("setup: expected a licensed gate")
	}

	hub.set("") // administrator removes the certificate
	if err := g.refresh(); err != nil {
		t.Fatalf("404 is a definite answer, not a failure: %v", err)
	}
	if snap := g.Snapshot(); snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("snapshot = %+v, want community after removal", snap)
	}

	hub.set(text) // the same certificate goes back in
	if err := g.refresh(); err != nil {
		t.Fatalf("refresh after re-install: %v", err)
	}
	if hub.lastWasConditional() {
		t.Fatal("after a 404 the cached validator must be dropped, so the next fetch is unconditional — otherwise recovery leans on text the hub said is gone")
	}
	if snap := g.Snapshot(); !snap.Licensed || snap.Edition != licverify.EditionEnterprise {
		t.Fatalf("snapshot = %+v, want the licence back without a restart", snap)
	}
}

func TestHubGateKeepsLastVerifiedSnapshotOnFailure(t *testing.T) {
	text, keys := enterpriseLicence(t)
	hub := &fakeHub{licence: text}
	g, _ := newTestGate(t, hub, keys)

	// bkn-safe restarts, or the network blips. Dropping paid capability here
	// would turn one component's restart into a cluster-wide outage; licverify's
	// grace window is what decides when a stale snapshot stops counting.
	hub.fail(http.StatusInternalServerError)
	if err := g.refresh(); err == nil {
		t.Fatal("a 500 should be reported so the retry cadence can react")
	}
	if snap := g.Snapshot(); !snap.Licensed {
		t.Fatalf("a failed refresh must not drop the last verified snapshot, got %+v", snap)
	}
}

func TestHubGateStartsCommunityWhenHubIsDown(t *testing.T) {
	_, keys := enterpriseLicence(t)
	hub := &fakeHub{}
	hub.fail(http.StatusServiceUnavailable)
	g, _ := newTestGate(t, hub, keys)

	// A cold start that never reached the hub has nothing to serve from. The
	// direction is the safe one: withhold paid capability, keep running.
	if snap := g.Snapshot(); snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("snapshot = %+v, want unlicensed community", snap)
	}
}

func TestHubGateTreatsMissingLicenceAsASteadyState(t *testing.T) {
	_, keys := enterpriseLicence(t)
	hub := &fakeHub{} // configured hub, no certificate installed yet
	g, _ := newTestGate(t, hub, keys)

	// "The hub says there is no certificate" is a definite answer, not a
	// failure. Reporting it as an error would put an enterprise deployment
	// awaiting its certificate into the cold-start retry loop — a poll every
	// 30 seconds, and a log line every time, indefinitely.
	if err := g.refresh(); err != nil {
		t.Fatalf("a 404 must not be an error: %v", err)
	}
	if snap := g.Snapshot(); snap.Licensed {
		t.Fatalf("snapshot = %+v, want community", snap)
	}
}

func TestHubGateRejectsAForgedCertificate(t *testing.T) {
	// A hijacked or impersonated hub can withhold a licence; it must not be
	// able to manufacture one. Verification is local, against keys compiled
	// into the binary.
	forged, _ := enterpriseLicence(t)    // signed by one key…
	_, otherKeys := enterpriseLicence(t) // …verified against another
	hub := &fakeHub{licence: forged}
	g, _ := newTestGate(t, hub, otherKeys)

	if snap := g.Snapshot(); snap.Licensed {
		t.Fatalf("a certificate signed by an unknown key must not license anything, got %+v", snap)
	}
}

func TestHubGateNeedsVerificationKeys(t *testing.T) {
	// A build with no key table cannot verify anything, so fetching would be
	// theatre. Fail loudly at construction instead of running as an
	// enterprise-looking process that trusts whatever it is handed.
	_, err := NewHubGate(HubConfig{BaseURL: "http://example.invalid"})
	if err == nil {
		t.Fatal("expected a refusal when no verification keys are configured")
	}
}

func TestUnreachableHubStillLetsTheClockRun(t *testing.T) {
	// The bypass this test exists for: the snapshot is a one-shot product of
	// evaluate(), and licverify's verdict depends on time. If the only paths to
	// evaluate() required a successful fetch, an unreachable hub would freeze
	// the last verdict — and the service runs on the customer's machine, on the
	// customer's network, so blocking egress to bkn-safe would make a paid
	// licence permanent.
	//
	// A certificate that expires a second from now lets the transition be
	// observed without waiting out a grace window: valid → grace has to happen
	// while every fetch is failing.
	now := time.Now()
	text, keys := mintLicence(t, licverify.Payload{
		LicID:     "expiring",
		Edition:   licverify.EditionEnterprise,
		IssuedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt: now.Add(time.Second).Unix(),
	})
	hub := &fakeHub{licence: text}
	g, _ := newTestGate(t, hub, keys)

	if snap := g.Snapshot(); snap.State != licverify.StateValid {
		t.Fatalf("setup: State = %q, want valid", snap.State)
	}

	hub.fail(http.StatusServiceUnavailable) // egress dies, or bkn-safe does
	// 2.5s, not 1.2s: ExpiresAt has second granularity, so a sub-second start
	// offset can leave now.Unix() == ExpiresAt after a 1.2s sleep and the
	// licence still reads as valid — the test would pass or fail on where in
	// the second it happened to start.
	time.Sleep(2500 * time.Millisecond)

	if err := g.refresh(); err == nil {
		t.Fatal("the failure should still be reported, it drives the retry cadence")
	}
	// Grace keeps the capability on — that is deliberate — but the verdict has
	// to have moved. Frozen at "valid" is what would never expire.
	if snap := g.Snapshot(); snap.State != licverify.StateGrace {
		t.Fatalf("State = %q, want grace: an unreachable hub must not freeze the clock", snap.State)
	}
}
