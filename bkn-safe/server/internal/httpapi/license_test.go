// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/licverify"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// newLicenseServer builds a full server with the license surfaces mounted: a
// self-signed test key table, the stub token verifier, and adminSub as
// super-admin (same trust setup as newAdminServer).
func newLicenseServer(t *testing.T) (*gin.Engine, *gorm.DB, ed25519.PrivateKey) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv(licverify.EnvInstanceID, "test-cluster-uid")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e, err := authz.New(db)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}
	if err := e.Grant(adminSub, "*", "*"); err != nil {
		t.Fatalf("grant super-admin: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := license.NewWithKeyTable(db, config.LicenseConfig{}, audit.New(db),
		map[string]ed25519.PublicKey{"test": pub})
	if err != nil {
		t.Fatalf("license service: %v", err)
	}
	// The same wiring app.Boot does. Without it the tier would stay community no
	// matter what certificate a test imports, and the capabilities endpoint —
	// which reads the gate, not the payload — would answer for a deployment
	// nobody is running.
	entitlement.SetGateForTest(license.Gate(svc))
	r := New(Deps{
		Enforcer: e, DB: db, Directory: directory.New(db), Users: auth.NewUserStore(db),
		Audit:         audit.New(db),
		TokenVerifier: stubVerifier{},
		License:       svc,
	})
	return r, db, priv
}

