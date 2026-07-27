// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build e2e

// End-to-end test against a REAL license-server (started locally by
// dev/license-e2e.sh — never a shared/public issuer). Covers what the unit
// suite fakes: true activation reissue, first-wins conflicts, renewal with
// binding checks. Skips unless LICENSE_E2E_ISSUER is set.
//
//	LICENSE_E2E_ISSUER       http://127.0.0.1:18341
//	LICENSE_E2E_COMMUNITY    path to an UNBOUND community .lic
//	LICENSE_E2E_PRO          path to an UNBOUND professional .lic
package license

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
)

const (
	e2eClusterA = "e2e-cluster-A"
	e2eClusterB = "e2e-cluster-B"
)

func e2eIssuer(t *testing.T) string {
	t.Helper()
	url := os.Getenv("LICENSE_E2E_ISSUER")
	if url == "" {
		t.Skip("LICENSE_E2E_ISSUER not set — run via dev/license-e2e.sh")
	}
	return url
}

// e2eKeys fetches the issuer's published verification keys (/api/keys). In
// production keys are compiled in; the e2e issuer generates a throwaway pair.
func e2eKeys(t *testing.T, issuer string) map[string]ed25519.PublicKey {
	t.Helper()
	resp, err := http.Get(issuer + "/api/keys")
	if err != nil {
		t.Fatalf("fetch keys: %v", err)
	}
	defer resp.Body.Close()
	var kv struct {
		Keys []struct {
			Kid       string `json:"kid"`
			PublicKey string `json:"public_key"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&kv); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	out := make(map[string]ed25519.PublicKey, len(kv.Keys))
	for _, k := range kv.Keys {
		pub, err := licverify.ParsePublicKey(k.PublicKey)
		if err != nil {
			t.Fatalf("key %s: %v", k.Kid, err)
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		t.Fatal("issuer published no keys")
	}
	return out
}

func e2eLic(t *testing.T, env string) string {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		t.Skipf("%s not set", env)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func e2eService(t *testing.T, issuer, instanceID string, keys map[string]ed25519.PublicKey) *Service {
	t.Helper()
	t.Setenv(licverify.EnvInstanceID, instanceID)
	db := testDB(t)
	svc, err := NewWithKeyTable(db, config.LicenseConfig{ServerURL: issuer}, audit.New(db), keys)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestE2EActivateBindsAndIsIdempotent(t *testing.T) {
	issuer := e2eIssuer(t)
	keys := e2eKeys(t, issuer)
	lic := e2eLic(t, "LICENSE_E2E_COMMUNITY")

	svc := e2eService(t, issuer, e2eClusterA, keys)
	snap, actErr, err := svc.Import(t.Context(), lic)
	if err != nil || actErr != nil {
		t.Fatalf("import: err=%v actErr=%v", err, actErr)
	}
	if snap.State != licverify.StateValid || !svc.Activated() {
		t.Fatalf("state=%s activated=%v after online import", snap.State, svc.Activated())
	}
	wantFP := licverify.FingerprintFrom("env:" + e2eClusterA)
	if snap.Payload.HWFingerprint != wantFP {
		t.Fatalf("bound fp = %s, want %s", snap.Payload.HWFingerprint, wantFP)
	}

	// Same instance re-imports the original unbound code (reinstall): the
	// issuer must treat it as idempotent, not first-wins it away.
	snap, actErr, err = svc.Import(t.Context(), lic)
	if err != nil || actErr != nil {
		t.Fatalf("re-import: err=%v actErr=%v", err, actErr)
	}
	if !svc.Activated() {
		t.Fatal("re-import lost the activation")
	}

	// Explicit re-activate is also idempotent.
	if _, err := svc.Activate(t.Context()); err != nil {
		t.Fatalf("re-activate: %v", err)
	}
}

func TestE2EFirstWinsAgainstSecondCluster(t *testing.T) {
	issuer := e2eIssuer(t)
	keys := e2eKeys(t, issuer)
	lic := e2eLic(t, "LICENSE_E2E_COMMUNITY") // already activated by cluster A above

	svcB := e2eService(t, issuer, e2eClusterB, keys)
	_, actErr, err := svcB.Import(t.Context(), lic)
	if err != nil {
		t.Fatalf("import on cluster B: %v", err)
	}
	if actErr == nil {
		t.Fatal("cluster B activating cluster A's license must conflict (first-wins)")
	}
}

func TestE2ECopiedBoundCertRejectedLocally(t *testing.T) {
	issuer := e2eIssuer(t)
	keys := e2eKeys(t, issuer)
	lic := e2eLic(t, "LICENSE_E2E_COMMUNITY")

	// Cluster A activates and holds the bound reissue.
	svcA := e2eService(t, issuer, e2eClusterA, keys)
	if _, actErr, err := svcA.Import(t.Context(), lic); err != nil || actErr != nil {
		t.Fatalf("cluster A import: err=%v actErr=%v", err, actErr)
	}
	bound, _, err := svcA.Current()
	if err != nil {
		t.Fatal(err)
	}

	// Cluster B copies the bound certificate: local verification alone must
	// reject it — no issuer round-trip involved.
	svcB := e2eService(t, issuer, e2eClusterB, keys)
	if _, _, err := svcB.Import(t.Context(), bound); err != ErrBoundElsewhere {
		t.Fatalf("copied bound cert: err=%v, want ErrBoundElsewhere", err)
	}
}

func TestE2ERenewChecksBinding(t *testing.T) {
	issuer := e2eIssuer(t)
	keys := e2eKeys(t, issuer)
	lic := e2eLic(t, "LICENSE_E2E_PRO")

	svc := e2eService(t, issuer, e2eClusterA, keys)
	if _, actErr, err := svc.Import(t.Context(), lic); err != nil || actErr != nil {
		t.Fatalf("import pro: err=%v actErr=%v", err, actErr)
	}
	bound, _, err := svc.Current()
	if err != nil {
		t.Fatal(err)
	}

	renew := func(text, fp string) (int, string) {
		body, _ := json.Marshal(map[string]string{"license": text, "instance_fp": fp})
		resp, err := http.Post(issuer+"/api/licenses/renew", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			License string `json:"license"`
			Error   string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.License
	}

	// Renew with the WRONG fingerprint: refused — a copied code cannot keep
	// itself alive through renewal.
	if code, _ := renew(bound, licverify.FingerprintFrom("env:"+e2eClusterB)); code != http.StatusConflict {
		t.Fatalf("renew with foreign fp = %d, want 409", code)
	}

	// Renew with the bound fingerprint: a fresh certificate that verifies and
	// carries the same binding.
	code, fresh := renew(bound, licverify.FingerprintFrom("env:"+e2eClusterA))
	if code != http.StatusOK || fresh == "" {
		t.Fatalf("renew = %d", code)
	}
	state, p := licverify.Eval(fresh, keys)
	if state != licverify.StateValid || p.HWFingerprint != licverify.FingerprintFrom("env:"+e2eClusterA) {
		t.Fatalf("renewed cert: state=%s fp=%s", state, p.HWFingerprint)
	}
	if p.LicID == "" {
		t.Fatal("renewed cert missing lic_id")
	}
}
