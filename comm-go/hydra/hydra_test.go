// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package hydra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIntrospectJSON(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		active         bool
		wantErr        bool
	}{
		{"active app", `{"active":true,"sub":"app-1","scope":"read","client_id":"app-1"}`, true, false},
		{"inactive", `{"active":false}`, false, false},
		{"invalid JSON", `{"active":`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/admin/oauth2/introspect" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("token") != "test-token" {
					t.Errorf("unexpected form: %v, %v", r.Form, err)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			client := &hydra{adminAddress: server.URL, client: server.Client()}
			result, err := client.Introspect(context.Background(), "test-token")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Introspect error = %v", err)
			}
			if result.Active != tc.active {
				t.Fatalf("active = %v, want %v", result.Active, tc.active)
			}
			if tc.active && (result.VisitorID != "app-1" || result.VisitorTyp != VisitorType_App || result.Scope != "read") {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}
