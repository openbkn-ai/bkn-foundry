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

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
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

func TestTokenlessPolicyWriteRefusalDoesNotCreateAuditFact(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	w := serviceReq(t, r, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": adminSub, "resource": map[string]any{"type": "*", "id": "x"}, "operations": []string{"view_detail"},
	}, "vega")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wildcard grant: want 400, got %d", w.Code)
	}
	var count int64
	if err := db.Model(&model.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("refused service write created %d audit facts, want 0", count)
	}
}

func TestAuthenticationFailuresDoNotCreateAuditFacts(t *testing.T) {
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
	if len(rows) != 0 {
		t.Fatalf("401 rows = %d, want 0: %+v", len(rows), rows)
	}
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/me", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: want 401, got %d", w.Code)
	}
	var n int64
	db.Model(&model.AuditLog{}).Where("status = ?", http.StatusUnauthorized).Count(&n)
	if n != 0 {
		t.Fatalf("401 rows after a second route = %d, want 0", n)
	}
}

func TestAuthorizationRefusalsDoNotCreateAuditFacts(t *testing.T) {
	r, _, db, users, collector := newAdminServerWithDecisions(t)
	const outsider = "user-outsider"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: outsider, Account: outsider, Name: "Out Sider", Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	// A read refused at the admin gate.
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil, outsider); w.Code != http.StatusForbidden {
		t.Fatalf("outsider read: want 403, got %d", w.Code)
	}
	// A write refused at the admin gate does not create an audit fact either.
	if w := tokReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-x", "name": "X"}, outsider); w.Code != http.StatusForbidden {
		t.Fatalf("outsider write: want 403, got %d", w.Code)
	}
	// Repeated denied reads remain business-noise-free.
	for i := 0; i < 5; i++ {
		if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/roles", nil, outsider); w.Code != http.StatusForbidden {
			t.Fatalf("outsider repeat read: want 403, got %d", w.Code)
		}
	}
	var rows []model.AuditLog
	if err := db.Where("actor_id = ?", outsider).Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("403 rows for outsider = %d, want 0: %+v", len(rows), rows)
	}
	// Every refusal is also a decision (decisions are not throttled): safe_admin
	// console manage, denied — 2 distinct requests + 5 repeats.
	var decisions []decisionlog.Entry
	for _, entry := range collector.Snapshot() {
		if entry.AccessorID == outsider {
			decisions = append(decisions, entry)
		}
	}
	if len(decisions) != 7 {
		t.Fatalf("outsider decisions = %d, want 7: %+v", len(decisions), decisions)
	}
	for _, d := range decisions {
		if d.Source != decisionSourceAdmin || d.ResourceType != "safe_admin" || d.ResourceID != "console" || d.Operation != "manage" || d.Decision != "deny" || d.Basis == "" || d.VerifiedActorID != outsider {
			t.Fatalf("admin gate decision facts: %+v", d)
		}
	}
}

func TestPermissionPointRefusalOnMutationDoesNotCreateAuditFact(t *testing.T) {
	r, e, db, users, collector := newAdminServerWithDecisions(t)
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
	if len(rows) != 0 {
		t.Fatalf("refused mutation rows = %d, want 0: %+v", len(rows), rows)
	}
	var decision decisionlog.Entry
	for _, entry := range collector.Snapshot() {
		if entry.AccessorID == limited && entry.ResourceType == "admin-dept" {
			decision = entry
		}
	}
	if decision.Operation != "create" || decision.Decision != "deny" || decision.Source != decisionSourceAdmin {
		t.Fatalf("permission point decision facts: %+v", decision)
	}
}

