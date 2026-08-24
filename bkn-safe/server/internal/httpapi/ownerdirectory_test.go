// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

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
