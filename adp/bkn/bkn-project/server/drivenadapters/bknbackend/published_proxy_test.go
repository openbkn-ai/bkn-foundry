// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishedProxyRequiresCurrentAcknowledgedMapping(t *testing.T) {
	for _, scenario := range []string{"ready", "wrong_network", "archived", "wrong_type", "unsynced", "missing_version", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/bkn-backend/in/v1/knowledge-networks/network/proxy-account" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected proxy lookup boundary")
				}
				if scenario == "denied" {
					w.WriteHeader(403)
					return
				}
				body := map[string]any{"kn_id": "network", "proxy_account_id": "proxy", "proxy_account_type": "app", "lifecycle_status": "active", "version": 7, "sync_status": "ready", "published_model_version": "model", "synced_model_version": "model"}
				switch scenario {
				case "wrong_network":
					body["kn_id"] = "other"
				case "archived":
					body["lifecycle_status"] = "archived"
				case "wrong_type":
					body["proxy_account_type"] = "user"
				case "unsynced":
					body["synced_model_version"] = "old"
				case "missing_version":
					delete(body, "version")
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			access := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
			identity, err := access.PublishedProxy(context.Background(), "network")
			if (err == nil) != (scenario == "ready") {
				t.Fatalf("mapping accepted incorrectly: %+v %v", identity, err)
			}
		})
	}
}
