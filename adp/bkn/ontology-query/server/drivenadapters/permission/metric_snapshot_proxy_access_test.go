// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
)

func TestMetricSnapshotManagedProxyAccess(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"active", `{"proxy_account_id":"proxy","account_type":"app","managed_by":"bkn","managed_resource_type":"knowledge_network","managed_resource_id":"kn","lifecycle_status":"active","enabled":true}`, 200, false},
		{"deleted", ``, 404, false},
		{"outage", `{}`, 503, true},
		{"empty", ``, 200, true},
		{"malformed", `{`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/api/safe/in/v1/managed-proxy-accounts/proxy" {
					t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			access := NewMetricSnapshotProxyAccess(&common.AppSetting{BknSafeURL: server.URL})
			state, err := access.GetManagedProxy(context.Background(), "proxy")
			if (err != nil) != test.wantError {
				t.Fatalf("state=%#v err=%v", state, err)
			}
			if test.name == "active" && (state == nil || !state.Enabled || state.ManagedResourceID != "kn") {
				t.Fatalf("state=%#v", state)
			}
			if test.name == "deleted" && state != nil {
				t.Fatalf("deleted proxy=%#v", state)
			}
		})
	}
}
