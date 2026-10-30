// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestActionEndpointsEscapePathSegments(t *testing.T) {
	ids := []struct{ raw, escaped string }{
		{"kn%1", "kn%251"},
		{"100%", "100%25"},
		{"a b", "a%20b"},
		{"a\n\tb", "a%0A%09b"},
		{"a/b?c#d", "a%2Fb%3Fc%23d"},
		{"normal-id_1", "normal-id_1"},
	}
	endpoints := []struct {
		name, method, path string
		call               func(*ontologyQueryClient, string) error
	}{
		{"QueryActions", http.MethodPost, "/action-types/%s", func(c *ontologyQueryClient, id string) error {
			_, err := c.QueryActions(context.Background(), &interfaces.QueryActionsRequest{KnID: id, AtID: id})
			return err
		}},
		{"ExecuteActions", http.MethodPost, "/action-types/%s/execute", func(c *ontologyQueryClient, id string) error {
			_, err := c.ExecuteActions(context.Background(), &interfaces.ExecuteActionsRequest{KnID: id, AtID: id})
			return err
		}},
		{"GetActionExecution", http.MethodGet, "/action-executions/%s", func(c *ontologyQueryClient, id string) error {
			_, err := c.GetActionExecution(context.Background(), &interfaces.GetActionExecutionRequest{KnID: id, ExecutionID: id})
			return err
		}},
		{"GetActionExecutionPaginated", http.MethodGet, "/action-logs/%s", func(c *ontologyQueryClient, id string) error {
			limit, offset := 10, 20
			_, err := c.GetActionExecution(context.Background(), &interfaces.GetActionExecutionRequest{
				KnID: id, ExecutionID: id, ResultsLimit: &limit, ResultsOffset: &offset,
			})
			return err
		}},
		{"ListActionExecutions", http.MethodGet, "/action-logs", func(c *ontologyQueryClient, id string) error {
			_, err := c.ListActionExecutions(context.Background(), &interfaces.ListActionExecutionsRequest{
				KnID: id, ActionTypeID: id, Status: "failed", TriggerType: "a b&c", Offset: 20, Limit: 10,
				SearchAfter: []any{"100%", "a b"},
			})
			return err
		}},
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, id := range ids {
				t.Run(id.escaped, func(t *testing.T) {
					client, mockHTTP, ctrl := newActionErrorClient(t)
					defer ctrl.Finish()
					check := func(rawURL string, query url.Values) {
						t.Helper()
						request, err := http.NewRequest(endpoint.method, rawURL, nil)
						if err != nil {
							t.Fatalf("URL cannot form a request: %v", err)
						}
						suffix := endpoint.path
						if strings.Contains(suffix, "%s") {
							suffix = fmt.Sprintf(suffix, id.escaped)
						}
						want := "/api/ontology-query/in/v1/knowledge-networks/" + id.escaped + suffix
						if got := request.URL.EscapedPath(); got != want {
							t.Errorf("EscapedPath = %q, want %q", got, want)
						}
						if endpoint.name == "GetActionExecutionPaginated" {
							if got := request.URL.RawQuery; got != "results_limit=10&results_offset=20" {
								t.Errorf("pagination query = %q", got)
							}
						} else if request.URL.RawQuery != "" {
							t.Errorf("unexpected query in path URL: %q", request.URL.RawQuery)
						}
						if request.URL.Fragment != "" {
							t.Errorf("unexpected fragment: %q", request.URL.Fragment)
						}
						if endpoint.name == "ListActionExecutions" {
							wantQuery := url.Values{
								"action_type_id": {id.raw}, "status": {"failed"}, "trigger_type": {"a b&c"},
								"offset": {"20"}, "limit": {"10"}, "search_after": {"100%,a b"}, "need_total": {"true"},
							}
							if query.Encode() != wantQuery.Encode() {
								t.Errorf("query = %v, want %v", query, wantQuery)
							}
							parsed, err := url.ParseQuery(query.Encode())
							if err != nil || parsed.Get("action_type_id") != id.raw {
								t.Errorf("query did not round trip: %v, %v", parsed, err)
							}
						}
					}
					if endpoint.method == http.MethodPost {
						mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
							DoAndReturn(func(_ context.Context, rawURL string, _ map[string]string, _ any) (int, []byte, error) {
								check(rawURL, nil)
								return http.StatusOK, []byte(`{}`), nil
							})
					} else {
						mockHTTP.EXPECT().GetBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
							DoAndReturn(func(_ context.Context, rawURL string, query url.Values, _ map[string]string) (int, []byte, error) {
								check(rawURL, query)
								return http.StatusOK, []byte(`{}`), nil
							})
					}
					if err := endpoint.call(client, id.raw); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestQueryActionsMalformedIDReachesDownstreamNotFound(t *testing.T) {
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		if r.Method != http.MethodPost || r.Header.Get("x-http-method-override") != "GET" {
			t.Errorf("unexpected request: %s, override %q", r.Method, r.Header.Get("x-http-method-override"))
		}
		if got := r.URL.EscapedPath(); got != "/in/v1/knowledge-networks/kn%251/action-types/100%25" {
			t.Errorf("downstream path = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":"OntologyQuery.ActionType.ActionTypeNotFound","description":"Action type does not exist"}`))
	}))
	defer server.Close()
	client := &ontologyQueryClient{
		logger: &mockLogger{}, baseURL: server.URL,
		httpClient: rest.NewHTTPClient(),
	}
	_, err := client.QueryActions(context.Background(), &interfaces.QueryActionsRequest{KnID: "kn%1", AtID: "100%"})
	if !called.Load() {
		t.Fatal("request did not reach downstream")
	}
	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusNotFound || !strings.Contains(fmt.Sprint(he.ErrorDetails), "ActionTypeNotFound") {
		t.Fatalf("expected downstream semantic 404, got %v", he)
	}
}
