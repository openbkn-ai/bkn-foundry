// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func client(id, name, kind string) map[string]any {
	c := map[string]any{"client_id": id, "client_name": name, "redirect_uris": []string{"http://127.0.0.1:1/cb"},
		"created_at": "2026-10-23T08:59:56Z"}
	if kind != "" {
		c["metadata"] = map[string]any{"openbkn_kind": kind, "registered_from": "10.0.0.9"}
	}
	return c
}

func session(cl map[string]any, handled string) map[string]any {
	return map[string]any{
		"grant_scope":     []string{"openid", "offline"},
		"handled_at":      handled,
		"consent_request": map[string]any{"challenge": "", "client": cl, "subject": "u-1"},
	}
}

// fakeHydraAdmin serves two pages of consent sessions and clients, and records
// revocations and deletions.
func fakeHydraAdmin(t *testing.T) (*HydraAdmin, *[]string) {
	t.Helper()
	calls := &[]string{}
	wb := client("wb-1", "WorkBuddy Connector", "dynamic")
	cursor := client("cur-1", "Cursor", "dynamic")
	studio := client("openbkn-studio", "", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/oauth2/auth/sessions/consent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			*calls = append(*calls, "revoke "+r.URL.Query().Get("subject")+" "+r.URL.Query().Get("client"))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Query().Get("page_token") == "" {
			w.Header().Set("Link", `</admin/oauth2/auth/sessions/consent?page_size=500&page_token=p2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]any{
				session(wb, "2026-10-23T09:00:24Z"),
				session(studio, "2026-10-23T09:10:00Z"),
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]any{
			session(wb, "2026-10-23T10:00:00Z"),
			session(cursor, "2026-10-22T08:00:00Z"),
		})
	})
	mux.HandleFunc("/admin/clients", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{wb, studio, cursor})
	})
	mux.HandleFunc("/admin/clients/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/admin/clients/"):]
		var found map[string]any
		for _, c := range []map[string]any{wb, cursor, studio} {
			if c["client_id"] == id {
				found = c
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			*calls = append(*calls, "delete "+id)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(found)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewHydraAdmin(srv.URL), calls
}

func TestListConnectedClientsFollowsPagesAndKeepsLatestGrant(t *testing.T) {
	h, _ := fakeHydraAdmin(t)
	got, err := h.ListConnectedClients(context.Background(), "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d clients, want WorkBuddy and Cursor only: %+v", len(got), got)
	}
	if got[0].ClientID != "wb-1" || got[0].GrantedAt.Format("15:04") != "10:00" {
		t.Fatalf("first = %+v, want WorkBuddy's later grant", got[0])
	}
	if got[0].RegisteredFrom != "10.0.0.9" || len(got[0].Scopes) != 2 {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestListDynamicClientsLeavesFirstPartyOut(t *testing.T) {
	h, _ := fakeHydraAdmin(t)
	got, err := h.ListDynamicClients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if c.ClientID == "openbkn-studio" {
			t.Fatal("first-party client listed")
		}
	}
}

func TestRevokeAndDeleteOnlyTouchSelfRegisteredClients(t *testing.T) {
	h, calls := fakeHydraAdmin(t)
	ctx := context.Background()
	if err := h.RevokeConnectedClient(ctx, "u-1", "wb-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteDynamicClient(ctx, "cur-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"openbkn-studio", "missing"} {
		if err := h.RevokeConnectedClient(ctx, "u-1", id); !errors.Is(err, ErrNotDynamicClient) {
			t.Fatalf("revoke %s: %v", id, err)
		}
		if err := h.DeleteDynamicClient(ctx, id); !errors.Is(err, ErrNotDynamicClient) {
			t.Fatalf("delete %s: %v", id, err)
		}
	}
	want := []string{"revoke u-1 wb-1", "delete cur-1"}
	if len(*calls) != len(want) || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
}
