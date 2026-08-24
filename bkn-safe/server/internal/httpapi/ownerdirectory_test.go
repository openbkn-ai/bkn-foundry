// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// seedDirectoryPeople puts two accounts in the directory so a picker has
// something to return, and gives the caller their ids.
func seedDirectoryPeople(t *testing.T, db *gorm.DB) (ownerID, colleagueID string) {
	t.Helper()
	owner := model.User{ID: "owner-1", Account: "owner", Name: "Owner One", Email: "owner@example.com", Telephone: "13800000001", Enabled: true}
	colleague := model.User{ID: "mate-1", Account: "mate", Name: "Mate One", Email: "mate@example.com", Telephone: "13800000002", Enabled: true}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if err := db.Create(&colleague).Error; err != nil {
		t.Fatalf("seed colleague: %v", err)
	}
	return owner.ID, colleague.ID
}

// An owner is someone holding `authorize` on one CONCRETE object — the row a
// domain service writes to whoever created it. That, and nothing weaker, opens
// the directory reads.
func TestOwnerDirectoryReadsGrantedByConcreteObjectGrant(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, colleagueID := seedDirectoryPeople(t, db)

	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil, ownerID); w.Code != http.StatusForbidden {
		t.Fatalf("before the grant, departments = %d, want 403", w.Code)
	}

	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil, ownerID); w.Code != http.StatusOK {
		t.Fatalf("departments = %d (%s), want 200", w.Code, w.Body.String())
	}

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/users/"+colleagueID, nil, ownerID)
	if w.Code != http.StatusOK {
		t.Fatalf("user detail = %d (%s), want 200", w.Code, w.Body.String())
	}
	// The picker needs a name, not a contact list.
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail["account"] != "mate" || detail["name"] != "Mate One" {
		t.Fatalf("detail lost the identity columns: %v", detail)
	}
	for _, leaked := range []string{"email", "telephone", "roles", "departments"} {
		if _, ok := detail[leaked]; ok {
			t.Errorf("owner-facing user detail exposes %q: %v", leaked, detail)
		}
	}
}

// A type-wide `authorize` is a role saying "members may share things of this
// kind". Every seeded network_builder holds one on catalog, connector_type,
// operator, skill and stream_data_pipeline, so honouring it here would hand the
// directory to the whole role — including members who have never created
// anything.
func TestOwnerDirectoryReadsRefuseTypeWideAuthorize(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)

	if err := e.Grant(ownerID, "catalog:*", "authorize"); err != nil {
		t.Fatalf("grant type-wide authorize: %v", err)
	}

	for _, path := range []string{
		"/api/safe/v1/admin/departments",
		"/api/safe/v1/admin/users",
		"/api/safe/v1/admin/users/mate-1",
	} {
		if w := tokReq(t, r, http.MethodGet, path, nil, ownerID); w.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403 for a type-wide-only holder", path, w.Code)
		}
	}
}

// An object grant that carries no `authorize` is not a licence to look anyone up.
func TestOwnerDirectoryReadsRefuseNonAuthorizeGrant(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)

	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "view_detail"); err != nil {
		t.Fatalf("grant view_detail: %v", err)
	}
	if w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments", nil, ownerID); w.Code != http.StatusForbidden {
		t.Fatalf("departments = %d, want 403", w.Code)
	}
}

// The list is projected and capped for an owner; an administrator keeps the
// shape the console has always read.
func TestOwnerDirectoryUserListProjectedAndCapped(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)
	for i := 0; i < ownerDirectoryMaxPageSize+10; i++ {
		id := fmt.Sprintf("bulk-%03d", i)
		if err := db.Create(&model.User{ID: id, Account: id, Name: "Bulk " + id, Email: id + "@example.com", Enabled: true}).Error; err != nil {
			t.Fatalf("seed bulk user: %v", err)
		}
	}
	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/users?limit=1000", nil, ownerID)
	if w.Code != http.StatusOK {
		t.Fatalf("users = %d (%s), want 200", w.Code, w.Body.String())
	}
	var out struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(out.Users) > ownerDirectoryMaxPageSize {
		t.Fatalf("owner pulled %d rows, cap is %d", len(out.Users), ownerDirectoryMaxPageSize)
	}
	for _, row := range out.Users {
		if _, ok := row["email"]; ok {
			t.Fatalf("owner-facing user row exposes email: %v", row)
		}
	}

	adminOut := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/users?limit=1000", nil)
	if adminOut.Code != http.StatusOK {
		t.Fatalf("admin users = %d, want 200", adminOut.Code)
	}
	var adminRows struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(adminOut.Body.Bytes(), &adminRows); err != nil {
		t.Fatalf("decode admin users: %v", err)
	}
	if len(adminRows.Users) <= ownerDirectoryMaxPageSize {
		t.Fatalf("the cap leaked onto the administrator: %d rows", len(adminRows.Users))
	}
	if _, ok := adminRows.Users[0]["email"]; !ok {
		t.Fatalf("the projection leaked onto the administrator: %v", adminRows.Users[0])
	}
}

