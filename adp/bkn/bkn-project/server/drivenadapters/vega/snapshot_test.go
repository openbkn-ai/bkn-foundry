// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestQuerySnapshotPreservesStableCursorAuthorityAndExactNumbers(t *testing.T) {
	for _, scenario := range []string{"valid", "limit_exceeded", "missing_paging", "missing_cursor", "empty_advance", "revoked", "source_denied"} {
		t.Run(scenario, func(t *testing.T) {
			calls, closed := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-account-id") != "operator" || r.Header.Get("x-account-type") != "user" {
					t.Error("query borrowed a service identity")
				}
				if r.Method == http.MethodDelete {
					closed++
					w.WriteHeader(204)
					return
				}
				calls++
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid query request")
				}
				if calls == 1 {
					if body["query"] != "SELECT id FROM {{source}}" || body["query_format"] != "sql" {
						t.Error("initial query changed")
					}
				} else {
					paging, _ := body["paging"].(map[string]any)
					if len(body) != 1 || len(paging) != 1 || paging["cursor"] != "stable-session" {
						t.Error("cursor authority or continuation contract changed")
					}
				}
				if scenario == "source_denied" && calls == 2 {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"error_code":"Forbidden"}`))
					return
				}
				rows := []map[string]any{{"id": json.Number("18446744073709551615"), "page": calls}}
				var next any
				if calls < 3 {
					next = "stable-session"
				}
				if scenario == "empty_advance" {
					rows = []map[string]any{}
				}
				page := map[string]any{"entries": rows, "paging": map[string]any{"next_cursor": next}}
				if scenario == "missing_paging" {
					delete(page, "paging")
				}
				if scenario == "missing_cursor" {
					page["paging"] = map[string]any{}
				}
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			client := &vegaAccess{baseUrl: server.URL, httpClient: rest.NewHTTPClient()}
			limit := 10
			if scenario == "limit_exceeded" {
				limit = 2
			}
			rows, err := client.QuerySnapshot(context.Background(), interfaces.VegaAccount{ID: "operator", Type: "user"}, "SELECT id FROM {{source}}", 30, limit, func(context.Context) error {
				if scenario == "revoked" && calls > 0 {
					return errors.New("source changed")
				}
				return nil
			})
			if (err == nil) != (scenario == "valid") {
				t.Fatal("partial or unauthorized snapshot accepted", scenario, err)
			}
			if scenario == "valid" {
				if len(rows) != 3 || rows[0]["id"] != json.Number("18446744073709551615") {
					t.Fatal("snapshot lost pages or numeric identity")
				}
			} else if rows != nil {
				t.Fatal("partial rows escaped")
			}
			if calls > 1 && closed != 1 {
				t.Fatal("cursor was not released")
			}
			if scenario == "source_denied" {
				var denied *interfaces.VegaError
				if !errors.As(err, &denied) || denied.HTTPCode != 403 {
					t.Fatal("source denial was obscured", err)
				}
			}
			if scenario == "revoked" && calls != 1 {
				t.Fatal("revoked source fetched another page")
			}
		})
	}
}
