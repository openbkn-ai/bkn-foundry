// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// serviceReq issues a tokenless request the way an in-cluster service would,
// with the self-declared caller header.
func serviceReq(t *testing.T, r *gin.Engine, method, path string, body any, caller string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if caller != "" {
		req.Header.Set("x-caller-service", caller)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTokenlessPolicyWritesAreAuditedAsServiceActor(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	grant := map[string]any{
		"accessor_id": adminSub,
		"resource":    map[string]any{"type": "knowledge_network", "id": "kn-334"},
		"operations":  []string{"view_detail"},
	}
	if w := serviceReq(t, r, http.MethodPost, "/api/safe/v1/authz/policies", grant, "bkn-backend"); w.Code != http.StatusNoContent {
		t.Fatalf("grant: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	if w := serviceReq(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
		"resource": map[string]any{"type": "knowledge_network", "id": "kn-334"},
	}, ""); w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}

	var rows []model.AuditLog
	if err := db.Where("resource = ?", "policies").Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("policy audit rows = %d, want 2: %+v", len(rows), rows)
	}
	grantRow, revokeRow := rows[0], rows[1]
	if grantRow.Action != "grant" || grantRow.Method != http.MethodPost || grantRow.Status != http.StatusNoContent || grantRow.TargetID != "kn-334" {
		t.Fatalf("grant row facts: %+v", grantRow)
	}
	if grantRow.ActorID != "" || grantRow.ActorType != "service" || grantRow.AuthMethod != "network" || grantRow.SourceChannel != "internal" {
		t.Fatalf("grant row actor must be the unnamed service peer: %+v", grantRow)
	}
	if !strings.Contains(grantRow.Detail, `"_caller_service":"bkn-backend"`) || !strings.Contains(grantRow.Detail, `"accessor_id":"`+adminSub+`"`) {
		t.Fatalf("grant row detail lacks caller/body facts: %s", grantRow.Detail)
	}
	if revokeRow.Action != "revoke" || revokeRow.Method != http.MethodDelete || revokeRow.TargetID != "kn-334" || strings.Contains(revokeRow.Detail, "_caller_service") {
		t.Fatalf("revoke row facts: %+v", revokeRow)
	}
	if grantRow.Seq == nil || revokeRow.Seq == nil || revokeRow.PrevHash != grantRow.RowHash {
		t.Fatalf("service rows are not chained: %+v -> %+v", grantRow, revokeRow)
	}
}

func TestTokenlessPolicyWriteRefusalIsAudited(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	w := serviceReq(t, r, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": adminSub, "resource": map[string]any{"type": "*", "id": "x"}, "operations": []string{"view_detail"},
	}, "vega")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wildcard grant: want 400, got %d", w.Code)
	}
	var row model.AuditLog
	if err := db.Where("resource = ? AND status = ?", "policies", http.StatusBadRequest).First(&row).Error; err != nil {
		t.Fatalf("refused service write not audited: %v", err)
	}
	if row.Action != "grant" || row.ActorType != "service" {
		t.Fatalf("refused row facts: %+v", row)
	}
}

func TestAuthenticationFailuresAreAuditedAndThrottled(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	for i := 0; i < 3; i++ {
		if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("no token: want 401, got %d", w.Code)
		}
	}
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil, "bad"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", w.Code)
	}
	var rows []model.AuditLog
	if err := db.Where("status = ?", http.StatusUnauthorized).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("401 rows = %d, want 1 (same client+route within the window is throttled): %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ActorID != "" || row.ActorType != "anonymous" || row.AuthMethod != "none" || row.Resource != "roles" || row.Method != http.MethodGet {
		t.Fatalf("401 row facts: %+v", row)
	}
	if !strings.Contains(row.Detail, `"_gate":"authn"`) {
		t.Fatalf("401 row must name the gate: %s", row.Detail)
	}
	// A different route is a different key and gets its own row.
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/me", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: want 401, got %d", w.Code)
	}
	var n int64
	db.Model(&model.AuditLog{}).Where("status = ?", http.StatusUnauthorized).Count(&n)
	if n != 2 {
		t.Fatalf("401 rows after a second route = %d, want 2", n)
	}
}

