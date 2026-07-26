package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
)

// capturePort records the exact query the handler forwards to OpenSearch, so a
// test can assert whether (and how) it was scoped.
type capturePort struct {
	last json.RawMessage
}

func (p *capturePort) SearchTraces(_ context.Context, q json.RawMessage) (opensearchvo.SearchResult, error) {
	p.last = q
	return opensearchvo.SearchResult(`{"hits":{"hits":[]}}`), nil
}

func handlerWith(cfg conf.TraceReadAuthzConfig) (*TraceHandler, *capturePort) {
	port := &capturePort{}
	return NewTraceHandlerWithAuthz(tracesvc.New(port), cfg), port
}

func searchReq(body, baggage string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/traces/_search", strings.NewReader(body))
	if baggage != "" {
		req.Header.Set("baggage", baggage)
	}
	return req
}

var enforce = conf.TraceReadAuthzConfig{Enforce: true, AdminTypes: map[string]bool{"admin": true, "audit": true, "super_admin": true}}
var shadow = conf.TraceReadAuthzConfig{Enforce: false, AdminTypes: map[string]bool{"admin": true, "audit": true, "super_admin": true}}

// --- scopeQueryToAccount unit tests ---

func TestScopeInjectsAccountFilterAndKeepsOriginalQuery(t *testing.T) {
	body := json.RawMessage(`{"size":50,"sort":[{"x":"asc"}],"query":{"term":{"name":"a"}}}`)
	out, err := scopeQueryToAccount(body, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	// size and sort survive.
	if string(root["size"]) != "50" {
		t.Errorf("size not preserved: %s", root["size"])
	}
	if _, ok := root["sort"]; !ok {
		t.Error("sort not preserved")
	}
	s := string(root["query"])
	if !strings.Contains(s, accountAttrField) || !strings.Contains(s, "u-1") {
		t.Errorf("account filter not injected: %s", s)
	}
	if !strings.Contains(s, `"term":{"name":"a"}`) {
		t.Errorf("original query not kept under must: %s", s)
	}
}

func TestScopeEmptyQueryBecomesScopedMatchAll(t *testing.T) {
	out, err := scopeQueryToAccount(json.RawMessage(`{"size":5}`), "u-2")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "match_all") || !strings.Contains(s, "u-2") {
		t.Errorf("empty query should become scoped match_all: %s", s)
	}
}

func TestScopeRejectsNonObjectBody(t *testing.T) {
	if _, err := scopeQueryToAccount(json.RawMessage(`[1,2,3]`), "u-1"); err == nil {
		t.Fatal("expected error for non-object search body")
	}
}

// --- handler staged-behaviour tests ---

func TestEnforceRejectsUnauthenticated(t *testing.T) {
	h, port := handlerWith(enforce)
	rec := httptest.NewRecorder()
	h.SearchTraces(rec, searchReq(`{"query":{"match_all":{}}}`, "")) // no baggage
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", rec.Code, rec.Body.String())
	}
	if port.last != nil {
		t.Fatal("no query should reach OpenSearch when rejected")
	}
}

func TestEnforceScopesNormalCaller(t *testing.T) {
	h, port := handlerWith(enforce)
	rec := httptest.NewRecorder()
	h.SearchTraces(rec, searchReq(`{"query":{"match_all":{}}}`, "bkn.account.type=user,bkn.account.id=u-9"))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	s := string(port.last)
	if !strings.Contains(s, accountAttrField) || !strings.Contains(s, "u-9") {
		t.Fatalf("normal caller query must be scoped to its account: %s", s)
	}
}

func TestEnforceDoesNotScopeAdmin(t *testing.T) {
	h, port := handlerWith(enforce)
	rec := httptest.NewRecorder()
	h.SearchTraces(rec, searchReq(`{"query":{"match_all":{}}}`, "bkn.account.type=audit,bkn.account.id=auditor-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if strings.Contains(string(port.last), accountAttrField) {
		t.Fatalf("admin/audit must see all accounts, query should be unscoped: %s", port.last)
	}
}

func TestShadowPassesThroughUnscoped(t *testing.T) {
	// Shadow mode: even a normal caller's query is NOT actually restricted —
	// the hole is only logged, not closed, until enforce flips on.
	h, port := handlerWith(shadow)
	rec := httptest.NewRecorder()
	h.SearchTraces(rec, searchReq(`{"query":{"match_all":{}}}`, "bkn.account.type=user,bkn.account.id=u-9"))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if strings.Contains(string(port.last), accountAttrField) {
		t.Fatalf("shadow mode must not restrict the query: %s", port.last)
	}
}

func TestShadowAllowsUnauthenticated(t *testing.T) {
	h, _ := handlerWith(shadow)
	rec := httptest.NewRecorder()
	h.SearchTraces(rec, searchReq(`{"query":{"match_all":{}}}`, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("shadow mode must not block missing identity, got %d", rec.Code)
	}
}

func TestEnforceRejectsAggregationsFromScopedCaller(t *testing.T) {
	// A global aggregation escapes the query scope; a scoped caller must not be
	// able to send one. Reject the whole request rather than silently drop aggs.
	h, port := handlerWith(enforce)
	rec := httptest.NewRecorder()
	body := `{"query":{"match_all":{}},"aggs":{"all":{"global":{},"aggs":{"docs":{"top_hits":{}}}}}}`
	h.SearchTraces(rec, searchReq(body, "bkn.account.type=user,bkn.account.id=u-9"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("scoped caller with aggs must be 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if port.last != nil {
		t.Fatal("nothing should reach OpenSearch when aggs are rejected")
	}
}

func TestAdminMayUseAggregations(t *testing.T) {
	// Admins are not scoped, so aggregations are fine for them.
	h, port := handlerWith(enforce)
	rec := httptest.NewRecorder()
	body := `{"aggs":{"by_svc":{"terms":{"field":"x"}}}}`
	h.SearchTraces(rec, searchReq(body, "bkn.account.type=admin,bkn.account.id=a-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin with aggs should pass, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(port.last), "aggs") {
		t.Fatalf("admin aggs should reach OpenSearch unchanged: %s", port.last)
	}
}

func TestRequireReadIdentityEnforceRejectsMissing(t *testing.T) {
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }
	mw := RequireReadIdentity(enforce, next)

	rec := httptest.NewRecorder()
	mw(rec, httptest.NewRequest(http.MethodGet, "/x", nil)) // no baggage
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("enforce + no identity: want 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("handler must not run when identity is missing under enforce")
	}
}

func TestRequireReadIdentityShadowPassesThrough(t *testing.T) {
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }
	mw := RequireReadIdentity(shadow, next)

	rec := httptest.NewRecorder()
	mw(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("shadow must pass through, got code=%d called=%v", rec.Code, called)
	}
}
