// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

// seedCatalogOps registers operation ids for a resource type so the
// object-grant op-validation has a catalog to check against.
func seedCatalogOps(t *testing.T, db *gorm.DB, resourceType string, ops ...string) {
	t.Helper()
	if err := db.FirstOrCreate(&model.ResourceType{ID: resourceType, Name: resourceType}).Error; err != nil {
		t.Fatalf("seed resource type %s: %v", resourceType, err)
	}
	for _, op := range ops {
		row := model.Operation{ResourceTypeID: resourceType, ID: op, Name: op}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed op %s/%s: %v", resourceType, op, err)
		}
	}
}

type ogEntry struct {
	AccessorID string `json:"accessor_id"`
	Resource   struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"resource"`
	Operations       []string `json:"operations"`
	DeniedOperations []string `json:"denied_operations"`
	Grants           []struct {
		GrantID         string `json:"grant_id"`
		Operation       string `json:"operation"`
		PolicySource    string `json:"policy_source"`
		AuthoritySource string `json:"authority_source"`
		Active          bool   `json:"active"`
		Inherited       bool   `json:"inherited"`
	} `json:"grants"`
}

func listObjectGrants(t *testing.T, r *gin.Engine, query string) []ogEntry {
	t.Helper()
	body := listObjectGrantsBody(t, r, query)
	return body.Entries
}

func listObjectGrantsBody(t *testing.T, r *gin.Engine, query string) struct {
	Entries []ogEntry `json:"entries"`
	Total   int       `json:"total"`
	Summary *struct {
		Grants   int `json:"grants"`
		Objects  int `json:"objects"`
		Grantees int `json:"grantees"`
	} `json:"summary"`
} {
	t.Helper()
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants"+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list grants: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Entries []ogEntry `json:"entries"`
		Total   int       `json:"total"`
		Summary *struct {
			Grants   int `json:"grants"`
			Objects  int `json:"objects"`
			Grantees int `json:"grantees"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grants: %v", err)
	}
	return body
}

func oneGrantID(t *testing.T, e *authz.Enforcer, filter authz.PolicyFilter) string {
	t.Helper()
	records, err := e.PolicyRecords(filter)
	if err != nil || len(records) != 1 {
		t.Fatalf("grant lookup = %+v, %v; want exactly one", records, err)
	}
	return records[0].GrantID
}

func TestObjectGrantsSetListRevoke(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	// set: grant u-1 two ops on catalog c1
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id":      "u-1",
		"resource":         map[string]any{"type": "catalog", "id": "c1"},
		"operations":       []string{"view_detail", "modify"},
		"policy_source":    "community_bundle",
		"authority_source": "owner_delegate",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "modify"); !ok {
		t.Fatal("grant did not take effect at enforce time")
	}
	// Provenance is derived by the trusted route. Unknown client fields cannot
	// forge either source dimension.
	records, err := e.PolicyRecords(authz.PolicyFilter{AccessorID: "u-1", Object: "catalog:c1"})
	if err != nil || len(records) != 2 {
		t.Fatalf("policy provenance = %+v, %v; want two server-derived rows", records, err)
	}
	for _, record := range records {
		if record.PolicySource != authz.PolicySourceProfessionalRule || record.AuthoritySource != authz.AuthoritySourceAdminAuthz {
			t.Fatalf("ordinary request forged policy provenance: %+v", record)
		}
	}

	// list (no filter) returns the grant
	entries := listObjectGrants(t, r, "")
	if len(entries) != 1 || entries[0].AccessorID != "u-1" || entries[0].Resource.ID != "c1" || len(entries[0].Operations) != 2 {
		t.Fatalf("unexpected list: %+v", entries)
	}
	if len(entries[0].Grants) != 2 || entries[0].Grants[0].GrantID == "" ||
		entries[0].Grants[0].PolicySource != string(authz.PolicySourceProfessionalRule) ||
		entries[0].Grants[0].AuthoritySource != string(authz.AuthoritySourceAdminAuthz) ||
		!entries[0].Grants[0].Active || entries[0].Grants[0].Inherited {
		t.Fatalf("direct grant metadata = %+v", entries[0].Grants)
	}
	// filtered lists
	if got := listObjectGrants(t, r, "?accessor_id=u-1"); len(got) != 1 {
		t.Fatalf("accessor filter: %+v", got)
	}
	if got := listObjectGrants(t, r, "?resource_type=catalog&resource_id=c1"); len(got) != 1 {
		t.Fatalf("resource filter: %+v", got)
	}
	if got := listObjectGrants(t, r, "?resource_id=other"); len(got) != 0 {
		t.Fatalf("resource filter (miss): %+v", got)
	}

	// set again with a smaller op set: replace semantics drop "modify"
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-1",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-grant: want 204, got %d", w.Code)
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "modify"); ok {
		t.Fatal("replace did not prune the dropped op")
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); !ok {
		t.Fatal("replace dropped the kept op")
	}

	// revoke
	grantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "u-1", Object: "catalog:c1", Operation: "view_detail",
		PolicySource: authz.PolicySourceProfessionalRule, AuthoritySource: authz.AuthoritySourceAdminAuthz,
	})
	w = adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{"grant_id": grantID})
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-1", "catalog", "c1", "view_detail"); ok {
		t.Fatal("revoke did not remove the grant")
	}
	if got := listObjectGrants(t, r, ""); len(got) != 0 {
		t.Fatalf("list after revoke: %+v", got)
	}
}

