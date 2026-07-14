// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"bkn-safe/config"
	"bkn-safe/internal/audit"
	"bkn-safe/internal/database"
	"bkn-safe/internal/model"
)

const testInstanceID = "test-cluster-uid"

// testKeys returns a fresh self-signed test key table (kid "test") plus the
// private key for signing test licenses. Never resembles the official table.
func testKeys(t *testing.T) (map[string]ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]ed25519.PublicKey{"test": pub}, priv
}

// signLic builds a signed v1 license from a payload map (format identical to
// production certificates).
func signLic(t *testing.T, priv ed25519.PrivateKey, p map[string]any) string {
	t.Helper()
	if _, ok := p["kid"]; !ok {
		p["kid"] = "test"
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, b)
	return "v1." + base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestService(t *testing.T, db *gorm.DB, cfg config.LicenseConfig, keyTable map[string]ed25519.PublicKey) *Service {
	t.Helper()
	t.Setenv(licverify.EnvInstanceID, testInstanceID)
	svc, err := NewWithKeyTable(db, cfg, audit.New(db), keyTable)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func localFP() string { return licverify.FingerprintFrom("env:" + testInstanceID) }

// payload builders ------------------------------------------------------------

func validPayload() map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"lic_id":              "lic-valid",
		"edition":             "professional",
		"customer":            map[string]string{"name": "acme", "project": "p1"},
		"issued_at":           now - 3600,
		"expires_at":          now + 90*86400,
		"contract_expires_at": now + 365*86400,
		"features":            []string{"rbac_basic", "source_sync"},
		"limits":              map[string]int64{"max_users": 100},
	}
}

func TestStateMachine(t *testing.T) {
	keyTable, priv := testKeys(t)
	now := time.Now().Unix()

	cases := []struct {
		name  string
		mut   func(p map[string]any)
		state licverify.State
	}{
		{"valid", func(p map[string]any) {}, licverify.StateValid},
		{"perpetual community", func(p map[string]any) {
			p["expires_at"] = 0
			p["contract_expires_at"] = 0
			p["edition"] = "community"
		}, licverify.StateValid},
		{"grace within 30d", func(p map[string]any) {
			p["issued_at"] = now - 100*86400
			p["expires_at"] = now - 10*86400
		}, licverify.StateGrace},
		{"fallback beyond grace", func(p map[string]any) {
			p["issued_at"] = now - 200*86400
			p["expires_at"] = now - 40*86400
			p["contract_expires_at"] = 0
		}, licverify.StateFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
			p := validPayload()
			tc.mut(p)
			snap, actErr, err := svc.Import(t.Context(), signLic(t, priv, p))
			if err != nil || actErr != nil {
				t.Fatalf("import: err=%v actErr=%v", err, actErr)
			}
			if snap.State != tc.state {
				t.Fatalf("state = %s, want %s", snap.State, tc.state)
			}
		})
	}
}

func TestImportRejectsBadLicense(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)

	for name, text := range map[string]string{
		"garbage":     "not-a-license",
		"tampered":    signLic(t, priv, validPayload())[:40] + "x" + signLic(t, priv, validPayload())[41:],
		"unknown kid": signLic(t, priv, func() map[string]any { p := validPayload(); p["kid"] = "rogue"; return p }()),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.Import(t.Context(), text)
			if !errors.Is(err, ErrBadLicense) {
				t.Fatalf("err = %v, want ErrBadLicense", err)
			}
		})
	}
	if _, _, err := svc.Current(); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("rejected imports must not store: %v", err)
	}
}

func TestImportRejectsForeignBoundLicense(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)

	p := validPayload()
	p["hw_fingerprint"] = "fp_deadbeefdeadbeef" // someone else's machine
	_, _, err := svc.Import(t.Context(), signLic(t, priv, p))
	if !errors.Is(err, ErrBoundElsewhere) {
		t.Fatalf("err = %v, want ErrBoundElsewhere", err)
	}
}

func TestImportOfflineUnboundStaysUnactivated(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)

	snap, actErr, err := svc.Import(t.Context(), signLic(t, priv, validPayload()))
	if err != nil || actErr != nil {
		t.Fatalf("import: err=%v actErr=%v", err, actErr)
	}
	if snap.State != licverify.StateValid {
		t.Fatalf("state = %s", snap.State)
	}
	if svc.Activated() {
		t.Fatal("offline import of an unbound license must stay unactivated")
	}
}

