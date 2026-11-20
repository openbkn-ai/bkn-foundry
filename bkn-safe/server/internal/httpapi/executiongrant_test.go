// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/executiongrant"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

type networkPermission struct{ allowed bool }

func (p *networkPermission) CheckResourceOpsScoped(_ context.Context, _ string, checks []authz.ResourceOperationCheck, _ authz.EvaluationScope) ([]authz.FilteredResource, error) {
	decision := authz.DecisionDeny
	if p.allowed {
		decision = authz.DecisionAllow
	}
	return []authz.FilteredResource{{Type: checks[0].Resource.Type, ID: checks[0].Resource.ID,
		Decisions: []authz.OperationDecision{{Operation: checks[0].Operations[0], Decision: decision}}}}, nil
}

func TestExecutionGrantHTTPIssueIntrospectRevoke(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "owner", Account: "owner", Name: "Owner", Enabled: true, AccountType: model.AccountTypeOther})
	p := &networkPermission{allowed: true}
	r := gin.New()
	registerExecutionGrants(r, executiongrant.New(db, p))
	issue := executiongrant.IssueRequest{ID: "run-1", ProjectID: "project-1", RunID: "run-1", DelegatorID: "owner", KnID: "kn-1",
		AgentID: "studio_smart_qa", TTLSeconds: 600}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/execution-grants", issue)
	if w.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", w.Code, w.Body.String())
	}
	var issued executiongrant.Issued
	if err = json.Unmarshal(w.Body.Bytes(), &issued); err != nil || !strings.HasPrefix(issued.Credential, "bex_run-1_") {
		t.Fatal(issued, err)
	}
	if w = do(t, r, http.MethodPost, "/api/safe/in/v1/execution-grants", issue); w.Code != http.StatusConflict ||
		strings.Contains(w.Body.String(), "bex_") {
		t.Fatalf("issue again status=%d body=%s", w.Code, w.Body.String())
	}
	introspect := func() map[string]any {
		w := do(t, r, http.MethodPost, "/api/safe/v1/execution-grants/introspect", map[string]string{"token": issued.Credential})
		var body map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("introspect status=%d body=%s", w.Code, w.Body.String())
		}
		return body
	}
	body := introspect()
	if body["active"] != true || body["sub"] != "owner" || body["delegation_id"] != "run-1" || body["kn_id"] != "kn-1" ||
		body["agent_id"] != "studio_smart_qa" || body["account_type"] != "other" {
		t.Fatalf("introspect %v", body)
	}
	// Refused with no reason given once the delegator may no longer view the network.
	p.allowed = false
	if body = introspect(); len(body) != 1 || body["active"] != false {
		t.Fatalf("introspect without permission %v", body)
	}
	p.allowed = true
	if w = do(t, r, http.MethodDelete, "/api/safe/in/v1/execution-grants/run-1", map[string]string{"project_id": "project-1", "run_id": "run-1"}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d", w.Code)
	}
	if body = introspect(); body["active"] != false {
		t.Fatalf("introspect after revoke %v", body)
	}
	p.allowed = false
	issue.ID, issue.RunID = "run-2", "run-2"
	if w = do(t, r, http.MethodPost, "/api/safe/in/v1/execution-grants", issue); w.Code != http.StatusForbidden {
		t.Fatalf("issue without permission status=%d", w.Code)
	}
}