func TestObjectGrantsListHidesManagedProxyAccounts(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-visible", Account: "visible", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "resource", "query_data")
	if err := e.GrantObjectPermission("u-visible", "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}

	proxy, _, err := managedproxy.New(db).Create(ctx, managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-hidden-proxy-grant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission(proxy.ProxyAccountID, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProxyGrantSource{
		ID: "source-hidden-proxy-grant", ProxyAccountID: proxy.ProxyAccountID,
		ResourceType: "resource", ResourceID: "r-1", Operation: "query_data",
		SourceType: model.ProxyGrantSourceTypeManual, SourceID: "hidden-proxy-grant",
		LifecycleStatus: model.ProxyGrantSourceStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	body := listObjectGrantsBody(t, r, "?include_summary=true")
	if body.Total != 1 || len(body.Entries) != 1 || body.Entries[0].AccessorID != "u-visible" {
		t.Fatalf("flat object grants = total %d entries %+v, want only the visible user", body.Total, body.Entries)
	}
	if body.Summary == nil || body.Summary.Grants != 1 || body.Summary.Objects != 1 || body.Summary.Grantees != 1 {
		t.Fatalf("summary = %+v, want one visible user grant", body.Summary)
	}

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants?group_by=grantee", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("grouped grants = %d body=%s", w.Code, w.Body.String())
	}
	var grouped struct {
		Groups []struct {
			AccessorID  string `json:"accessor_id"`
			ObjectCount int64  `json:"object_count"`
		} `json:"groups"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &grouped); err != nil {
		t.Fatal(err)
	}
	if grouped.Total != 1 || len(grouped.Groups) != 1 || grouped.Groups[0].AccessorID != "u-visible" || grouped.Groups[0].ObjectCount != 1 {
		t.Fatalf("grouped grants = %+v, want only the visible user", grouped)
	}

	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants?group_by=object", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("grouped objects = %d body=%s", w.Code, w.Body.String())
	}
	var groupedObjects struct {
		Groups []struct {
			Object struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"object"`
			GranteeCount int64 `json:"grantee_count"`
		} `json:"groups"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &groupedObjects); err != nil {
		t.Fatal(err)
	}
	if groupedObjects.Total != 1 || len(groupedObjects.Groups) != 1 ||
		groupedObjects.Groups[0].Object.Type != "resource" || groupedObjects.Groups[0].Object.ID != "r-1" ||
		groupedObjects.Groups[0].GranteeCount != 1 {
		t.Fatalf("grouped objects = %+v, want one visible user on resource/r-1", groupedObjects)
	}
}

func TestObjectGrantAllowAtomicallyAddsDirectRequirements(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "required-user", Account: "required-user", Enabled: true,
	}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "catalog", "modify").
		Update("implied_operation_ids", "view_detail").Error; err != nil {
		t.Fatal(err)
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "required-user",
		"resource":    map[string]any{"type": "catalog", "id": "c-required"},
		"operations":  []string{"modify"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("normalized grant = %d %s", w.Code, w.Body.String())
	}
	records, err := e.PolicyRecords(authz.PolicyFilter{
		AccessorID: "required-user", Object: "catalog:c-required",
		PolicySource: authz.PolicySourceProfessionalRule,
	})
	if err != nil || len(records) != 2 {
		t.Fatalf("normalized records = %+v, %v; want modify and view_detail", records, err)
	}
	for _, operation := range []string{"modify", "view_detail"} {
		if allowed, err := e.Check("required-user", "catalog", "c-required", operation); err != nil || !allowed {
			t.Fatalf("normalized %s decision = %v, %v", operation, allowed, err)
		}
	}
}

func TestCommunityObjectGrantCompatibilityWriteIsImmediatelyEffective(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")
	if err := db.Create(&model.User{ID: "community-user", Account: "community-user", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "community-user",
		"resource":    map[string]any{"type": "catalog", "id": "c-community"},
		"bundle":      authz.ActFullBusinessAccess,
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("Community grant = %d %s; want 204", w.Code, w.Body.String())
	}
	for _, operation := range []string{"view_detail", "modify", "query_data", "resource_manage"} {
		allowed, err := e.Check("community-user", "catalog", "c-community", operation)
		if err != nil || !allowed {
			t.Fatalf("Community grant Check(%q) = %v, %v; want true", operation, allowed, err)
		}
	}
	grants := listObjectGrants(t, r, "?accessor_id=community-user&resource_type=catalog&resource_id=c-community")
	if len(grants) != 1 || len(grants[0].Operations) != 6 {
		t.Fatalf("Community grant listing = %+v; want one projected bundle grant", grants)
	}
}

func TestBundlePreviewAndLegacyRevokePreserveSiblingSource(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "legacy-user", Account: "legacy-user", Enabled: true,
	}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify", "delete", "query_data", "resource_manage", "task_manage")
	if err := e.GrantObjectPermission("legacy-user", "catalog", "c-legacy", "view_detail"); err != nil {
		t.Fatal(err)
	}
	legacyID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "legacy-user", Object: "catalog:c-legacy", Operation: "view_detail",
		PolicySource: authz.PolicySourceLegacy,
	})

	preview := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants/preview", map[string]any{
		"accessor_id": "legacy-user",
		"resource":    map[string]any{"type": "catalog", "id": "c-legacy"},
		"bundle":      authz.ActFullBusinessAccess,
	})
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), legacyID) ||
		strings.Contains(preview.Body.String(), `"added_operations":["view_detail"`) {
		t.Fatalf("bundle preview = %d %s", preview.Code, preview.Body.String())
	}

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "legacy-user",
		"resource":    map[string]any{"type": "catalog", "id": "c-legacy"},
		"bundle":      authz.ActFullBusinessAccess,
	}); w.Code != http.StatusNoContent {
		t.Fatalf("bundle grant = %d %s", w.Code, w.Body.String())
	}
	records, err := e.PolicyRecords(authz.PolicyFilter{AccessorID: "legacy-user", Object: "catalog:c-legacy"})
	if err != nil || len(records) != 2 {
		t.Fatalf("coexisting sources = %+v, %v", records, err)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"grant_id": legacyID,
	}); w.Code != http.StatusNoContent {
		t.Fatalf("legacy revoke = %d %s", w.Code, w.Body.String())
	}
	if allowed, err := e.Check("legacy-user", "catalog", "c-legacy", "view_detail"); err != nil || !allowed {
		t.Fatalf("bundle sibling after legacy revoke = %v, %v", allowed, err)
	}
}

func TestAdminObjectGrantDenyIsBackwardCompatible(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	if err := users.CreateLocalUser(t.Context(), &model.User{
		ID: "alice", Account: "alice-deny", Name: "Alice", Enabled: true,
	}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")
	const reader = "reader-role"
	if err := e.GrantRolePermission(reader, "catalog", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("alice", reader); err != nil {
		t.Fatal(err)
	}

	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "alice",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"view_detail"},
		"effect":      "deny",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("deny: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, err := e.Check("alice", "catalog", "c1", "view_detail"); err != nil || ok {
		t.Fatalf("denied check = %v, %v; want false", ok, err)
	}
	if ok, err := e.Check("alice", "catalog", "c2", "view_detail"); err != nil || !ok {
		t.Fatalf("sibling check = %v, %v; want true", ok, err)
	}
	entries := listObjectGrants(t, r, "?accessor_id=alice&resource_type=catalog&resource_id=c1")
	if len(entries) != 1 || len(entries[0].Operations) != 0 ||
		len(entries[0].DeniedOperations) != 1 || entries[0].DeniedOperations[0] != "view_detail" {
		t.Fatalf("deny-only grant listing = %+v", entries)
	}

	// A legacy request without effect still means allow and changes only allows;
	// it must not silently erase the independently managed deny exception.
	w = adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "alice",
		"resource":    map[string]any{"type": "catalog", "id": "c1"},
		"operations":  []string{"modify"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("legacy allow: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	entries = listObjectGrants(t, r, "?accessor_id=alice&resource_type=catalog&resource_id=c1")
	if len(entries) != 1 || len(entries[0].DeniedOperations) != 1 || entries[0].DeniedOperations[0] != "view_detail" {
		t.Fatalf("deny was not exposed separately: %+v", entries)
	}

	denyGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "alice", Object: "catalog:c1", Operation: "view_detail",
		Effect: authz.EffectDeny, PolicySource: authz.PolicySourceProfessionalRule,
		AuthoritySource: authz.AuthoritySourceAdminAuthz,
	})
	w = adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{"grant_id": denyGrantID})
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove deny: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, err := e.Check("alice", "catalog", "c1", "view_detail"); err != nil || !ok {
		t.Fatalf("role allow was not restored after removing deny: %v, %v", ok, err)
	}
}

func TestObjectGrantsValidation(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Department{ID: "dep-1", Name: "Data"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "role-1", Name: "Readers", Source: "custom"}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "catalog", "view_detail")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"department grantee", map[string]any{"accessor_id": "dep-1", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"role grantee", map[string]any{"accessor_id": "role-1", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"unknown user", map[string]any{"accessor_id": "ghost", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"wildcard id", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "catalog", "id": "*"}, "operations": []string{"view_detail"}}},
		{"unknown type", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "nope", "id": "c1"}, "operations": []string{"view_detail"}}},
		{"unknown op", map[string]any{"accessor_id": "u-1", "resource": map[string]any{"type": "catalog", "id": "c1"}, "operations": []string{"bogus"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}

// Role-subject and type-wide grants must not surface on the user object-grant
// listing (that surface is users-on-concrete-objects only).
func TestObjectGrantsExcludesRolesAndWildcards(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	if err := users.CreateLocalUser(ctx, &model.User{ID: "u-1", Account: "alice", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "role-x", Name: "x", Source: "custom"}).Error; err != nil {
		t.Fatal(err)
	}
	// a concrete grant to a ROLE (should be excluded)
	_ = e.GrantRolePermission("role-x", "catalog", "c9", "view_detail")
	roleGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "role-x", Object: "catalog:c9", Operation: "view_detail",
		PolicySource: authz.PolicySourceRolePermission,
	})
	// a type-wide grant to the user (id "*", should be excluded)
	_ = e.GrantRolePermission("u-1", "catalog", "*", "view_detail")
	// a concrete grant to the USER (should be included)
	_ = e.GrantObjectPermission("u-1", "catalog", "c1", "view_detail")

	entries := listObjectGrants(t, r, "")
	if len(entries) != 1 || entries[0].AccessorID != "u-1" || entries[0].Resource.ID != "c1" {
		t.Fatalf("listing must contain only the user concrete grant, got %+v", entries)
	}
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"grant_id": roleGrantID,
	}); w.Code != http.StatusForbidden {
		t.Fatalf("generic role revoke = %d %s; want dedicated role API", w.Code, w.Body.String())
	}
	if allowed, err := e.Check("role-x", "catalog", "c9", "view_detail"); err != nil || !allowed {
		t.Fatalf("generic object route removed a role permission: allowed=%v err=%v", allowed, err)
	}
}

func TestObjectGrantsPaginationAndSearch(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []model.User{
		{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, &u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	grant := func(accessorID, id string) {
		t.Helper()
		w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
			"accessor_id": accessorID,
			"resource":    map[string]any{"type": "catalog", "id": id},
			"operations":  []string{"view_detail"},
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("grant %s/%s: want 204, got %d (%s)", accessorID, id, w.Code, w.Body.String())
		}
	}
	grant("u-1", "c1")
	grant("u-1", "c2")
	grant("u-2", "c3")

	body := listObjectGrantsBody(t, r, "?limit=1&offset=0&include_summary=true")
	if body.Total != 3 || len(body.Entries) != 1 {
		t.Fatalf("pagination page 1: total=%d entries=%d", body.Total, len(body.Entries))
	}
	if body.Summary == nil || body.Summary.Grants != 3 || body.Summary.Objects != 3 || body.Summary.Grantees != 2 {
		t.Fatalf("unexpected summary: %+v", body.Summary)
	}
	body = listObjectGrantsBody(t, r, "?limit=1&offset=2")
	if body.Total != 3 || len(body.Entries) != 1 {
		t.Fatalf("pagination page 3: total=%d entries=%d", body.Total, len(body.Entries))
	}

	if got := listObjectGrants(t, r, "?search=alice"); len(got) != 2 {
		t.Fatalf("search by user: %+v", got)
	}
	if got := listObjectGrants(t, r, "?search=c3"); len(got) != 1 || got[0].Resource.ID != "c3" {
		t.Fatalf("search by resource id: %+v", got)
	}
	if got := listObjectGrants(t, r, "?obj_type=catalog&obj_id=c1"); len(got) != 1 {
		t.Fatalf("obj_* aliases: %+v", got)
	}
}

func TestObjectGrantsGroupedViews(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []model.User{
		{ID: "u-1", Account: "alice", Name: "Alice", Enabled: true},
		{ID: "u-2", Account: "bob", Name: "Bob", Enabled: true},
	} {
		if err := users.CreateLocalUser(ctx, &u, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	seedCatalogOps(t, db, "catalog", "view_detail", "modify")

	grant := func(accessorID, id string, ops ...string) {
		t.Helper()
		w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
			"accessor_id": accessorID,
			"resource":    map[string]any{"type": "catalog", "id": id},
			"operations":  ops,
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("grant %s/%s: %d (%s)", accessorID, id, w.Code, w.Body.String())
		}
	}
	// c1: granted to both u-1 and u-2; c2: only u-1.
	grant("u-1", "c1", "view_detail", "modify")
	grant("u-2", "c1", "view_detail")
	grant("u-1", "c2", "view_detail")

	decode := func(query string) struct {
		Groups []map[string]any `json:"groups"`
		Total  int              `json:"total"`
	} {
		t.Helper()
		w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants"+query, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("grouped list %s: %d (%s)", query, w.Code, w.Body.String())
		}
		var body struct {
			Groups []map[string]any `json:"groups"`
			Total  int              `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body
	}

	// group_by=object: 2 distinct objects (c1, c2); c1 has 2 grantees, c2 has 1.
	byObj := decode("?group_by=object")
	if byObj.Total != 2 || len(byObj.Groups) != 2 {
		t.Fatalf("group_by=object: total=%d groups=%d", byObj.Total, len(byObj.Groups))
	}
	for _, g := range byObj.Groups {
		obj := g["object"].(map[string]any)
		want := 1.0
		if obj["id"] == "c1" {
			want = 2.0
		}
		if g["grantee_count"].(float64) != want {
			t.Fatalf("object %v grantee_count = %v, want %v", obj["id"], g["grantee_count"], want)
		}
	}

	// group_by=grantee: 2 distinct grantees; u-1 on 2 objects, u-2 on 1.
	byGrantee := decode("?group_by=grantee")
	if byGrantee.Total != 2 || len(byGrantee.Groups) != 2 {
		t.Fatalf("group_by=grantee: total=%d groups=%d", byGrantee.Total, len(byGrantee.Groups))
	}
	for _, g := range byGrantee.Groups {
		want := 1.0
		if g["accessor_id"] == "u-1" {
			want = 2.0
		}
		if g["object_count"].(float64) != want {
			t.Fatalf("grantee %v object_count = %v, want %v", g["accessor_id"], g["object_count"], want)
		}
	}

	// grouped pagination: 1 object per page, total still 2.
	page := decode("?group_by=object&limit=1&offset=0")
	if page.Total != 2 || len(page.Groups) != 1 {
		t.Fatalf("grouped pagination: total=%d groups=%d", page.Total, len(page.Groups))
	}
}

// ownerGrantFixture builds the situation the owner path exists for: a builder
// who created a knowledge network (and therefore holds the Community business
// bundle plus a separate system-derived authorize grant) but holds no
// admin-authz permission at all.
func ownerGrantFixture(t *testing.T) (*gin.Engine, *authz.Enforcer) {
	r, e, _ := ownerGrantFixtureWithDB(t)
	return r, e
}

func ownerGrantFixtureWithDB(t *testing.T) (*gin.Engine, *authz.Enforcer, *gorm.DB) {
	t.Helper()
	r, e, db, users := newAdminServer(t)
	ctx := t.Context()
	for _, u := range []struct {
		id, account string
		enabled     bool
	}{
		{"u-owner", "builder", true},
		{"u-mate", "teammate", true},
		{"u-stranger", "stranger", true},
		{"u-disabled", "disabled", false},
	} {
		if err := users.CreateLocalUser(ctx, &model.User{ID: u.id, Account: u.account, Name: u.account, Enabled: u.enabled}, "pw-init0"); err != nil {
			t.Fatal(err)
		}
	}
	// Create is registered for the type but deliberately not granted on the
	// instance, so a test can ask for an operation that is valid yet unheld.
	seedCatalogOps(t, db, "knowledge_network",
		"view_detail", "create", "modify", "delete", "query_data", "authorize", "task_manage")
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "knowledge_network", "authorize").
		Update("implied_operation_ids", "view_detail").Error; err != nil {
		t.Fatal(err)
	}
	// The new-resource contract keeps business access and management authority
	// in independently managed sources.
	if err := e.GrantCommunityBundle("u-owner", "knowledge_network", "kn-mine",
		authz.AuthoritySourceSystem); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantSystemObjectPermission("u-owner", "knowledge_network", "kn-mine", "authorize"); err != nil {
		t.Fatal(err)
	}
	return r, e, db
}

// The regression: the creator of a knowledge network can share it, without any
// admin-authz permission. Before, opAuthorize was written on create and then
// never consulted, so this was a flat 403 (bkn-studio#478).
func TestObjectGrantsOwnerMayShareOwnObject(t *testing.T) {
	r, e := ownerGrantFixture(t)

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail", "query_data"},
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner share: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); !ok {
		t.Error("shared grant did not take effect at enforce time")
	}

	// POST is replacement, not merge.
	w = tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner replacement share: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "query_data"); ok {
		t.Error("replacement share retained an omitted operation")
	}

	// And can take it back.
	grantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "u-mate", Object: "knowledge_network:kn-mine", Operation: "view_detail",
		PolicySource: authz.PolicySourceProfessionalRule, AuthoritySource: authz.AuthoritySourceOwnerDelegate,
	})
	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": grantID,
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("owner revoke did not remove the grant")
	}
	// After the source row is gone, a delegated caller cannot prove that the
	// opaque id belonged to an object it manages. Only platform administrators
	// receive idempotent success for an already-absent id.
	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": grantID,
	}, "u-owner")
	if w.Code != http.StatusForbidden {
		t.Fatalf("repeat owner revoke: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestObjectGrantsUseKnowledgeNetworkAsChildAuthorizationRoot(t *testing.T) {
	r, e, db := ownerGrantFixtureWithDB(t)
	if err := db.Create(&model.ResourceType{
		ID: "object_type", Name: "object_type", ParentTypeID: "knowledge_network",
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "object_type", "view_detail", "modify", "delete", "query_data", "authorize")
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "object_type", "view_detail").
		Update("parent_operation_id", "view_detail").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "object_type", ResourceID: "kn-mine/order",
		ParentTypeID: "knowledge_network", ParentID: "kn-mine",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"view_detail", "modify", "query_data"} {
		if err := e.GrantObjectPermission("u-owner", "object_type", "kn-mine/order", operation); err != nil {
			t.Fatal(err)
		}
	}

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "object_type", "id": "kn-mine/order"},
		"operations":  []string{"modify"},
	}, "u-owner")
	if w.Code != http.StatusNoContent {
		t.Fatalf("child grant via KN authorize = %d %s", w.Code, w.Body.String())
	}
	if allowed, _ := e.Check("u-mate", "object_type", "kn-mine/order", "modify"); !allowed {
		t.Fatal("child grant did not become effective")
	}
	if err := e.GrantProfessionalObjectPermission("u-mate", "knowledge_network", "kn-mine", "query_data",
		authz.EffectDeny, authz.AuthoritySourceAdminAuthz); err != nil {
		t.Fatal(err)
	}
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "object_type", "id": "kn-mine/order"},
		"operations":  []string{"query_data"},
	}, "u-owner"); w.Code != http.StatusNoContent {
		t.Fatalf("owner child allow over parent admin deny = %d %s; want 204", w.Code, w.Body.String())
	}
	if allowed, err := e.Check("u-mate", "object_type", "kn-mine/order", "query_data"); err != nil || !allowed {
		t.Fatalf("specific child allow did not override parent deny: allowed=%v err=%v", allowed, err)
	}

	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "object_type", ResourceID: "kn-mine/hidden",
		ParentTypeID: "knowledge_network", ParentID: "kn-mine",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DenyObjectPermission("u-owner", "object_type", "kn-mine/hidden", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "object_type", "id": "kn-mine/hidden"},
		"operations":  []string{"query_data"},
	}, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("grant on invisible child = %d %s; want 403", w.Code, w.Body.String())
	}
	if w := tokReq(t, r, http.MethodGet,
		"/api/safe/v1/me/object-grants?resource_type=object_type&resource_id=kn-mine/hidden",
		nil, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("read grants on invisible child = %d %s; want 403", w.Code, w.Body.String())
	}
	if err := db.Model(&model.Operation{}).
		Where("resource_type_id = ? AND id = ?", "object_type", "modify").
		Update("implied_operation_ids", "delete").Error; err != nil {
		t.Fatal(err)
	}
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "object_type", "id": "kn-mine/order"},
		"operations":  []string{"modify"},
	}, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("grant with an unheld runtime requirement = %d %s; want 403", w.Code, w.Body.String())
	}

	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "object_type", "id": "kn-mine/order"},
		"operations":  []string{"authorize"},
	}); w.Code != http.StatusForbidden {
		t.Fatalf("BKN child authorize = %d %s; want 403", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"authorize"},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("platform-managed root authorize = %d %s; want 204", w.Code, w.Body.String())
	}
}

