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
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
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

func TestLicenseActivationCodeWithoutLicense(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	w := adminReq(t, r, http.MethodGet, licAdminBase+"/activation-code", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activation-code = %d", w.Code)
	}
	var resp struct {
		InstanceFP     string `json:"instance_fp"`
		ActivationCode string `json:"activation_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.InstanceFP == "" || resp.ActivationCode == "" {
		t.Fatalf("both fingerprint and code must be present: %s", w.Body.String())
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
	if detail.State != string(licverify.StateInvalid) {
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

func issueAppKey(t *testing.T, db *gorm.DB) string {
	t.Helper()
	// Verification resolves the key to its owner, so the owner must exist.
	if err := db.Create(&model.User{ID: "svc-module", Account: "svc-module", Name: "module", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, _, err := auth.NewAPIKeyStore(db).Issue(t.Context(), "svc-module", "module key", nil)
	if err != nil {
		t.Fatal(err)
	}
	return plaintext
}

func TestLicenseInternalRequiresAppKey(t *testing.T) {
	r, _, _ := newLicenseServer(t)
	for _, tok := range []string{"", "bak_bogus_bogus"} {
		w := tokReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/status", nil, tok)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("token %q = %d, want 401", tok, w.Code)
		}
	}
}

func TestLicenseInternalCurrentAndETag(t *testing.T) {
	r, db, priv := newLicenseServer(t)
	key := issueAppKey(t, db)

	// No license yet: 404.
	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", nil, key)
	if w.Code != http.StatusNotFound {
		t.Fatalf("current with no license = %d, want 404", w.Code)
	}

	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})

	w = tokReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", nil, key)
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
	req := tokReq2(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", key, etag)
	if req.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match = %d, want 304", req.Code)
	}

	// Re-import (a "renewal"): ETag changes and the poll misses.
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, func(p map[string]any) { p["lic_id"] = "lic-renewed" })})
	req = tokReq2(t, r, http.MethodGet, "/api/safe/v1/internal/license/current", key, etag)
	if req.Code != http.StatusOK {
		t.Fatalf("post-renewal poll = %d, want 200", req.Code)
	}
	if req.Header().Get("ETag") == etag {
		t.Fatal("ETag must change when the license changes")
	}
}

func TestLicenseInternalStatusAndCapabilities(t *testing.T) {
	r, db, priv := newLicenseServer(t)
	key := issueAppKey(t, db)
	adminReq(t, r, http.MethodPost, licAdminBase+"/import",
		map[string]string{"license": signTestLic(t, priv, nil)})

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/status", nil, key)
	var st struct {
		State     string `json:"state"`
		Edition   string `json:"edition"`
		ExpiresAt int64  `json:"expires_at"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.State != string(licverify.StateValid) || st.Edition != "professional" || st.ExpiresAt == 0 {
		t.Fatalf("status = %s", w.Body.String())
	}

	w = tokReq(t, r, http.MethodGet, "/api/safe/v1/internal/license/capabilities", nil, key)
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

// tokReq2 issues an authenticated request with an If-None-Match header.
func tokReq2(t *testing.T, r *gin.Engine, method, path, token, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
