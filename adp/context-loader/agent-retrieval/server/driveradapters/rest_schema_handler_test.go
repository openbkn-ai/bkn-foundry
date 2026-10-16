// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	aerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type schemaAuth struct{}

func (schemaAuth) Introspect(_ context.Context, token string) (*interfaces.TokenInfo, error) {
	if token != "token-a" && token != "token-b" {
		return nil, &aerrors.HTTPError{HTTPCode: 401, Code: "Unauthorized"}
	}
	return &interfaces.TokenInfo{VisitorID: "user-" + strings.TrimPrefix(token, "token-"), VisitorTyp: interfaces.RealName}, nil
}

type schemaReadFunc func(context.Context, string, string, url.Values) (int, []byte, error)

func (f schemaReadFunc) ReadSchema(ctx context.Context, kn, collection string, query url.Values) (int, []byte, error) {
	return f(ctx, kn, collection, query)
}

func TestSchemaSandboxRoutesAuthenticateAndPreserveRawReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Include fields that the agent-facing structs would otherwise discard and a
	// large integer that must never pass through float64 decoding.
	const raw = `{"id":"kn","display_key":"name","analysis_dimensions":[{"name":"region"}],"mapping_rules":{"resource":"r"},"future":9007199254740993}`
	for _, route := range []struct{ path, collection string }{
		{"/kn?branch=dev", ""}, {"/kn/object-types?branch=dev&limit=-1", "object-types"},
		{"/kn/relation-types?branch=dev&limit=-1", "relation-types"},
		{"/kn/object-metrics?branch=dev&status=published&limit=500&offset=0", "object-metrics"},
	} {
		t.Run(route.collection, func(t *testing.T) {
			calls := 0
			handler := &restSchemaHandler{Hydra: schemaAuth{}, Schema: schemaReadFunc(func(ctx context.Context, kn, collection string, q url.Values) (int, []byte, error) {
				calls++
				auth, ok := common.GetAccountAuthContextFromCtx(ctx)
				if !ok || auth.AccountID != "user-a" || kn != "kn" || collection != route.collection || q.Get("branch") != "dev" {
					t.Fatalf("wrong authenticated read: %#v %s %s %v", auth, kn, collection, q)
				}
				if collection == "object-metrics" {
					if q.Get("limit") != "500" || q.Get("offset") != "0" || q.Get("status") != "published" {
						t.Fatal("V1 metric pagination lost")
					}
				} else if collection != "" && q.Get("limit") != "-1" {
					t.Fatal("list limit lost")
				}
				return 200, []byte(raw), nil
			})}
			router := gin.New()
			handler.RegisterRouter(router.Group("/api/bkn-backend/v1"))
			for range 2 {
				req := httptest.NewRequest("GET", "/api/bkn-backend/v1/knowledge-networks"+route.path, nil)
				req.Header.Set("Authorization", "Bearer token-a")
				req.Header.Set("x-account-id", "spoofed-admin")
				req.Header.Set("user_id", "spoofed-admin")
				out := httptest.NewRecorder()
				router.ServeHTTP(out, req)
				if out.Code != 200 || out.Body.String() != raw {
					t.Fatalf("raw schema changed: %d %s", out.Code, out.Body)
				}
			}
			if calls != 2 {
				t.Fatalf("schema was deduplicated: %d calls", calls)
			}
		})
	}
}

func TestSchemaSandboxRejectsUnsafeRoutesAndQueries(t *testing.T) {
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/knowledge-networks/kn", "", 401},
		{"GET", "/knowledge-networks/kn", "bad-token", 401},
		{"POST", "/knowledge-networks/kn", "token-a", 404},
		{"DELETE", "/knowledge-networks/kn", "token-a", 404},
		{"GET", "/knowledge-networks/kn/action-types", "token-a", 404},
		{"GET", "/knowledge-networks/kn/object-types/ot", "token-a", 404},
		{"GET", "/knowledge-networks/kn?mode=export", "token-a", 400},
		{"GET", "/knowledge-networks/kn?branch=%ZZ", "token-a", 400},
		{"GET", "/knowledge-networks/..?branch=main", "token-a", 400},
	} {
		t.Run(tc.method+tc.path+tc.token, func(t *testing.T) {
			handler := &restSchemaHandler{Hydra: schemaAuth{}, Schema: schemaReadFunc(func(context.Context, string, string, url.Values) (int, []byte, error) {
				t.Fatal("unexpected backend call")
				return 0, nil, nil
			})}
			router := gin.New()
			handler.RegisterRouter(router.Group("/api/bkn-backend/v1"))
			req := httptest.NewRequest(tc.method, "/api/bkn-backend/v1"+tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			req.Header.Set("x-account-id", "spoofed-admin")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("got %d: %s", out.Code, out.Body)
			}
		})
	}
}

func TestSchemaSandboxKeepsPermissionFailuresAndHidesTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		token  string
		status int
		body   string
		err    error
		want   int
	}{
		{"token-a", 200, `{"id":"kn"}`, nil, 200},
		{"token-b", 403, `{"error_code":"Forbidden"}`, nil, 403},
		{"token-a", 404, `{"error_code":"NotFound"}`, nil, 404},
		{"token-a", 302, "redirect", nil, 502},
		{"token-a", 0, "", errors.New("credential-secret"), 502},
	} {
		t.Run(tc.token+tc.body, func(t *testing.T) {
			handler := &restSchemaHandler{Hydra: schemaAuth{}, Schema: schemaReadFunc(func(ctx context.Context, _ string, _ string, _ url.Values) (int, []byte, error) {
				auth, _ := common.GetAccountAuthContextFromCtx(ctx)
				if auth.AccountID != "user-"+strings.TrimPrefix(tc.token, "token-") {
					t.Fatal("identity changed")
				}
				return tc.status, []byte(tc.body), tc.err
			})}
			router := gin.New()
			handler.RegisterRouter(router.Group("/api/bkn-backend/v1"))
			req := httptest.NewRequest("GET", "/api/bkn-backend/v1/knowledge-networks/kn", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != tc.want || strings.Contains(out.Body.String(), "credential-secret") {
				t.Fatalf("bad error mapping: %d %s", out.Code, out.Body)
			}
			if tc.err == nil && tc.want != 502 && out.Body.String() != tc.body {
				t.Fatal("backend response changed")
			}
		})
	}
}

func TestSchemaSandboxAppKeyUsesVerifiedOwner(t *testing.T) {
	called := false
	handler := &restSchemaHandler{
		Hydra: schemaAuth{}, AppKeys: stubAppKeys{called: &called},
		Schema: schemaReadFunc(func(ctx context.Context, _ string, _ string, _ url.Values) (int, []byte, error) {
			auth, _ := common.GetAccountAuthContextFromCtx(ctx)
			if auth.AccountID != "appkey-owner" {
				t.Fatalf("wrong AppKey owner: %#v", auth)
			}
			return 200, []byte(`{"id":"kn"}`), nil
		}),
	}
	router := gin.New()
	handler.RegisterRouter(router.Group("/api/bkn-backend/v1"))
	req := httptest.NewRequest("GET", "/api/bkn-backend/v1/knowledge-networks/kn", nil)
	req.Header.Set("Authorization", "Bearer bak_test")
	req.Header.Set("x-account-id", "spoofed-admin")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != 200 || !called {
		t.Fatalf("AppKey did not reach verifier: %d %v", out.Code, called)
	}
}