func TestAuthorizationRefusalsAreAuditedWithSubjectAndDecision(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	const outsider = "user-outsider"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: outsider, Account: outsider, Name: "Out Sider", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	// A read refused at the admin gate.
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil, outsider); w.Code != http.StatusForbidden {
		t.Fatalf("outsider read: want 403, got %d", w.Code)
	}
	// A write refused at the admin gate: exactly one row, from the failure
	// recorder (the mutation audit never ran).
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-x", "name": "X"}, outsider); w.Code != http.StatusForbidden {
		t.Fatalf("outsider write: want 403, got %d", w.Code)
	}
	var rows []model.AuditLog
	if err := db.Where("actor_id = ?", outsider).Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("403 rows for outsider = %d, want 2: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Status != http.StatusForbidden || row.ActorType != "user" || row.AuthMethod != "oauth" || row.ActorNameSnapshot != "Out Sider" {
			t.Fatalf("403 row facts: %+v", row)
		}
		if !strings.Contains(row.Detail, `"_gate":"authz"`) {
			t.Fatalf("403 row must name the gate: %s", row.Detail)
		}
	}
	if rows[0].Resource != "roles" || rows[0].Method != http.MethodGet || rows[1].Resource != "departments" || rows[1].Method != http.MethodPost || rows[1].Action != "create" {
		t.Fatalf("403 rows must keep what was attempted: %+v", rows)
	}
	// Both refusals are also decisions: safe_admin console manage, denied.
	var decisions []model.AuthzDecision
	if err := db.Where("accessor_id = ?", outsider).Find(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("outsider decisions = %d, want 2: %+v", len(decisions), decisions)
	}
	for _, d := range decisions {
		if d.Source != decisionSourceAdmin || d.ResourceType != "safe_admin" || d.ResourceID != "console" || d.Operation != "manage" || d.Decision != "deny" || d.Basis == "" {
			t.Fatalf("admin gate decision facts: %+v", d)
		}
	}
}

func TestPermissionPointRefusalOnMutationIsRecordedOnceWithGate(t *testing.T) {
	r, e, db, users := newAdminServer(t)
	// A console administrator without the department create point.
	const limited = "user-limited"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: limited, Account: limited, Name: limited, Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	if err := e.Grant(limited, "safe_admin:*", "manage"); err != nil {
		t.Fatal(err)
	}
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-y", "name": "Y"}, limited); w.Code != http.StatusForbidden {
		t.Fatalf("limited write: want 403, got %d (%s)", w.Code, w.Body.String())
	}
	var rows []model.AuditLog
	if err := db.Where("actor_id = ?", limited).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("refused mutation rows = %d, want exactly 1: %+v", len(rows), rows)
	}
	if rows[0].Status != http.StatusForbidden || rows[0].Resource != "departments" || !strings.Contains(rows[0].Detail, `"_gate":"permission"`) || !strings.Contains(rows[0].Detail, `"name":"Y"`) {
		t.Fatalf("refused mutation row must carry the body and the gate: %+v", rows[0])
	}
	var decision model.AuthzDecision
	if err := db.Where("accessor_id = ? AND resource_type = ?", limited, "admin-dept").First(&decision).Error; err != nil {
		t.Fatalf("permission point decision not recorded: %v", err)
	}
	if decision.Operation != "create" || decision.Decision != "deny" || decision.Source != decisionSourceAdmin {
		t.Fatalf("permission point decision facts: %+v", decision)
	}
}