func TestCheckRecordsDecisionsForActiveInactiveAndUnknownAccessors(t *testing.T) {
	r, _, _, users, collector := newAdminServerWithDecisions(t)
	const plain = "user-plain"
	if err := users.CreateLocalUser(t.Context(), &model.User{ID: plain, Account: plain, Name: plain, Enabled: true}, "pw-init0"); err != nil {
		t.Fatal(err)
	}
	check := func(accessor string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/safe/v1/authz/checks", strings.NewReader(`{"accessor_id":"`+accessor+`","checks":[{"resource":{"type":"knowledge_network","id":"kn-1"},"operation":"view_detail"}]}`))
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
	var rows []decisionlog.Entry
	for _, entry := range collector.Snapshot() {
		if entry.Source == decisionSourceCheck {
			rows = append(rows, entry)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("check decisions = %d, want 3: %+v", len(rows), rows)
	}
	byAccessor := map[string]decisionlog.Entry{}
	for _, row := range rows {
		byAccessor[row.AccessorID] = row
		if row.ResourceType != "knowledge_network" || row.ResourceID != "kn-1" || row.Operation != "view_detail" || row.Scope != "effective" {
			t.Fatalf("check decision target facts: %+v", row)
		}
		if row.VerifiedActorID != row.AccessorID {
			t.Fatalf("check decision actor = %q, want evaluated user %q: %+v", row.VerifiedActorID, row.AccessorID, row)
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

func TestResourceFilterRecordsOneRowPerCall(t *testing.T) {
	r, _, _, _, collector := newAdminServerWithDecisions(t)
	if w := do(t, r, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id": adminSub, "resource_type": "knowledge_network", "resource_ids": []string{"kn-1", "kn-2", "kn-3"},
		"visibility_operations": []string{"view_detail"}, "include_operations": true,
	}); w.Code != http.StatusOK {
		t.Fatalf("resource-filter: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var filters []decisionlog.Entry
	for _, entry := range collector.Snapshot() {
		if entry.Source == decisionSourceFilter {
			filters = append(filters, entry)
		}
	}
	if len(filters) != 1 {
		t.Fatalf("resource-filter decisions = %d, want exactly 1 for a 3-resource batch", len(filters))
	}
	if filters[0].ResourceType != "knowledge_network" || filters[0].Operation != "view_detail" || !strings.Contains(filters[0].Detail, `"requested":3`) {
		t.Fatalf("resource-filter decision facts: %+v", filters[0])
	}
	if filters[0].VerifiedActorID != adminSub {
		t.Fatalf("resource-filter decision actor = %q, want evaluated user %q: %+v", filters[0].VerifiedActorID, adminSub, filters[0])
	}
}

func TestDecisionReadEndpointIsRetiredButHistoryRemains(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	old := model.AuthzDecision{ID: "old", AccessorID: "acct-x", Decision: "deny", Source: "check", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	in := model.AuthzDecision{ID: "in", AccessorID: "acct-x", Decision: "allow", Source: "check", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	for _, row := range []model.AuthzDecision{old, in} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/authz-decisions?accessor_id=acct-x&from=2026-05-01T00:00:00Z&to=2026-07-01T00:00:00Z", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("retired local query: want 404, got %d (%s)", w.Code, w.Body.String())
	}
	var count int64
	if err := db.Model(&model.AuthzDecision{}).Where("accessor_id = ?", "acct-x").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("historical decisions lost: %d", count)
	}
}

func TestHistoricalAuditChainRemainsVerifiableWithoutHTTPRoutes(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-1", "name": "Root"})
	adminReq(t, r, http.MethodPut, "/api/safe/v1/admin/departments/d-1", map[string]any{"name": "Renamed"})

	store := audit.New(db)
	head, err := store.Verify(t.Context(), 0, 0, 1)
	if err != nil || head.Head == nil || head.Head.Seq != 2 || len(head.Head.RowHash) != 64 || head.UnchainedRows != 0 {
		t.Fatalf("historical chain head = %+v, %v", head, err)
	}
	intact, err := store.Verify(t.Context(), 0, 0, 0)
	if err != nil || !intact.OK || intact.Checked != 2 {
		t.Fatalf("historical chain verification = %+v, %v", intact, err)
	}
	if err := db.Model(&model.AuditLog{}).Where("seq = ?", 1).Update("target_name", "Forged").Error; err != nil {
		t.Fatal(err)
	}
	broken, err := store.Verify(t.Context(), 0, 0, 0)
	if err != nil || broken.OK || broken.BrokenSeq == nil || *broken.BrokenSeq != 1 || broken.Reason != "hash_mismatch" {
		t.Fatalf("tampered historical chain = %+v, %v", broken, err)
	}
	for _, path := range []string{"/api/safe/v1/admin/audit-chain", "/api/safe/v1/admin/audit-chain/verify"} {
		if w := adminReq(t, r, http.MethodGet, path, nil); w.Code != http.StatusNotFound {
			t.Fatalf("retired audit endpoint %s = %d, want 404", path, w.Code)
		}
	}
}

func TestAuditedMutationKeepsBodiesLargerThanTheSnapshotCap(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	// A body past maxAuditBody: the snapshot is capped, the handler must still
	// see the whole JSON (a resource-parents batch at its documented 1000-item
	// limit is well past 64KB).
	body := map[string]any{"id": "d-big", "name": "Big", "padding": strings.Repeat("x", 70<<10)}
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("oversized body: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	if w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments/d-big", nil); w.Code != http.StatusOK {
		t.Fatalf("department from the oversized body was not created: %d", w.Code)
	}
	var row model.AuditLog
	if err := db.Where("request_id = ?", w.Header().Get("x-request-id")).First(&row).Error; err != nil {
		t.Fatalf("oversized mutation not audited: %v", err)
	}
	if row.Status != http.StatusCreated || row.Resource != "departments" || len(row.Detail) > maxAuditDetail {
		t.Fatalf("oversized mutation row: %+v", row)
	}
	// The snapshot is partial, says so, and still carries the top-level
	// fields that came before the oversized one.
	if !strings.Contains(row.Detail, `"_body_truncated":true`) || !strings.Contains(row.Detail, `"name":"Big"`) || strings.Contains(row.Detail, "padding") {
		t.Fatalf("truncated snapshot detail: %.200s", row.Detail)
	}
}

func TestAuditDetailSummarizesLargeValidJSONWithoutCuttingIt(t *testing.T) {
	grantIDs := make([]string, 40)
	for i := range grantIDs {
		grantIDs[i] = strings.Repeat("g", 64)
	}
	raw, err := json.Marshal(map[string]any{
		"grant_ids": grantIDs,
		"reason":    "batch revoke",
	})
	if err != nil || len(raw) <= maxAuditDetail {
		t.Fatalf("fixture size = %d err=%v, want over %d", len(raw), err, maxAuditDetail)
	}
	detail := auditDetail(raw)
	var got map[string]any
	if len(detail) > maxAuditDetail || json.Unmarshal([]byte(detail), &got) != nil {
		t.Fatalf("large detail is not bounded valid JSON (%d bytes): %s", len(detail), detail)
	}
	if got["_body_truncated"] != true || got["reason"] != "batch revoke" {
		t.Fatalf("large detail summary = %s", detail)
	}
	if _, kept := got["grant_ids"]; kept {
		t.Fatalf("large array was kept in detail summary: %s", detail)
	}
}

func TestAuditDetailFromPrefixKeepsLeadingFieldsAndSkipsArrays(t *testing.T) {
	// A resource-parents batch shape: two scalars, one nested object, then an
	// array the cut runs through.
	head := `{"resource_type":"resource","parent_type":"catalog","resource":{"type":"knowledge_network","id":"kn-1"},"password":"x","tags":["a","b"],"items":[`
	body := head + strings.Repeat(`{"resource_id":"r","parent_id":"p"},`, 4000)
	cut := body[:maxAuditBody]
	got := auditDetailFromPrefix([]byte(cut))
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("prefix detail is not JSON: %s", got)
	}
	if m["_body_truncated"] != true || m["resource_type"] != "resource" || m["parent_type"] != "catalog" || m["password"] != "***" {
		t.Fatalf("prefix detail = %s", got)
	}
	if ref, _ := m["resource"].(map[string]any); ref["id"] != "kn-1" {
		t.Fatalf("small nested object dropped: %s", got)
	}
	if _, kept := m["tags"]; kept {
		t.Fatalf("array kept in prefix detail: %s", got)
	}
	if _, kept := m["items"]; kept {
		t.Fatalf("cut array kept in prefix detail: %s", got)
	}
	// And the target id derivation works off the salvaged snapshot.
	if id := auditDetailTargetID("resource-parents", got); id != "resource" {
		t.Fatalf("resource-parents target from prefix = %q", id)
	}
	if id := auditDetailTargetID("policies", got); id != "kn-1" {
		t.Fatalf("policies target from prefix = %q", id)
	}
	if got := auditDetailFromPrefix([]byte("[1,2")); got != `{"_body_truncated":true}` {
		t.Fatalf("non-object prefix = %s", got)
	}
}

func TestOneRequestIDTiesResponseAndCommittedAudit(t *testing.T) {
	r, _, db, _, _ := newAdminServerWithDecisions(t)
	w := adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/departments", map[string]any{"id": "d-rid", "name": "RID"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	rid := w.Header().Get("x-request-id")
	if rid == "" {
		t.Fatal("no x-request-id on the response")
	}
	var row model.AuditLog
	if err := db.Where("request_id = ?", rid).First(&row).Error; err != nil {
		t.Fatalf("no audit row carries the response's request id %q: %v", rid, err)
	}
	if row.Resource != "departments" || row.Method != http.MethodPost || row.Status != http.StatusCreated {
		t.Fatalf("audit row for the request id: %+v", row)
	}
}