func TestObjectGrantsOwnerCannotRemoveAdminDeny(t *testing.T) {
	r, e, _ := ownerGrantFixtureWithDB(t)
	const readerRole = "kn-reader"
	if err := e.GrantRolePermission(readerRole, "knowledge_network", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u-mate", readerRole); err != nil {
		t.Fatal(err)
	}
	if err := e.DenyObjectPermission("u-mate", "knowledge_network", "kn-mine", "view_detail"); err != nil {
		t.Fatal(err)
	}

	denyGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "u-mate", Object: "knowledge_network:kn-mine",
		Operation: "view_detail", Effect: authz.EffectDeny,
	})
	// A delegated owner cannot revoke an administrator/legacy source by naming
	// its stable identity.
	w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": denyGrantID,
	}, "u-owner")
	if w.Code != http.StatusForbidden {
		t.Fatalf("legacy owner revoke: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, err := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); err != nil || ok {
		t.Fatalf("owner revoke removed admin deny: allowed=%v err=%v", ok, err)
	}

	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": denyGrantID,
	}, "u-owner")
	if w.Code != http.StatusForbidden {
		t.Fatalf("owner remove deny: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestObjectGrantsDisabledOwnerCannotWrite(t *testing.T) {
	r, e, db := ownerGrantFixtureWithDB(t)
	if err := db.Model(&model.User{}).Where("id = ?", "u-owner").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail", "query_data"},
	}, "u-owner")
	if w.Code != http.StatusForbidden {
		t.Fatalf("disabled owner share: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("disabled owner wrote a grant")
	}
}