// Capping the page length bounds one response and nothing else: offset=0,50,100…
// walks the same table at the same cost. An owner is pinned to the first page.
func TestOwnerDirectoryRefusesToPage(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)
	for i := 0; i < ownerDirectoryMaxPageSize*2; i++ {
		id := fmt.Sprintf("page-%03d", i)
		if err := db.Create(&model.User{ID: id, Account: id, Name: "Page " + id, Enabled: true}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	first := ownerAccounts(t, r, ownerID, "/api/safe/v1/admin/users?limit=50&offset=0")
	second := ownerAccounts(t, r, ownerID, "/api/safe/v1/admin/users?limit=50&offset=50")
	if len(first) == 0 {
		t.Fatal("first page came back empty")
	}
	if len(second) != len(first) || second[0] != first[0] {
		t.Fatalf("offset moved the window: first[0]=%q second[0]=%q", first[0], second[0])
	}
}

// The platform's head count is not an answer this caller asked for, and paired
// with a fixed page it is the number that tells an enumerator how far to go.
func TestOwnerDirectoryTotalReportsThePageOnly(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)
	for i := 0; i < ownerDirectoryMaxPageSize+25; i++ {
		id := fmt.Sprintf("count-%03d", i)
		if err := db.Create(&model.User{ID: id, Account: id, Name: "Count", Enabled: true}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/users", nil, ownerID)
	var out struct {
		Users []map[string]any `json:"users"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != len(out.Users) {
		t.Fatalf("total=%d leaks a count beyond the %d rows returned", out.Total, len(out.Users))
	}
}

// Listing users is one question; asking which accounts hold a named privileged
// role is another, and answering it hands over a target list.
func TestOwnerDirectoryIgnoresRoleFilter(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, colleagueID := seedDirectoryPeople(t, db)
	if err := e.AssignRole(colleagueID, "role-privileged"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	accounts := ownerAccounts(t, r, ownerID, "/api/safe/v1/admin/users?role_id=role-privileged")
	if len(accounts) < 2 {
		t.Fatalf("role_id narrowed the list to %v — the filter was honoured", accounts)
	}
}

// A department row locates a person; it does not have to describe the
// organisation. The admin shape keeps manager, code, email, remark and counts.
func TestOwnerDirectoryDepartmentsProjectedAndCapped(t *testing.T) {
	r, e, db, _ := newAdminServer(t)
	ownerID, _ := seedDirectoryPeople(t, db)
	for i := 0; i < ownerDirectoryMaxPageSize+5; i++ {
		id := fmt.Sprintf("dept-%03d", i)
		d := model.Department{ID: id, Name: "Dept " + id, ManagerID: "mate-1", Email: id + "@example.com", Code: id, Remark: "secret"}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("seed dept: %v", err)
		}
	}
	if err := e.GrantObjectPermission(ownerID, "knowledge_network", "kn-1", "authorize"); err != nil {
		t.Fatalf("grant authorize: %v", err)
	}

	w := tokReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments?limit=1000", nil, ownerID)
	if w.Code != http.StatusOK {
		t.Fatalf("departments = %d (%s)", w.Code, w.Body.String())
	}
	var out struct {
		Departments []map[string]any `json:"departments"`
		Total       int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Departments) > ownerDirectoryMaxPageSize {
		t.Fatalf("owner pulled %d departments, cap is %d", len(out.Departments), ownerDirectoryMaxPageSize)
	}
	if out.Total != len(out.Departments) {
		t.Fatalf("total=%d beyond the %d rows returned", out.Total, len(out.Departments))
	}
	for _, row := range out.Departments {
		for _, leaked := range []string{"manager_id", "manager_name", "email", "code", "remark", "member_count", "subtree_member_count"} {
			if _, ok := row[leaked]; ok {
				t.Fatalf("owner-facing department exposes %q: %v", leaked, row)
			}
		}
	}

	adminResp := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/departments?limit=1000", nil)
	var adminOut struct {
		Departments []map[string]any `json:"departments"`
	}
	if err := json.Unmarshal(adminResp.Body.Bytes(), &adminOut); err != nil {
		t.Fatalf("decode admin: %v", err)
	}
	if len(adminOut.Departments) <= ownerDirectoryMaxPageSize {
		t.Fatalf("the cap leaked onto the administrator: %d rows", len(adminOut.Departments))
	}
	if _, ok := adminOut.Departments[0]["member_count"]; !ok {
		t.Fatalf("the projection leaked onto the administrator: %v", adminOut.Departments[0])
	}
}

// ownerAccounts issues an owner-authenticated user list read and returns the
// accounts it named.
func ownerAccounts(t *testing.T, r *gin.Engine, token, path string) []string {
	t.Helper()
	w := tokReq(t, r, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("%s = %d (%s)", path, w.Code, w.Body.String())
	}
	var out struct {
		Users []struct {
			Account string `json:"account"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	accounts := make([]string, 0, len(out.Users))
	for _, u := range out.Users {
		accounts = append(accounts, u.Account)
	}
	return accounts
}
