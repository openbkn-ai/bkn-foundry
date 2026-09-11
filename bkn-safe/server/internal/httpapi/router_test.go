// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func newTestServer(t *testing.T) (*gin.Engine, *authz.Enforcer, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
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
	r := New(Deps{Enforcer: e, DB: db, Directory: directory.New(db)})
	return r, e, db
}

func doWithCallerService(t *testing.T, r *gin.Engine, method, path string, body any, caller string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-caller-service", caller)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedEnabledUser(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	if err := db.Create(&model.User{ID: id, Account: id, Enabled: true}).Error; err != nil {
		t.Fatalf("create enabled user %s: %v", id, err)
	}
}

func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHealth(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := do(t, r, http.MethodGet, "/health/ready", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health = %d", w.Code)
	}
}

func TestAuthzCheckEndpoint(t *testing.T) {
	r, e, db := newTestServer(t)
	// grant app-admin agent:* use, bind user.
	const role, user = "role-app", "u-1"
	seedEnabledUser(t, db, user)
	_ = e.GrantRolePermission(role, "agent", "*", "use")
	_ = e.AssignRole(user, role)

	body := map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "agent", "id": "probe"},
		"operation":   "use",
	}
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", body)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Allowed bool `json:"allowed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Allowed {
		t.Error("expected allowed=true")
	}

	// a denied op
	body["operation"] = "delete"
	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", body)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Allowed {
		t.Error("expected allowed=false for delete")
	}
}

func TestAuthzEndpointsApplyDenyWithoutChangingBusinessRequests(t *testing.T) {
	r, e, db := newTestServer(t)
	const user, role = "alice", "reader-role"
	seedEnabledUser(t, db, user)
	if err := db.Create(&model.Operation{ResourceTypeID: "resource", ID: "view_detail", Name: "view_detail"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission(role, "resource", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole(user, role); err != nil {
		t.Fatal(err)
	}
	if err := e.DenyObjectPermission(user, "resource", "r-1", "view_detail"); err != nil {
		t.Fatal(err)
	}

	check := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": user,
		"resource":    map[string]string{"type": "resource", "id": "r-1"},
		"operation":   "view_detail",
	})
	var decision struct {
		Allowed bool `json:"allowed"`
	}
	if check.Code != http.StatusOK || json.Unmarshal(check.Body.Bytes(), &decision) != nil || decision.Allowed {
		t.Fatalf("deny check response = %d %s", check.Code, check.Body.String())
	}

	filter := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           user,
		"resource_type":         "resource",
		"resource_ids":          []string{"r-1", "r-2"},
		"visibility_operations": []string{"view_detail"},
		"candidate_operations":  []string{"view_detail"},
	})
	var filtered struct {
		Resources []struct {
			ID string `json:"resource_id"`
		} `json:"resources"`
	}
	if filter.Code != http.StatusOK || json.Unmarshal(filter.Body.Bytes(), &filtered) != nil {
		t.Fatalf("deny filter response = %d %s", filter.Code, filter.Body.String())
	}
	if len(filtered.Resources) != 1 || filtered.Resources[0].ID != "r-2" {
		t.Fatalf("deny filter resources = %+v, want only r-2", filtered.Resources)
	}
}

