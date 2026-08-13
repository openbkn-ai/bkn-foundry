// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestFailedUserCreateAuditKeepsAttemptedBusinessTarget(t *testing.T) {
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

	var record model.AuditLog
	if err := db.Where("resource = ? AND status >= ?", "users", http.StatusBadRequest).
		Order("created_at DESC").First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Action != "create" || record.TargetID != "duplicate-user" || record.TargetName != "Duplicate User" {
		t.Fatalf("failed create target is not reproducible: %+v", record)
	}
	if record.ActorID == "" || record.RequestID == "" {
		t.Fatalf("failed create identity and correlation facts are incomplete: %+v", record)
	}
}