// The two limits on a delegate, and the boundary of what it owns.
func TestObjectGrantsOwnerLimits(t *testing.T) {
	r, e := ownerGrantFixture(t)
	// A second network the owner did NOT create.
	if err := e.GrantObjectPermission("u-stranger", "knowledge_network", "kn-theirs", "view_detail"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			// The chain stops at one: a delegate cannot mint another delegate.
			"cannot pass authorize on",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"operations":  []string{"view_detail", "authorize"},
			},
		},
		{
			// create is registered for the type but the owner does not hold
			// it, so it cannot be handed out.
			"cannot grant an op it does not hold",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"operations":  []string{"view_detail", "create"},
			},
		},
		{
			// Ownership is per object, not per type.
			"cannot reach another owner's object",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-theirs"},
				"operations":  []string{"view_detail"},
			},
		},
		{
			"cannot write deny",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"operations":  []string{"view_detail"},
				"effect":      authz.EffectDeny,
			},
		},
		{
			"cannot change protected bundle source",
			map[string]any{
				"accessor_id": "u-mate",
				"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
				"bundle":      authz.ActFullBusinessAccess,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", tc.body, "u-owner"); w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d (%s)", w.Code, w.Body.String())
			}
		})
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("a rejected request must not write a partial grant")
	}

	// Disabled accounts are not valid grant targets and no policy is written.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-disabled",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("disabled grantee: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-disabled", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Error("a disabled grantee received a policy")
	}

	// Someone with no stake in the object gets nothing.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestObjectGrantsAuthorizeRevocationRemovesSharingAuthority(t *testing.T) {
	r, e := ownerGrantFixture(t)
	if _, err := e.RemoveAccessorResourcePolicies("u-owner", "knowledge_network", "kn-mine"); err != nil {
		t.Fatal(err)
	}

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-owner")
	if w.Code != http.StatusForbidden {
		t.Fatalf("sharing after authorize revocation: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

// A role holding type-wide authorize may delegate any instance of that type.
// This existing non-creator path remains restricted by the same two limits.
func TestObjectGrantsTypeWideAuthorize(t *testing.T) {
	r, e := ownerGrantFixture(t)
	if err := e.GrantRolePermission("role-builder", "knowledge_network", "*", "authorize"); err != nil {
		t.Fatal(err)
	}
	if err := e.GrantRolePermission("role-builder", "knowledge_network", "*", "view_detail"); err != nil {
		t.Fatal(err)
	}
	if err := e.AssignRole("u-stranger", "role-builder"); err != nil {
		t.Fatal(err)
	}

	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusNoContent {
		t.Fatalf("type-wide authorize holder: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	// modify is not in the role grant, so it cannot be passed on.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"modify"},
	}, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("op it does not hold: want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

// The self-service read follows the write authority: an owner sees who already
// holds their own network, and nothing else. It is per object by construction —
// there is no way to widen it into a platform view.
func TestObjectGrantsOwnerPolicyRead(t *testing.T) {
	r, _ := ownerGrantFixture(t)
	const base = "/api/safe/v1/me/object-grants?resource_type=knowledge_network"

	if w := tokReq(t, r, http.MethodGet, base+"&resource_id=kn-mine", nil, "u-owner"); w.Code != http.StatusOK {
		t.Fatalf("owner reading its own object: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if w := tokReq(t, r, http.MethodGet, base+"&resource_id=kn-theirs", nil, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("owner reading another object: want 403, got %d", w.Code)
	}
	// No id at all is rejected as a malformed request rather than answered as a
	// type-wide read — the endpoint has no type-wide mode to fall into.
	if w := tokReq(t, r, http.MethodGet, base, nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("owner reading the whole type: want 400, got %d (%s)", w.Code, w.Body.String())
	}
	// The platform-wide listing stays where it was, administrator-only.
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/object-grants", nil, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("owner listing every grant: want 403, got %d", w.Code)
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/policies?resource_type=knowledge_network", nil); w.Code != http.StatusOK {
		t.Fatalf("administrator reading the whole type: want 200, got %d", w.Code)
	}
}

// The owner-facing lookups: names on the grant rows, and a candidate picker.
// Both exist because the platform user directory is administrator-only, and both
// are gated on the same authority as writing grants on the object.
func TestObjectGrantsOwnerDirectoryLookups(t *testing.T) {
	r, _ := ownerGrantFixture(t)
	const base = "/api/safe/v1/me"

	w := tokReq(t, r, http.MethodGet, base+"/object-grants?resource_type=knowledge_network&resource_id=kn-mine", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("read grants: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var grants struct {
		Entries []struct {
			AccessorID      string `json:"accessor_id"`
			AccessorAccount string `json:"accessor_account"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(grants.Entries) != 1 || grants.Entries[0].AccessorID != "u-owner" {
		t.Fatalf("want the owner's own row, got %+v", grants.Entries)
	}
	if grants.Entries[0].AccessorAccount != "builder" {
		t.Errorf("accessor_account = %q, want \"builder\" — an id alone names nobody", grants.Entries[0].AccessorAccount)
	}

	// The picker is scoped to an object the caller may authorize...
	w = tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine&search=team", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("picker: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var picker struct {
		Users []struct {
			ID      string `json:"id"`
			Account string `json:"account"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &picker); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(picker.Users) != 1 || picker.Users[0].Account != "teammate" {
		t.Fatalf("search=team should match only teammate, got %+v", picker.Users)
	}

	// ...and is not a back door into the directory for anyone else.
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine&search=team", nil, "u-stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner picker: want 403, got %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&search=team", nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("picker without an object: want 400, got %d", w.Code)
	}
	// Holding authorize on one object is not a licence to page through the
	// directory, so an empty search is refused rather than answered with a page.
	if w := tokReq(t, r, http.MethodGet, base+"/grantable-users?resource_type=knowledge_network&resource_id=kn-mine", nil, "u-owner"); w.Code != http.StatusBadRequest {
		t.Fatalf("picker without a search term: want 400, got %d", w.Code)
	}
}

// Stable source slicing lets a delegate manage an ordinary owner-written row
// for a user who also holds authorize, without touching that protected sibling.
func TestObjectGrantsDelegateCannotMutateProtectedSources(t *testing.T) {
	r, e := ownerGrantFixture(t)
	// u-stranger holds type-wide authorize on the whole type, with no direct
	// stake in this particular network.
	mustGrantRole(t, e, "role-builder", "knowledge_network", "authorize")
	mustGrantRole(t, e, "role-builder", "knowledge_network", "view_detail")
	if err := e.AssignRole("u-stranger", "role-builder"); err != nil {
		t.Fatal(err)
	}

	// The creator holds authorize on kn-mine and must survive.
	authorizeGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "u-owner", Object: "knowledge_network:kn-mine", Operation: "authorize",
	})
	w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": authorizeGrantID,
	}, "u-stranger")
	if w.Code != http.StatusForbidden {
		t.Fatalf("stripping the creator: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-owner", "knowledge_network", "kn-mine", "authorize"); !ok {
		t.Fatal("the creator lost authorize on its own object")
	}

	// A plain grantee stays revocable — undoing a share is the point.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusNoContent {
		t.Fatalf("seed owner-managed grant: %d %s", w.Code, w.Body.String())
	}
	plaintGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: "u-mate", Object: "knowledge_network:kn-mine", Operation: "view_detail",
		PolicySource: authz.PolicySourceProfessionalRule, AuthoritySource: authz.AuthoritySourceOwnerDelegate,
	})
	w = tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": plaintGrantID,
	}, "u-stranger")
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoking a plain grantee: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-mate", "knowledge_network", "kn-mine", "view_detail"); ok {
		t.Fatal("delegate revoke left its owner-managed grant effective")
	}

	// POST replaces only the owner_delegate Professional slice. Granting an
	// ordinary operation to the creator is safe because its protected authorize
	// row belongs to a different source slice.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
		"accessor_id": "u-owner",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-mine"},
		"operations":  []string{"view_detail"},
	}, "u-stranger"); w.Code != http.StatusNoContent {
		t.Fatalf("source-scoped grant to the creator: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check("u-owner", "knowledge_network", "kn-mine", "authorize"); !ok {
		t.Fatal("the creator lost its protected authorize sibling through the grant path")
	}

	// An administrator is still able to do both.
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"grant_id": authorizeGrantID,
	}); w.Code != http.StatusNoContent {
		t.Fatalf("administrator revoking an authorize holder: want 204, got %d", w.Code)
	}
}

