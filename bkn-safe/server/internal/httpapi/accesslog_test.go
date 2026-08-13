// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAccessLogReadEndpointIsAvailableToTheExistingAdminSurface(t *testing.T) {
	r, _, _, _ := newAdminServer(t)

	response := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/access-logs", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("access-log read endpoint status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestAccessLogReadsRequireAuditViewPermission(t *testing.T) {
	r, e, _, users := newAdminServer(t)
	const (
		securityUser = "access-log-security"
		securityRole = "access-log-security-role"
		auditUser    = "access-log-auditor"
		auditRole    = "access-log-auditor-role"
	)
	for _, roleID := range []string{securityRole, auditRole} {
		grantAdminSurface(t, e, roleID)
	}
	grantRoleOps(t, e, auditRole, "admin-audit", "view")
	for _, userID := range []string{securityUser, auditUser} {
		if err := users.CreateLocalUser(t.Context(), &model.User{ID: userID, Account: userID, Name: userID, Enabled: true}, "pw-init0"); err != nil {
			t.Fatalf("create user %s: %v", userID, err)
		}
	}
	bindRole(t, e, securityUser, securityRole)
	bindRole(t, e, auditUser, auditRole)

	if response := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/access-logs", nil, securityUser); response.Code != http.StatusForbidden {
		t.Fatalf("security role status = %d, want 403: %s", response.Code, response.Body.String())
	}
	if response := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/access-logs", nil, auditUser); response.Code != http.StatusOK {
		t.Fatalf("audit role status = %d, want 200: %s", response.Code, response.Body.String())
	}
}

func TestVoluntaryLogoutRecordsAnAccessFact(t *testing.T) {
	r, _, db, _ := newAdminServer(t)

	response := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/logout", nil, adminSub)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}

	var row model.AccessLog
	if err := db.First(&row, "actor_id = ?", adminSub).Error; err != nil {
		t.Fatalf("read logout access fact: %v", err)
	}
	if row.Action != "logout" || row.Outcome != "success" || row.AuthMethod != "oauth" {
		t.Fatalf("logout access fact = %#v, want oauth/logout/success", row)
	}
}
