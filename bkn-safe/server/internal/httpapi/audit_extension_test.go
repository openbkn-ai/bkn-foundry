// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

type extensionAuditCapture struct{ entries []audit.Entry }

func (r *extensionAuditCapture) RecordBatch(_ context.Context, entries []audit.Entry) error {
	r.entries = append(r.entries, entries...)
	return nil
}

func TestExtensionAuditSnapshotAndNegativePaths(t *testing.T) {
	for _, tc := range []struct {
		name, method, actor string
		status              int
		suppress            bool
		want                int
	}{
		{"update", "PATCH", "admin", 200, false, 1},
		{"enqueue", "POST", "admin", 202, false, 1},
		{"read", "GET", "admin", 200, false, 0},
		{"connection test", "POST", "admin", 200, true, 0},
		{"validation", "PATCH", "admin", 400, false, 0},
		{"unauthorized", "PATCH", "admin", 401, false, 0},
		{"forbidden", "PATCH", "admin", 403, false, 0},
		{"failed write", "PATCH", "admin", 500, false, 0},
		{"missing actor", "PATCH", "", 200, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			conn, _ := db.DB()
			t.Cleanup(func() { _ = conn.Close() })
			if err := db.AutoMigrate(&model.User{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.User{ID: "admin", Name: "Administrator", Account: "admin", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			recorder := &extensionAuditCapture{}
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(ctxAccessorID, tc.actor); c.Next() })
			r.Use(auditMiddleware(recorder, nil, db))
			r.Handle(tc.method, "/api/safe/v1/admin/extensions/:id", func(c *gin.Context) {
				adminwrite.SetAuditOperation(c, adminwrite.AuditOperation{TargetType: "customer_identity_provider", Action: "update", TargetID: "external-id", TargetName: "Customer identity source", Suppress: tc.suppress})
				c.Status(tc.status)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/safe/v1/admin/extensions/external-id", strings.NewReader(`{"client_secret":"do-not-record","webhook_secret":"do-not-record"}`)))
			if w.Code != tc.status || len(recorder.entries) != tc.want {
				t.Fatalf("status=%d entries=%d", w.Code, len(recorder.entries))
			}
			if tc.want > 0 {
				entry := recorder.entries[0]
				// Exercise the real Kafka projection too, not just middleware capture.
				value, err := audit.BuildKafkaAdminRecord(entry, "test")
				if err != nil {
					t.Fatal(err)
				}
				var event map[string]any
				if err := json.Unmarshal(value, &event); err != nil {
					t.Fatal(err)
				}
				if event["target"].(map[string]any)["type"] != "customer_identity_provider" {
					t.Fatal("lost extension target type")
				}
				if entry.ActorNameSnapshot != "Administrator" || entry.TargetName != "Customer identity source" || entry.TargetID != "external-id" {
					t.Fatalf("wrong snapshots: %+v", entry)
				}
				if strings.Contains(entry.Detail, "do-not-record") {
					t.Fatal("secret entered audit detail")
				}
			}
		})
	}
}

func TestAuditRedactsNestedCredentialAliasesAndTruncatedBodies(t *testing.T) {
	raw := `{"client_secret":"credential-a","config":{"webhookSecret":"credential-b","nested":[{"REFRESH_TOKEN":"credential-c"}]},"enabled":true}`
	for _, detail := range []string{auditDetail([]byte(raw)), auditDetailFromPrefix([]byte(strings.TrimSuffix(raw, "}") + `,"large":[`))} {
		if strings.Contains(detail, "credential-") {
			t.Fatal("nested credential entered audit detail")
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(detail), &parsed) != nil || parsed["enabled"] != true {
			t.Fatalf("lost non-secret facts: %s", detail)
		}
	}
}