func mustGrantRole(t *testing.T, e *authz.Enforcer, roleID, resourceType, op string) {
	t.Helper()
	if err := e.GrantRolePermission(roleID, resourceType, "*", op); err != nil {
		t.Fatal(err)
	}
}

// A delegate must not be able to write a wildcard grant, or to un-publish a
// built-in by deleting the public-access row. Both are shapes that look like an
// ordinary per-object write but reach far past one object.
func TestObjectGrantsDelegateCannotWriteWildcardOrTouchPublic(t *testing.T) {
	r, e := ownerGrantFixture(t)

	// keyMatch treats "*" anywhere as a wildcard, so an id like "kn-*" would be
	// stored verbatim and then match every network with that prefix — a grant no
	// console screen could show or revoke.
	for _, id := range []string{"kn-*", "*-mine", "*"} {
		if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/object-grants", map[string]any{
			"accessor_id": "u-mate",
			"resource":    map[string]any{"type": "knowledge_network", "id": id},
			"operations":  []string{"view_detail"},
		}, "u-owner"); w.Code != http.StatusBadRequest {
			t.Errorf("granting on id %q: want 400, got %d (%s)", id, w.Code, w.Body.String())
		}
		if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
			"accessor_id": "u-mate",
			"resource":    map[string]any{"type": "knowledge_network", "id": id},
		}, "u-owner"); w.Code != http.StatusBadRequest {
			t.Errorf("revoking on id %q: want 400, got %d (%s)", id, w.Code, w.Body.String())
		}
	}
	// An administrator gets the same answer: these endpoints write one instance.
	if w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/object-grants", map[string]any{
		"accessor_id": "u-mate",
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-*"},
		"operations":  []string{"view_detail"},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("administrator granting on a wildcard id: want 400, got %d", w.Code)
	}

	// The public-access row publishes a built-in to everyone. It carries no
	// `authorize`, so the holder check alone would let a delegate delete it.
	if err := e.GrantObjectPermission(authz.PublicAccessorID, "knowledge_network", "kn-mine", "view_detail"); err != nil {
		t.Fatal(err)
	}
	publicGrantID := oneGrantID(t, e, authz.PolicyFilter{
		AccessorID: authz.PublicAccessorID, Object: "knowledge_network:kn-mine", Operation: "view_detail",
	})
	if w := tokReq(t, r, http.MethodDelete, "/api/safe/v1/me/object-grants", map[string]any{
		"grant_id": publicGrantID,
	}, "u-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("delegate deleting the public row: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	if ok, _ := e.Check(authz.PublicAccessorID, "knowledge_network", "kn-mine", "view_detail"); !ok {
		t.Error("the public-access row was removed by a delegate")
	}
	// An administrator may still remove it.
	if w := adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/object-grants", map[string]any{
		"grant_id": publicGrantID,
	}); w.Code != http.StatusNoContent {
		t.Errorf("administrator removing the public row: want 204, got %d", w.Code)
	}
}
