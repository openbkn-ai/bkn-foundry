package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDeletePoliciesDoesNotReportSuccessWhenHierarchyCleanupFails(t *testing.T) {
	r, e, db := newTestServer(t)
	declareHierarchy(t, db)
	if err := e.GrantObjectPermission("reader", "resource", "res-1", "modify"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "resource", ResourceID: "res-1", ParentTypeID: "catalog", ParentID: "cat-a",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("test:parent_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ResourceParent" {
			tx.AddError(errors.New("parent deletion failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"resource": map[string]string{"type": "resource", "id": "res-1"}}
	w := do(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE = %d, want failure; body=%s", w.Code, w.Body.String())
	}
	if allowed, err := e.Check("reader", "resource", "res-1", "modify"); err != nil || !allowed {
		t.Fatalf("failed teardown lost the caller's permission: allowed=%v err=%v", allowed, err)
	}
	if err := db.Callback().Delete().Remove("test:parent_failure"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w = do(t, r, http.MethodDelete, "/api/safe/v1/authz/policies", body)
		if w.Code != http.StatusNoContent {
			t.Fatalf("retry DELETE = %d, body=%s", w.Code, w.Body.String())
		}
	}
}