func TestImportAutoActivatesOnline(t *testing.T) {
	keyTable, priv := testKeys(t)

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/licenses/activate" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			License    string `json:"license"`
			InstanceFP string `json:"instance_fp"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.InstanceFP != localFP() {
			t.Errorf("activate fp = %s, want %s", req.InstanceFP, localFP())
		}
		p := validPayload()
		p["hw_fingerprint"] = req.InstanceFP
		_ = json.NewEncoder(w).Encode(map[string]string{"license": signLic(t, priv, p)})
	}))
	defer issuer.Close()

	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{ServerURL: issuer.URL}, keyTable)
	snap, actErr, err := svc.Import(t.Context(), signLic(t, priv, validPayload()))
	if err != nil || actErr != nil {
		t.Fatalf("import: err=%v actErr=%v", err, actErr)
	}
	if snap.Payload == nil || snap.Payload.HWFingerprint != localFP() {
		t.Fatal("import on an online deployment must store the reissued, bound license")
	}
	if !svc.Activated() {
		t.Fatal("Activated() = false after auto-activation")
	}
}

func TestImportSurfacesActivationConflict(t *testing.T) {
	keyTable, priv := testKeys(t)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "already activated"})
	}))
	defer issuer.Close()

	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{ServerURL: issuer.URL}, keyTable)
	_, actErr, err := svc.Import(t.Context(), signLic(t, priv, validPayload()))
	if err != nil {
		t.Fatalf("import err = %v", err)
	}
	if !errors.Is(actErr, ErrActivatedElsewhere) {
		t.Fatalf("actErr = %v, want ErrActivatedElsewhere", actErr)
	}
	// The license is stored regardless — the import must not be lost.
	if _, _, err := svc.Current(); err != nil {
		t.Fatalf("license must be stored despite activation conflict: %v", err)
	}
}

func TestActivateOfflineRefused(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if _, _, err := svc.Import(t.Context(), signLic(t, priv, validPayload())); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Activate(t.Context()); !errors.Is(err, ErrOfflineDeployment) {
		t.Fatalf("err = %v, want ErrOfflineDeployment", err)
	}
}

func TestReceiptFingerprintMismatchRejected(t *testing.T) {
	// The offline receipt flow imports a bound license; a receipt for another
	// machine (copied) must be rejected — same guardrail as import.
	TestImportRejectsForeignBoundLicense(t)
}

func TestRenewReplacesLicenseAndETag(t *testing.T) {
	keyTable, priv := testKeys(t)
	now := time.Now().Unix()

	// Bound license deep inside the last third of its window: issued 90 days
	// ago, 10 days left of a 100-day window.
	old := validPayload()
	old["issued_at"] = now - 90*86400
	old["expires_at"] = now + 10*86400
	old["hw_fingerprint"] = localFP()

	fresh := validPayload()
	fresh["lic_id"] = "lic-renewed"
	fresh["hw_fingerprint"] = localFP()

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/licenses/renew" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"license": signLic(t, priv, fresh)})
	}))
	defer issuer.Close()

	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{ServerURL: issuer.URL}, keyTable)
	if err := svc.storeText(signLic(t, priv, old)); err != nil {
		t.Fatal(err)
	}
	_, before, err := svc.Current()
	if err != nil {
		t.Fatal(err)
	}

	snap := svc.RenewNow()
	if snap.RenewErr != nil {
		t.Fatalf("renew err: %v", snap.RenewErr)
	}
	if snap.Payload == nil || snap.Payload.LicID != "lic-renewed" {
		t.Fatalf("payload after renew = %+v", snap.Payload)
	}
	_, after, err := svc.Current()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("ETag must change when the license text changes")
	}
}

func TestRenewFailureKeepsStateAndAudits(t *testing.T) {
	keyTable, priv := testKeys(t)
	now := time.Now().Unix()

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "issuer down"})
	}))
	issuer.Close() // unreachable: the pulled-network-cable case

	old := validPayload()
	old["issued_at"] = now - 90*86400
	old["expires_at"] = now + 10*86400
	old["hw_fingerprint"] = localFP()

	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{ServerURL: issuer.URL}, keyTable)
	if err := svc.storeText(signLic(t, priv, old)); err != nil {
		t.Fatal(err)
	}

	snap := svc.RenewNow()
	if snap.State != licverify.StateValid {
		t.Fatalf("a failed renew must not degrade state: %s", snap.State)
	}
	if snap.RenewErr == nil {
		t.Fatal("RenewErr must surface the failure")
	}
	var n int64
	db.Model(&model.AuditLog{}).Where("action = ?", "license.renew-failed").Count(&n)
	if n != 1 {
		t.Fatalf("renew failure audit rows = %d, want 1", n)
	}
	// A second identical failure is logged, not re-audited (no hourly spam).
	svc.RenewNow()
	db.Model(&model.AuditLog{}).Where("action = ?", "license.renew-failed").Count(&n)
	if n != 1 {
		t.Fatalf("repeat failure re-audited: rows = %d", n)
	}
}

func TestRestartRecovery(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
	p := validPayload()
	p["hw_fingerprint"] = localFP()
	if _, _, err := svc.Import(t.Context(), signLic(t, priv, p)); err != nil {
		t.Fatal(err)
	}

	// "Restart": a fresh service over the same DB must come up licensed.
	svc2 := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if snap := svc2.State(); snap.State != licverify.StateValid {
		t.Fatalf("state after restart = %s", snap.State)
	}
	if !svc2.Activated() {
		t.Fatal("activation must survive a restart")
	}
}

func TestRemove(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if _, _, err := svc.Import(t.Context(), signLic(t, priv, validPayload())); err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snap := svc.State(); snap.State != licverify.StateInvalid {
		t.Fatalf("state after remove = %s", snap.State)
	}
	if _, _, err := svc.Current(); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("current after remove: %v", err)
	}
}

func TestActivationCodeWithoutLicense(t *testing.T) {
	keyTable, _ := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)

	fp, code, licID := svc.ActivationCode()
	if fp != localFP() {
		t.Fatalf("fp = %s", fp)
	}
	if licID != "" {
		t.Fatalf("licID with no license = %q", licID)
	}
	raw, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["instance_fp"] != localFP() {
		t.Fatalf("code fp = %s", decoded["instance_fp"])
	}
}

func TestClockHighWater(t *testing.T) {
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if _, _, err := svc.Import(t.Context(), signLic(t, priv, validPayload())); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	svc.checkClock(now)
	var row model.License
	if err := db.First(&row, "id = ?", rowID).Error; err != nil {
		t.Fatal(err)
	}
	if row.HighWater != now.Unix() {
		t.Fatalf("high water = %d, want %d", row.HighWater, now.Unix())
	}

	// A 48h rollback (beyond the 24h tolerance) is audited and never lowers
	// the mark.
	svc.checkClock(now.Add(-48 * time.Hour))
	var n int64
	db.Model(&model.AuditLog{}).Where("action = ?", "license.clock-rollback").Count(&n)
	if n != 1 {
		t.Fatalf("rollback audit rows = %d, want 1", n)
	}
	db.First(&row, "id = ?", rowID)
	if row.HighWater != now.Unix() {
		t.Fatal("rollback must not lower the high-water mark")
	}

	// Small skew within tolerance: no audit.
	svc.checkClock(now.Add(-time.Hour))
	db.Model(&model.AuditLog{}).Where("action = ?", "license.clock-rollback").Count(&n)
	if n != 1 {
		t.Fatalf("in-tolerance skew audited: rows = %d", n)
	}
}

func TestExternalWriterVisibleAfterRefresh(t *testing.T) {
	// Another replica renews (writes the row); this replica's next evaluation
	// must pick the fresh text up from the DB, not a cached copy.
	keyTable, priv := testKeys(t)
	db := testDB(t)
	svc := newTestService(t, db, config.LicenseConfig{}, keyTable)
	if _, _, err := svc.Import(t.Context(), signLic(t, priv, validPayload())); err != nil {
		t.Fatal(err)
	}

	fresh := validPayload()
	fresh["lic_id"] = "lic-other-replica"
	db.Model(&model.License{}).Where("id = ?", rowID).
		Updates(map[string]any{"text": signLic(t, priv, fresh), "version": gorm.Expr("version + 1")})

	snap := svc.RenewNow() // an hourly tick re-loads from the DB
	if snap.Payload == nil || snap.Payload.LicID != "lic-other-replica" {
		t.Fatalf("payload = %+v, want the other replica's license", snap.Payload)
	}
}