func signTestLic(t *testing.T, priv ed25519.PrivateKey, mut func(p map[string]any)) string {
	t.Helper()
	now := time.Now().Unix()
	p := map[string]any{
		"lic_id":              "lic-http",
		"kid":                 "test",
		"edition":             "professional",
		"customer":            map[string]string{"name": "acme"},
		"issued_at":           now - 3600,
		"expires_at":          now + 90*86400,
		"contract_expires_at": now + 365*86400,
		"features":            []string{"rbac_basic"},
		"limits":              map[string]int64{"max_users": 100},
	}
	if mut != nil {
		mut(p)
	}
	b, _ := json.Marshal(p)
	sig := ed25519.Sign(priv, b)
	return "v1." + base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const licAdminBase = "/api/safe/v1/admin/license"

func TestLicenseAdminRequiresToken(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	if w := do(t, r, http.MethodGet, licAdminBase, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin read = %d, want 401", w.Code)
	}
}

func TestLicenseFingerprintWithoutLicense(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	w := adminReq(t, r, http.MethodGet, licAdminBase+"/fingerprint", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("fingerprint = %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		InstanceFP string `json:"instance_fp"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.InstanceFP == "" {
		t.Fatal("machine code must be available before any license is imported")
	}
}

func TestLicenseActivationRequestWithoutLicense(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	w := adminReq(t, r, http.MethodGet, licAdminBase+"/activation-code", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activation-code = %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	fp, _ := resp["instance_fp"].(string)
	if fp == "" {
		t.Fatalf("the fingerprint is what the portal wants; it must be present: %s", w.Body.String())
	}
	if _, ok := resp["lic_id"]; !ok {
		t.Fatalf("lic_id must be present (empty with no license): %s", w.Body.String())
	}

	// The retired field must stay gone. The issuer validates ^fp_[0-9a-f]{16}$
	// and answers 400 to the old base64 blob, so re-adding it would hand an
	// admin a value that only fails.
	if _, ok := resp["activation_code"]; ok {
		t.Fatalf("activation_code came back; the portal no longer accepts it: %s", w.Body.String())
	}
}

func TestLicenseImportInvalidRejected(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	w := adminReq(t, r, http.MethodPost, licAdminBase+"/import", map[string]string{"license": "garbage"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("import garbage = %d, want 400", w.Code)
	}
}

func TestLicenseImportDetailAndRemove(t *testing.T) {
	r, _, priv := newLicenseServer(t)

	w := adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})
	if w.Code != http.StatusOK {
		t.Fatalf("import = %d (%s)", w.Code, w.Body.String())
	}

	w = adminReq(t, r, http.MethodGet, licAdminBase, nil)
	var detail struct {
		State      string   `json:"state"`
		Edition    string   `json:"edition"`
		Activated  bool     `json:"activated"`
		InstanceFP string   `json:"instance_fp"`
		LicID      string   `json:"lic_id"`
		Features   []string `json:"features"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.State != string(licverify.StateValid) || detail.Edition != "professional" ||
		detail.LicID != "lic-http" || len(detail.Features) != 1 || detail.InstanceFP == "" {
		t.Fatalf("detail = %s", w.Body.String())
	}
	if detail.Activated {
		t.Fatal("unbound license on an offline deployment must read activated=false")
	}

	if w = adminReq(t, r, http.MethodDelete, licAdminBase, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	w = adminReq(t, r, http.MethodGet, licAdminBase, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	// See TestRemove: a freshly created deployment with no license reads
	// "trial", not "invalid" — "invalid" now means a license that failed to
	// verify, which is a different thing to tell an admin.
	if detail.State != string(licverify.StateTrial) {
		t.Fatalf("state after remove = %s", detail.State)
	}
}

func TestLicenseReceiptFingerprintMismatch(t *testing.T) {
	r, _, priv := newLicenseServer(t)
	w := adminReq(t, r, http.MethodPost, licAdminBase+"/receipt",
		map[string]string{"license": signTestLic(t, priv, func(p map[string]any) {
			p["hw_fingerprint"] = "fp_deadbeefdeadbeef"
		})})
	if w.Code != http.StatusConflict {
		t.Fatalf("foreign-bound receipt = %d, want 409 (%s)", w.Code, w.Body.String())
	}
}

func TestLicenseActivateOffline(t *testing.T) {
	r, _, priv := newLicenseServer(t)
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})
	w := adminReq(t, r, http.MethodPost, licAdminBase+"/activate", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("activate with no issuer configured = %d, want 400", w.Code)
	}
}

// internal face ---------------------------------------------------------------

// TestLicenseInternalIsTokenless pins the trust face of the whole
// /internal/license group. It is the inverse of the assertion this test used to
// make, and it is kept as a test rather than a comment because the failure it
// guards against is someone re-fencing the surface with a service credential:
// every module that needs a licence would then be blocked again on an issuer, a
// rotation story and a revocation signal that do not exist. What travels here is
// signed text verified locally against a compiled-in key, so reading it confers
// no power to forge it — see registerLicenseInternal.
func TestLicenseInternalIsTokenless(t *testing.T) {
	r, _, priv := newLicenseServer(t)
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})

	for _, path := range []string{"/current", "/status", "/capabilities"} {
		w := do(t, r, http.MethodGet, "/api/safe/v1/internal/license"+path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("unauthenticated GET %s = %d, want 200 — the group is tokenless", path, w.Code)
		}
	}
}

func TestLicenseInternalCurrentAndETag(t *testing.T) {
	r, _, priv := newLicenseServer(t)

	// No license yet: 404 — the absence is a definite answer, not a 401.
	w := do(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("current with no license = %d, want 404", w.Code)
	}

	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})

	w = do(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("current = %d", w.Code)
	}
	etag := w.Header().Get("ETag")
	var resp struct {
		License string `json:"license"`
		ETag    string `json:"etag"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if etag == "" || resp.License == "" {
		t.Fatalf("current must carry ETag and text: %s", w.Body.String())
	}
	// The distributed text must verify locally — that is the whole point.
	if _, p := licverify.Eval(resp.License, map[string]ed25519.PublicKey{"test": priv.Public().(ed25519.PublicKey)}); p == nil {
		t.Fatal("distributed license text does not verify")
	}

	// Conditional poll: 304 without a body.
	req := condReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", etag)
	if req.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match = %d, want 304", req.Code)
	}

	// Re-import (a "renewal"): ETag changes and the poll misses.
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, func(p map[string]any) { p["lic_id"] = "lic-renewed" })})
	req = condReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", etag)
	if req.Code != http.StatusOK {
		t.Fatalf("post-renewal poll = %d, want 200", req.Code)
	}
	if req.Header().Get("ETag") == etag {
		t.Fatal("ETag must change when the license changes")
	}
}

func TestLicenseInternalStatusAndCapabilities(t *testing.T) {
	r, _, priv := newLicenseServer(t)
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})

	w := do(t, r, http.MethodGet, "/api/safe/v1/internal/license/status", nil)
	var st struct {
		State     string `json:"state"`
		Edition   string `json:"edition"`
		ExpiresAt int64  `json:"expires_at"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.State != string(licverify.StateValid) || st.Edition != "professional" || st.ExpiresAt == 0 {
		t.Fatalf("status = %s", w.Body.String())
	}

	w = do(t, r, http.MethodGet, "/api/safe/v1/internal/license/capabilities", nil)
	var caps struct {
		State    string           `json:"state"`
		Features []string         `json:"features"`
		Limits   map[string]int64 `json:"limits"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &caps)
	if len(caps.Features) != 1 || caps.Features[0] != "rbac_basic" || caps.Limits["max_users"] != 100 {
		t.Fatalf("capabilities = %s", w.Body.String())
	}
}

// condReq issues a conditional (If-None-Match) request. No credential: the
// distribution group is tokenless.
func condReq(t *testing.T, r *gin.Engine, method, path, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