func TestDirectoryNamesEndpoint(t *testing.T) {
	r, _, db := newTestServer(t)
	db.Create(&model.User{ID: "u1", Account: "alice", Name: "Alice", Enabled: true})

	w := do(t, r, http.MethodPost, "/api/safe/v1/directory/names",
		map[string]any{"user_ids": []string{"u1", "ghost"}})
	if w.Code != http.StatusOK {
		t.Fatalf("names = %d", w.Code)
	}
	var resp struct {
		UserNames []directory.NamedRef `json:"user_names"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.UserNames) != 1 || resp.UserNames[0].Name != "Alice" {
		t.Errorf("user_names = %v", resp.UserNames)
	}
}

func TestSelfServiceChangePassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
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
	users := auth.NewUserStore(db)
	ctx := t.Context()
	u := &model.User{ID: "u-cp", Account: "erin", Enabled: true, MustChangePassword: true}
	if err := users.CreateLocalUser(ctx, u, "initial0"); err != nil {
		t.Fatal(err)
	}
	r := New(Deps{Enforcer: e, DB: db, Users: users})
	const path = "/api/safe/v1/auth/change-password"

	// wrong old password -> 401
	w := do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "nope", "new_password": "brandnew1"})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong old: want 401, got %d (%s)", w.Code, w.Body.String())
	}
	// new == old -> 400
	w = do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "initial0", "new_password": "initial0"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("new==old: want 400, got %d", w.Code)
	}
	// success -> 204, new password works, flag cleared
	w = do(t, r, http.MethodPost, path, map[string]string{"account": "erin", "old_password": "initial0", "new_password": "brandnew1"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("change: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	got, err := users.Verify(ctx, "erin", "brandnew1")
	if err != nil {
		t.Fatalf("verify new: %v", err)
	}
	if got.MustChangePassword {
		t.Error("change-password must clear MustChangePassword")
	}
}

func TestAuthzBadRequest(t *testing.T) {
	r, _, _ := newTestServer(t)
	// missing required fields -> 400
	w := do(t, r, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{"accessor_id": "x"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestPolicyWriteWildcardGuard covers the stage-1 contract on the tokenless
// /authz/policies route: the two wildcard shapes that produce a policy matching
// every object are refused, and every shape a real service sends today still
// works. The second half is the compatibility evidence — 12 call sites across
// vega, bkn, the execution factory and the model factory depend on it.
func TestPolicyWriteWildcardGuard(t *testing.T) {
	r, e, _ := newTestServer(t)

	refused := []struct {
		name string
		body map[string]any
	}{
		{"wildcard type", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "*", "id": "*"},
			"operations":  []string{"view_detail"},
		}},
		{"wildcard operation", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "agent", "id": "a-1"},
			"operations":  []string{"*"},
		}},
		{"type-wide action execution", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "action_type", "id": "*"},
			"operations":  []string{"execute"},
		}},
		{"pattern-wide action execution", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "action_type", "id": "kn-1/*"},
			"operations":  []string{"execute"},
		}},
	}
	for _, c := range refused {
		t.Run("refuse "+c.name, func(t *testing.T) {
			if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", c.body); w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	t.Run("refuse wildcard type on delete", func(t *testing.T) {
		if w := do(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
			"resource": map[string]string{"type": "*", "id": "*"},
		}); w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Shapes that are logged but must NOT be refused in stage 1.
	allowed := []struct {
		name string
		body map[string]any
	}{
		{"ordinary creation grant", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "agent", "id": "a-1"},
			"operations":  []string{"use", "modify"},
		}},
		{"public accessor (built-in components)", map[string]any{
			"accessor_id": authz.PublicAccessorID,
			"resource":    map[string]string{"type": "tool_box", "id": "tb-1"},
			"operations":  []string{"execute"},
		}},
		{"type outside the seeded catalog (vega internal_*)", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "internal_resource", "id": "ir-1"},
			"operations":  []string{"view_detail"},
		}},
		{"empty operation list (execution factory no-op)", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "agent", "id": "a-2"},
			"operations":  []string{},
		}},
		{"wildcard resource id", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "agent", "id": "*"},
			"operations":  []string{"use"},
		}},
		{"concrete action execution", map[string]any{
			"accessor_id": "u-1",
			"resource":    map[string]string{"type": "action_type", "id": "kn-1/action-1"},
			"operations":  []string{"execute"},
		}},
	}
	for _, c := range allowed {
		t.Run("allow "+c.name, func(t *testing.T) {
			if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", c.body); w.Code != http.StatusNoContent {
				t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// The grants that were accepted must actually be in force.
	ok, err := e.Check("u-1", "agent", "a-1", "use")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("ordinary creation grant did not take effect")
	}
	ok, err = e.Check("someone-else", "tool_box", "tb-1", "execute")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("public-accessor grant should reach every requester")
	}
}

func TestBKNCreationPoliciesUseTrustedLifecycleSources(t *testing.T) {
	r, e, _ := newTestServer(t)

	knBody := map[string]any{
		"accessor_id":      "creator-1",
		"resource":         map[string]string{"type": "knowledge_network", "id": "kn-1"},
		"operations":       []string{authz.ActFullBusinessAccess, "authorize"},
		"policy_source":    authz.PolicySourceProfessionalRule,
		"authority_source": authz.AuthoritySourceOwnerDelegate,
	}
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", knBody); w.Code != http.StatusNoContent {
		t.Fatalf("create knowledge-network policies: want 204, got %d: %s", w.Code, w.Body.String())
	}
	records, err := e.PolicyRecords(authz.PolicyFilter{AccessorID: "creator-1", Object: "knowledge_network:kn-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("knowledge-network records = %#v, want bundle and authorize", records)
	}
	byOperation := make(map[string]authz.PolicyRecord, len(records))
	for _, record := range records {
		byOperation[record.Operation] = record
	}
	bundle := byOperation[authz.ActFullBusinessAccess]
	if bundle.PolicySource != authz.PolicySourceCommunityBundle || bundle.AuthoritySource != authz.AuthoritySourceSystem {
		t.Fatalf("bundle provenance = (%q, %q)", bundle.PolicySource, bundle.AuthoritySource)
	}
	authorize := byOperation["authorize"]
	if authorize.PolicySource != authz.PolicySourceSystemDerived || authorize.AuthoritySource != authz.AuthoritySourceSystem {
		t.Fatalf("authorize provenance = (%q, %q)", authorize.PolicySource, authorize.AuthoritySource)
	}
	for _, operation := range []string{"view_detail", "modify", "delete", "query_data", "execute", "authorize"} {
		if allowed, checkErr := e.Check("creator-1", "knowledge_network", "kn-1", operation); checkErr != nil || !allowed {
			t.Fatalf("creator %s = %v, %v; want allowed", operation, allowed, checkErr)
		}
	}
	if allowed, checkErr := e.Check("creator-1", "knowledge_network", "kn-1", "task_manage"); checkErr != nil || allowed {
		t.Fatalf("creator task_manage = %v, %v; want denied", allowed, checkErr)
	}

	actionBody := map[string]any{
		"accessor_id": "creator-1",
		"resource":    map[string]string{"type": "action_type", "id": "kn-1/action-1"},
		"operations":  []string{"execute"},
	}
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", actionBody); w.Code != http.StatusNoContent {
		t.Fatalf("create action-type policy: want 204, got %d: %s", w.Code, w.Body.String())
	}
	actionRecords, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID: "creator-1", Object: "action_type:kn-1/action-1", Operation: "execute",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actionRecords) != 1 || actionRecords[0].PolicySource != authz.PolicySourceSystemDerived ||
		actionRecords[0].AuthoritySource != authz.AuthoritySourceSystem {
		t.Fatalf("action execute records = %#v, want one system-derived grant", actionRecords)
	}

	// Existing callers may combine execute with other concrete action-type
	// operations. Keep those requests on the legacy compatibility path rather
	// than rejecting a shape accepted before lifecycle provenance was added.
	legacyActionBody := map[string]any{
		"accessor_id": "legacy-creator",
		"resource":    map[string]string{"type": "action_type", "id": "kn-1/action-2"},
		"operations":  []string{"view_detail", "execute"},
	}
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", legacyActionBody); w.Code != http.StatusNoContent {
		t.Fatalf("create legacy mixed action-type policies: want 204, got %d: %s", w.Code, w.Body.String())
	}
	for _, operation := range []string{"view_detail", "execute"} {
		records, recordsErr := e.PolicyRecords(authz.PolicyFilter{
			AccessorID: "legacy-creator", Object: "action_type:kn-1/action-2", Operation: operation,
		})
		if recordsErr != nil {
			t.Fatal(recordsErr)
		}
		if len(records) != 1 || records[0].PolicySource != authz.PolicySourceLegacy ||
			records[0].AuthoritySource != authz.AuthoritySourceMigration {
			t.Fatalf("legacy action %s records = %#v, want one legacy/migration grant", operation, records)
		}
	}

	knBody["operations"] = []string{authz.ActFullBusinessAccess}
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", knBody); w.Code != http.StatusBadRequest {
		t.Fatalf("partial lifecycle shape: want 400, got %d: %s", w.Code, w.Body.String())
	}
}
