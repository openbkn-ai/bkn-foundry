// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestFailedUserCreateDoesNotCreateAuditFact(t *testing.T) {
	router, _, db, _ := newAdminServer(t)
	body := map[string]any{
		"account": "duplicate-user", "name": "Duplicate User", "password": "Phase4A-Test-only-123!",
	}
	if response := adminReq(t, router, http.MethodPost, "/api/safe/v1/admin/users", body); response.Code != http.StatusCreated {
		t.Fatalf("initial create: want %d, got %d (%s)", http.StatusCreated, response.Code, response.Body.String())
	}
	response := adminReq(t, router, http.MethodPost, "/api/safe/v1/admin/users", body)
	if response.Code < http.StatusBadRequest {
		t.Fatalf("duplicate create must fail, got %d (%s)", response.Code, response.Body.String())
	}

	var failedCount int64
	if err := db.Model(&model.AuditLog{}).Where("resource = ? AND status >= ?", "users", http.StatusBadRequest).
		Count(&failedCount).Error; err != nil {
		t.Fatal(err)
	}
	if failedCount != 0 {
		t.Fatalf("failed user create generated %d audit facts, want 0", failedCount)
	}
}
