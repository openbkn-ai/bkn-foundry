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
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/toon-format/toon-go"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
)

func TestRunCypherToolPreservesExactNumbers(t *testing.T) {
	for _, format := range []string{"json", "toon", ""} {
		t.Run("format="+format, func(t *testing.T) {
			resp := &interfaces.CypherQueryResp{Entries: []map[string]any{{
				"safe":       json.Number("9007199254740991"),
				"even":       json.Number("9007199254740992"),
				"odd":        json.Number("9007199254740993"),
				"negative":   json.Number("-9007199254740993"),
				"exact_text": "9007199254740993",
				"nested":     map[string]any{"value": json.Number("9007199254740993")},
			}}}
			arguments := map[string]any{"kn_id": "kn-1", "query": "MATCH (p:precision_sample) RETURN p"}
			if format != "" {
				arguments["response_format"] = format
			}
			result, err := handleRunCypher(returningCypher{resp: resp}, nil)(context.Background(), newCallToolRequest(arguments))
			if err != nil || result == nil || result.IsError {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			// Structured content keeps JSON numbers even when the text is TOON.
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Structured struct {
					Entries []map[string]any `json:"entries"`
				} `json:"structuredContent"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Structured.Entries) != 1 || envelope.Structured.Entries[0]["odd"] != json.Number("9007199254740993") {
				t.Fatalf("structured content lost precision: %s", raw)
			}
			var decoded struct {
				Entries []map[string]any `json:"entries" toon:"entries"`
			}
			text := resultText(result)
			if format == "json" {
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if err := decoder.Decode(&decoded); err != nil {
					t.Fatal(err)
				}
			} else if err := toon.Unmarshal([]byte(text), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Entries) != 1 {
				t.Fatalf("entries = %+v", decoded.Entries)
			}
			row := decoded.Entries[0]
			for key, digits := range map[string]string{"even": "9007199254740992", "odd": "9007199254740993", "negative": "-9007199254740993"} {
				var want any = digits
				if format == "json" {
					want = json.Number(digits)
				}
				if row[key] != want {
					t.Errorf("%s = %T(%v), want %T(%v); text = %s", key, row[key], row[key], want, want, text)
				}
			}
			if row["exact_text"] != "9007199254740993" {
				t.Errorf("text control = %#v", row["exact_text"])
			}
			var wantNested any = "9007199254740993"
			var wantSafe any = float64(9007199254740991)
			if format == "json" {
				wantNested = json.Number("9007199254740993")
				wantSafe = json.Number("9007199254740991")
			}
			if nested, ok := row["nested"].(map[string]any); !ok || nested["value"] != wantNested {
				t.Errorf("nested = %#v, want value %v", row["nested"], wantNested)
			}
			if row["safe"] != wantSafe {
				t.Errorf("safe = %T(%v), want %T(%v)", row["safe"], row["safe"], wantSafe, wantSafe)
			}
			if resp.Entries[0]["odd"] != json.Number("9007199254740993") {
				t.Fatal("formatting mutated the original response")
			}
		})
	}
}

type refusingCypher struct{ err error }

func (r refusingCypher) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return nil, r.err
}

type returningCypher struct{ resp *interfaces.CypherQueryResp }

// recordingCypher keeps the request the tool handed the service.
type recordingCypher struct{ req *kncypher.RunCypherReq }

func (r *recordingCypher) RunCypher(_ context.Context, req *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	r.req = req
	return &interfaces.CypherQueryResp{}, nil
}

// #2090: only the REST face takes a statement timeout. An agent would ask for
// as much as it could, so one sent to the tool is dropped.
func TestRunCypherToolDropsTheTimeout(t *testing.T) {
	cypher := &recordingCypher{}
	handler := handleRunCypher(cypher, nil)

	result, err := handler(context.Background(), newCallToolRequest(map[string]any{
		"kn_id": "kn-1", "query": "MATCH (o:Order) RETURN count(*) AS total", "timeout_sec": 55,
	}))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if cypher.req == nil || cypher.req.TimeoutSec != 0 {
		t.Fatalf("request = %+v, want no timeout", cypher.req)
	}
}

func (r returningCypher) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return r.resp, nil
}

// #1545: a bkn-backend refusal comes back to the agent as a tool error carrying
// the compiler's code and details nested in the local error, so the agent can tell a property it may
// not read, or a resource grant it lacks, from a query it wrote wrong.
func TestRunCypherToolCarriesForbiddenCode(t *testing.T) {
	handler := handleRunCypher(refusingCypher{err: infraErr.NewHTTPError(context.Background(), http.StatusForbidden, infraErr.ErrExtCommonExternalServerError, httperrors.BaseError{ErrorCode: "BknBackend.Cypher.PropertyForbidden",
		Description: "refused", ErrorDetails: "ot_user.phone"})}, nil)

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
	if !ok || !strings.Contains(text.Text, `"error_code":"BknBackend.Cypher.PropertyForbidden"`) ||
		!strings.Contains(text.Text, "ot_user.phone") {
		t.Fatalf("content = %+v, want the refusal's code and details", result.Content[0])
	}
}

func TestRunCypherToolBuildsPropertyRequestLinksForEveryObjectType(t *testing.T) {
	handler := handleRunCypher(refusingCypher{err: infraErr.NewHTTPError(context.Background(), http.StatusForbidden, infraErr.ErrExtCommonExternalServerError, httperrors.BaseError{ErrorCode: "BknBackend.Cypher.PropertyForbidden",
		Description: "refused", ErrorDetails: "ot_order.amount, ot_customer.name",
		Metadata: map[string]any{"permission_impacts": []any{
			map[string]any{"object_type_id": "ot_order", "properties": []any{"amount"}},
			map[string]any{"object_type_id": "ot_order", "row_filter_applied": true},
			map[string]any{"object_type_id": "ot_customer", "properties": []any{"name"}},
		}}})}, guidanceTestGuide())

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
