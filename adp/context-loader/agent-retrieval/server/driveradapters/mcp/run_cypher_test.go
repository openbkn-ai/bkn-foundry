// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
)

type refusingCypher struct{ err error }

func (r refusingCypher) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return nil, r.err
}

type returningCypher struct{ resp *interfaces.CypherQueryResp }

func (r returningCypher) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return r.resp, nil
}

// #1545: a bkn-backend refusal comes back to the agent as a tool error carrying
// the compiler's own code and details, so the agent can tell a property it may
// not read, or a resource grant it lacks, from a query it wrote wrong.
func TestRunCypherToolCarriesForbiddenCode(t *testing.T) {
	handler := handleRunCypher(refusingCypher{err: &infraErr.HTTPError{
		HTTPCode: http.StatusForbidden, Code: "BknBackend.Cypher.PropertyForbidden",
		Description: "refused", ErrorDetails: "ot_user.phone",
	}}, nil)

	result, err := handler(context.Background(), newCallToolRequest(map[string]any{
		"kn_id": "kn-1", "query": "MATCH (u:user) RETURN u.phone", "response_format": "json",
	}))
	if err != nil {
		t.Fatalf("handler error = %v, want a tool error result", err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %+v, want one error content", result)
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok || !strings.Contains(text.Text, `"code":"BknBackend.Cypher.PropertyForbidden"`) ||
		!strings.Contains(text.Text, "ot_user.phone") {
		t.Fatalf("content = %+v, want the refusal's code and details", result.Content[0])
	}
}

func TestRunCypherToolBuildsPropertyRequestLinksForEveryObjectType(t *testing.T) {
	handler := handleRunCypher(refusingCypher{err: &infraErr.HTTPError{
		HTTPCode: http.StatusForbidden, Code: "BknBackend.Cypher.PropertyForbidden",
		Description: "refused", ErrorDetails: "ot_order.amount, ot_customer.name",
		Metadata: map[string]any{"permission_impacts": []any{
			map[string]any{"object_type_id": "ot_order", "properties": []any{"amount"}},
			map[string]any{"object_type_id": "ot_order", "row_filter_applied": true},
			map[string]any{"object_type_id": "ot_customer", "properties": []any{"name"}},
		}},
	}}, guidanceTestGuide())

	result, err := handler(guidanceTestCtx(), newCallToolRequest(map[string]any{
		"kn_id": "kn-1", "query": "MATCH (o:Order), (c:Customer) RETURN o.amount, c.name", "response_format": "json",
	}))
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	var envelope struct {
		Guidance []interfaces.PermissionGuidance `json:"permission_guidance"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &envelope); err != nil {
		t.Fatalf("error text is not JSON: %v", err)
	}
	if len(envelope.Guidance) != 2 || envelope.Guidance[0].Resource.OtID != "ot_order" ||
		envelope.Guidance[1].Resource.OtID != "ot_customer" {
		t.Fatalf("guidance = %+v", envelope.Guidance)
	}
	for _, guidance := range envelope.Guidance {
		for _, shortfall := range guidance.Shortfalls {
			if shortfall.RequestPermissionURL == "" {
				t.Fatalf("guidance = %+v, want request links", guidance)
			}
		}
	}
	if len(envelope.Guidance[0].Shortfalls) != 2 ||
		envelope.Guidance[0].Shortfalls[1].Scope != interfaces.PermissionScopeRowFilter {
		t.Fatalf("order guidance = %+v, want property and row-filter requests", envelope.Guidance[0])
	}
}

func TestRunCypherToolExplainsSuccessfulRowFiltering(t *testing.T) {
	handler := handleRunCypher(returningCypher{resp: &interfaces.CypherQueryResp{
		Columns: []interfaces.CypherQueryColumn{{Name: "total", Type: "integer"}},
		Entries: []map[string]any{{"total": 3}},
		PermissionImpacts: []interfaces.ObjectPermissionImpact{{
			ObjectTypeID: "ot_order", RowFilterApplied: true,
		}},
	}}, guidanceTestGuide())

	result, err := handler(guidanceTestCtx(), newCallToolRequest(map[string]any{
		"kn_id": "kn-1", "query": "MATCH (o:Order) RETURN count(*) AS total", "response_format": "json",
	}))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	var body struct {
		Guidance []interfaces.PermissionGuidance `json:"permission_guidance"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &body); err != nil {
		t.Fatalf("result text is not JSON: %v", err)
	}
	if len(body.Guidance) != 1 || body.Guidance[0].Shortfalls[0].Scope != interfaces.PermissionScopeRowFilter ||
		body.Guidance[0].Shortfalls[0].RequestPermissionURL == "" {
		t.Fatalf("guidance = %+v", body.Guidance)
	}
}
