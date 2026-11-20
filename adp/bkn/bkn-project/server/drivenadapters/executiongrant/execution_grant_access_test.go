// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package executiongrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestIssueAndRevokeAtBknSafe(t *testing.T) {
	var got []map[string]any
	var paths []string
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got, paths = append(got, body), append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(status)
		if r.Method == http.MethodPost && status == http.StatusCreated {
			_, _ = w.Write([]byte(`{"id":"run-1","credential":"bex_run-1_secret","expires_at":"2026-11-20T14:00:00Z"}`))
		}
		if status == http.StatusForbidden {
			_, _ = w.Write([]byte(`{"credential":"bex_leak"}`))
		}
	}))
	defer server.Close()
	a := NewExecutionGrantAccessAt(server.URL + "/")
	ctx := context.Background()

	grant, err := a.Issue(ctx, interfaces.ExecutionGrantRequest{ID: "run-1", ProjectID: "p1", RunID: "run-1", DelegatorID: "u1",
		KnID: "kn-1", AgentID: "studio_smart_qa", TTL: 6 * time.Hour})
	if err != nil || grant.Credential != "bex_run-1_secret" || !grant.ExpiresAt.Equal(time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)) {
		t.Fatal(grant, err)
	}
	if paths[0] != "POST /api/safe/in/v1/execution-grants" || got[0]["ttl_seconds"] != float64(21600) || got[0]["delegator_id"] != "u1" ||
		got[0]["kn_id"] != "kn-1" || got[0]["agent_id"] != "studio_smart_qa" {
		t.Fatal(paths, got)
	}
	status = http.StatusNoContent
	if err = a.Revoke(ctx, "run-1", "p1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if paths[1] != "DELETE /api/safe/in/v1/execution-grants/run-1" || got[1]["project_id"] != "p1" || got[1]["run_id"] != "run-1" {
		t.Fatal(paths, got)
	}
	// A refusal is an error that carries nothing bkn-safe answered.
	status = http.StatusForbidden
	if _, err = a.Issue(ctx, interfaces.ExecutionGrantRequest{ID: "run-2"}); err == nil || strings.Contains(err.Error(), "bex_") {
		t.Fatal("refusal", err)
	}
	if _, err = NewExecutionGrantAccessAt("").Issue(ctx, interfaces.ExecutionGrantRequest{}); err != ErrUnavailable {
		t.Fatal(err)
	}
}
