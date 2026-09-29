// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package license

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// fakeIssuer answers /status with whatever the test sets, and /activate with a
// freshly bound certificate.
type fakeIssuer struct {
	t      *testing.T
	priv   ed25519.PrivateKey
	mu     sync.Mutex
	code   int    // HTTP status for /status
	status string // body status for /status
	calls  atomic.Int32
	// onStatus runs inside the /status handler before it answers, to simulate
	// something changing while the check is in flight.
	onStatus func()
}

func newFakeIssuer(t *testing.T, priv ed25519.PrivateKey) (*fakeIssuer, *httptest.Server) {
	f := &fakeIssuer{t: t, priv: priv, code: http.StatusOK, status: BindingBound}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			License    string `json:"license"`
			InstanceFP string `json:"instance_fp"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch r.URL.Path {
		case "/api/licenses/status":
			f.calls.Add(1)
			if req.InstanceFP != localFP() {
				t.Errorf("status fp = %s, want %s", req.InstanceFP, localFP())
			}
			f.mu.Lock()
			code, status, hook := f.code, f.status, f.onStatus
			f.mu.Unlock()
			if hook != nil {
				hook()
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
		case "/api/licenses/activate":
			p := validPayload()
			p["hw_fingerprint"] = req.InstanceFP
			_ = json.NewEncoder(w).Encode(map[string]string{"license": signLic(t, priv, p)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeIssuer) answer(code int, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code, f.status = code, status
}

// boundService installs an activated professional certificate on an online
// deployment pointed at the fake issuer.
func boundService(t *testing.T) (*Service, *fakeIssuer, *gorm.DB, map[string]ed25519.PublicKey) {
	t.Helper()
	keyTable, priv := testKeys(t)
	f, srv := newFakeIssuer(t, priv)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{ServerURL: srv.URL}, keyTable)
	p := validPayload()
	p["hw_fingerprint"] = localFP()
	if _, actErr, err := svc.Import(t.Context(), signLic(t, priv, p)); err != nil || actErr != nil {
		t.Fatalf("import: err=%v actErr=%v", err, actErr)
	}
	if !svc.InForce() || !svc.Activated() {
		t.Fatal("precondition: an activated certificate must be in force")
	}
	return svc, f, db, keyTable
}

func assertLicensed(t *testing.T, svc *Service) {
	t.Helper()
	if got := svc.Binding(); got != "" {
		t.Fatalf("Binding() = %q, want empty", got)
	}
	if !svc.InForce() || !svc.Activated() {
		t.Fatalf("certificate must stay in force: state=%s activated=%v", svc.State().State, svc.Activated())
	}
	if !Gate(svc).Snapshot().Licensed {
		t.Fatal("gate must still grant the paid tier")
	}
	if _, _, err := svc.Current(); err != nil {
		t.Fatalf("Current() = %v, want the certificate", err)
	}
}

func assertTakenBack(t *testing.T, svc *Service, want string) {
	t.Helper()
	if got := svc.Binding(); got != want {
		t.Fatalf("Binding() = %q, want %q", got, want)
	}
	if st := svc.State(); st.State != licverify.StateUnlicensed || st.Payload == nil {
		t.Fatalf("State() = %s (payload %v), want unlicensed with the payload kept", st.State, st.Payload != nil)
	}
	if svc.InForce() || svc.Activated() {
		t.Fatal("a certificate the issuer took back must grant nothing and read as not activated")
	}
	if g := Gate(svc).Snapshot(); g.Licensed || g.Edition != licverify.EditionCommunity {
		t.Fatalf("gate = %+v, want community", g)
	}
	// Modules verify the text themselves; withholding it is what downgrades them.
	if _, _, err := svc.Current(); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("Current() = %v, want ErrNoLicense", err)
	}
}

// #1782: after an issuer-side unbind the cluster must stop granting the paid
// tier instead of running on until the certificate expires.
func TestCheckBindingUnboundDowngrades(t *testing.T) {
	svc, f, db, keyTable := boundService(t)

	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatalf("check while bound: %v", err)
	}
	assertLicensed(t, svc)

	f.answer(http.StatusOK, BindingUnbound)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatalf("check after unbind: %v", err)
	}
	assertTakenBack(t, svc, BindingUnbound)

	var n int64
	db.Model(&model.AuditLog{}).Where("action = ?", "license.binding-change").Count(&n)
	if n != 1 {
		t.Fatalf("binding-change audit rows = %d, want 1", n)
	}

	// Survives a restart: the verdict lives in the row, not in memory.
	restarted := newTestService(t, db, config.LicenseConfig{ServerURL: svc.serverURL}, keyTable)
	assertTakenBack(t, restarted, BindingUnbound)
}

func TestCheckBindingRevoked(t *testing.T) {
	svc, f, _, _ := boundService(t)
	f.answer(http.StatusOK, BindingRevoked)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatalf("check: %v", err)
	}
	assertTakenBack(t, svc, BindingRevoked)
}

// Anything short of a definitive answer changes nothing, in either direction:
// an issuer outage must not downgrade a bound cluster, nor restore one it
// already took back.
func TestCheckBindingNoAnswerKeepsState(t *testing.T) {
	failures := []struct {
		name   string
		code   int
		status string
	}{
		{"server error", http.StatusInternalServerError, ""},
		{"older issuer without the endpoint", http.StatusNotFound, ""},
		{"rate limited", http.StatusTooManyRequests, ""},
		{"unknown answer", http.StatusOK, "maybe"},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			svc, f, _, _ := boundService(t)
			f.answer(tc.code, tc.status)
			if err := svc.CheckBinding(t.Context()); err == nil {
				t.Fatal("a non-answer must come back as an error")
			}
			assertLicensed(t, svc)

			f.answer(http.StatusOK, BindingUnbound)
			if err := svc.CheckBinding(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.answer(tc.code, tc.status)
			if err := svc.CheckBinding(t.Context()); err == nil {
				t.Fatal("a non-answer must come back as an error")
			}
			assertTakenBack(t, svc, BindingUnbound)
		})
	}

	t.Run("issuer unreachable", func(t *testing.T) {
		keyTable, priv := testKeys(t)
		_, srv := newFakeIssuer(t, priv)
		db := testDB(t)
		svc := newTestService(t, db, config.LicenseConfig{ServerURL: srv.URL}, keyTable)
		p := validPayload()
		p["hw_fingerprint"] = localFP()
		if _, _, err := svc.Import(t.Context(), signLic(t, priv, p)); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		if err := svc.CheckBinding(t.Context()); err == nil {
			t.Fatal("unreachable issuer must be an error")
		}
		assertLicensed(t, svc)
	})
}

// Re-activating (an accidental unbind, or the issuer rebinding this cluster)
// stores a fresh certificate and with it clears the verdict.
func TestReactivateClearsBinding(t *testing.T) {
	svc, f, _, _ := boundService(t)
	f.answer(http.StatusOK, BindingUnbound)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Activate(t.Context()); err != nil {
		t.Fatalf("re-activate: %v", err)
	}
	assertLicensed(t, svc)

	// And a later "bound" also clears it, e.g. rebound through the portal.
	f.answer(http.StatusOK, BindingUnbound)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.answer(http.StatusOK, BindingBound)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertLicensed(t, svc)
}

// A verdict about one certificate must not land on the one that replaced it
// while the check was in flight.
func TestCheckBindingIgnoresAnswerForReplacedText(t *testing.T) {
	svc, f, _, _ := boundService(t)
	f.mu.Lock()
	f.status = BindingUnbound
	f.onStatus = func() {
		p := validPayload()
		p["lic_id"] = "lic-replaced"
		p["hw_fingerprint"] = localFP()
		if err := svc.storeText(signLic(t, f.priv, p)); err != nil {
			t.Errorf("store replacement: %v", err)
		}
	}
	f.mu.Unlock()

	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc.guard.Refresh()
	assertLicensed(t, svc)
}

func TestCheckBindingThrottle(t *testing.T) {
	svc, f, _, _ := boundService(t)
	svc.CheckBindingIfStale(t.Context())
	svc.CheckBindingIfStale(t.Context())
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("issuer calls = %d, want 1 within the throttle window", got)
	}
	// The periodic path uses the long interval: a fresh check is not redone.
	svc.checkBindingOlderThan(t.Context(), bindingCheckInterval, time.Second)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("issuer calls = %d, want 1 inside the periodic interval", got)
	}
}

// Replicas share one row: a verdict written by one pod reaches another the
// next time that pod reads it, without a restart.
func TestBindingVisibleToOtherReplica(t *testing.T) {
	svc, f, db, keyTable := boundService(t)
	other := newTestService(t, db, config.LicenseConfig{ServerURL: svc.serverURL}, keyTable)
	assertLicensed(t, other)

	f.answer(http.StatusOK, BindingUnbound)
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The other pod's page-open path is throttled (just checked) but re-reads.
	other.CheckBindingIfStale(t.Context())
	assertTakenBack(t, other, BindingUnbound)
}

func TestCheckBindingOfflineAndUnactivated(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	offline := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if err := offline.CheckBinding(t.Context()); !errors.Is(err, ErrOfflineDeployment) {
		t.Fatalf("offline: got %v, want ErrOfflineDeployment", err)
	}

	// An unactivated certificate is not asked about at all.
	f, srv := newFakeIssuer(t, priv)
	f.answer(http.StatusOK, BindingUnbound)
	db2 := testDB(t)
	svc := newTestService(t, db2, config.LicenseConfig{ServerURL: srv.URL}, keyTable)
	if err := svc.storeText(signLic(t, priv, validPayload())); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckBinding(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 || svc.Binding() != "" {
		t.Fatal("an unactivated certificate must not be checked or flagged")
	}
}