func TestCheckRecordsDecisionsForActiveInactiveAndUnknownAccessors(t *testing.T) {
	r, _, db, users := newAdminServer(t)
	const plain = "user-plain"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: plain, Account: plain, Name: plain, Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	check := func(accessor string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/safe/v1/authz/check", strings.NewReader(`{"accessor_id":"`+accessor+`","resource":{"type":"knowledge_network","id":"kn-1"},"operation":"view_detail"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
		req.Header.Set("x-request-id", "req-"+accessor)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("check %s: want 200, got %d (%s)", accessor, w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body
	}
	if body := check(adminSub); body["allowed"] != true {
		t.Fatalf("admin check = %v", body)
	}
	if body := check(plain); body["allowed"] != false {
		t.Fatalf("plain check = %v", body)
	}
	if body := check("nobody"); body["allowed"] != false {
		t.Fatalf("unknown check = %v", body)
	}
	var rows []model.AuthzDecision
	if err := db.Where("source = ?", decisionSourceCheck).Order("request_id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("check decisions = %d, want 3: %+v", len(rows), rows)
	}
	byAccessor := map[string]model.AuthzDecision{}
	for _, row := range rows {
		byAccessor[row.AccessorID] = row
		if row.ResourceType != "knowledge_network" || row.ResourceID != "kn-1" || row.Operation != "view_detail" || row.Scope != "effective" {
			t.Fatalf("check decision target facts: %+v", row)
		}
		if row.RequestID != "req-"+row.AccessorID || row.TraceID != "0af7651916cd43dd8448eb211c80319c" || row.ClientIP == "" {
			t.Fatalf("check decision correlation facts: %+v", row)
		}
	}
	if d := byAccessor[adminSub]; d.Decision != "allow" || d.Basis == "" {
		t.Fatalf("admin decision: %+v", d)
	}
	if d := byAccessor[plain]; d.Decision != "deny" || d.Basis != "default" {
		t.Fatalf("plain decision: %+v", d)
	}
	if d := byAccessor["nobody"]; d.Decision != "deny" || d.Basis != basisInactiveAccount {
		t.Fatalf("unknown accessor decision: %+v", d)
	}
}

func TestOperationsAndResourceFilterRecordOneRowPerCall(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
		"accessor_id": adminSub, "resource": map[string]any{"type": "knowledge_network", "id": "kn-1"},
	}); w.Code != http.StatusOK {
		t.Fatalf("operations: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id": adminSub, "resource_type": "knowledge_network", "resource_ids": []string{"kn-1", "kn-2", "kn-3"},
		"visibility_operations": []string{"view_detail"}, "candidate_operations": []string{"view_detail", "delete"},
	}); w.Code != http.StatusOK {
		t.Fatalf("resource-filter: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var ops model.AuthzDecision
	if err := db.Where("source = ?", decisionSourceOperations).First(&ops).Error; err != nil {
		t.Fatalf("operations decision: %v", err)
	}
	if ops.Operation != "*" || ops.ResourceID != "kn-1" || ops.Detail == "" {
		t.Fatalf("operations decision facts: %+v", ops)
	}
	var filters []model.AuthzDecision
	if err := db.Where("source = ?", decisionSourceFilter).Find(&filters).Error; err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 {
		t.Fatalf("resource-filter decisions = %d, want exactly 1 for a 3-resource batch", len(filters))
	}
	if filters[0].ResourceType != "knowledge_network" || filters[0].Operation != "view_detail" || !strings.Contains(filters[0].Detail, `"requested":3`) {
		t.Fatalf("resource-filter decision facts: %+v", filters[0])
	}
}

func TestDecisionReadEndpointListsAnAccountsDecisionsInAWindow(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	// A permission-point decision from the request itself plus two planted
	// rows on either side of the window.
	old := model.AuthzDecision{ID: "old", AccessorID: "acct-x", Decision: "deny", Source: "check", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	in := model.AuthzDecision{ID: "in", AccessorID: "acct-x", Decision: "allow", Source: "check", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	for _, row := range []model.AuthzDecision{old, in} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/authz-decisions?accessor_id=acct-x&from=2026-05-01T00:00:00Z&to=2026-07-01T00:00:00Z", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Decisions []model.AuthzDecision `json:"decisions"`
		Total     int64                 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Decisions) != 1 || body.Decisions[0].ID != "in" {
		t.Fatalf("window list = %+v", body)
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/authz-decisions?from=not-a-time", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad from: want 400, got %d", w.Code)
	}
	// The read itself passed a permission point, which is a decision too.
	var mine model.AuthzDecision
	if err := db.Where("accessor_id = ? AND resource_type = ? AND operation = ?", adminSub, "admin-audit", "view").First(&mine).Error; err != nil {
		t.Fatalf("permission point decision for the read: %v", err)
	}
	if mine.Decision != "allow" {
		t.Fatalf("read decision: %+v", mine)
	}
}

func TestAuditChainEndpointsReportHeadAndDetectTampering(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-1", "name": "Root"})
	adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/d-1", map[string]any{"name": "Renamed"})

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-chain", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chain head: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var head struct {
		Head *struct {
			Seq     uint64 `json:"seq"`
			RowHash string `json:"row_hash"`
		} `json:"head"`
		Unchained int64 `json:"unchained_rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &head); err != nil {
		t.Fatal(err)
	}
	if head.Head == nil || head.Head.Seq != 2 || len(head.Head.RowHash) != 64 || head.Unchained != 0 {
		t.Fatalf("chain head = %s", w.Body.String())
	}
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-chain/verify", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"checked":2`) {
		t.Fatalf("verify intact: %d %s", w.Code, w.Body.String())
	}
	if err := db.Model(&model.AuditLog{}).Where("seq = ?", 1).Update("target_name", "Forged").Error; err != nil {
		t.Fatal(err)
	}
	w = adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-chain/verify", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"broken_seq":1`) || !strings.Contains(w.Body.String(), `"reason":"hash_mismatch"`) {
		t.Fatalf("verify tampered: %d %s", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/audit-chain/verify?from_seq=x", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad from_seq: want 400, got %d", w.Code)
	}
	// Chain reads are GETs: they left no audit rows of their own.
	var n int64
	db.Model(&model.AuditLog{}).Where("resource = ?", "audit-chain").Count(&n)
	if n != 0 {
		t.Fatalf("chain reads produced %d audit rows", n)
	}
}

func TestFailureLimiterWindow(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	l := newFailureLimiter(time.Minute)
	l.now = func() time.Time { return now }
	if !l.allow("k") || l.allow("k") {
		t.Fatal("first hit allowed, repeat within window throttled")
	}
	now = now.Add(61 * time.Second)
	if !l.allow("k") {
		t.Fatal("hit after the window must be allowed again")
	}
	if !l.allow("other") {
		t.Fatal("different key is independent")
	}
}
