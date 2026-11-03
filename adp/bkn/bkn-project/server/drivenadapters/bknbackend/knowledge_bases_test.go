// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

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

func TestKnowledgeBaseMountUsesExistingCapabilityAPIAndRequiresCompleteAcknowledgement(t *testing.T) {
	for _, scenario := range []string{"complete", "partial", "wrong_type", "forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/bkn-backend/v1/knowledge-networks/release/capabilities" || r.URL.Query().Get("branch") != "main" || r.Header.Get("Authorization") != token {
					t.Error("wrong mount boundary")
				}
				var body struct {
					Capabilities []struct {
						Type string `json:"capability_type"`
						ID   string `json:"capability_id"`
					} `json:"capabilities"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Capabilities) != 2 || body.Capabilities[0].Type != "knowledge_base" || body.Capabilities[0].ID != "1" || body.Capabilities[1].ID != "2" {
					t.Error("unexpected mount request")
				}
				if scenario == "forbidden" {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"error_code":"Denied","description":"Denied"}`))
					return
				}
				second := `,{"id":"binding-2","capability_type":"knowledge_base","capability_id":"2"}`
				if scenario == "partial" {
					second = ""
				}
				if scenario == "wrong_type" {
					second = `,{"id":"binding-2","capability_type":"skill","capability_id":"2"}`
				}
				_, _ = w.Write([]byte(`{"entries":[{"id":"binding-1","capability_type":"knowledge_base","capability_id":"1"}` + second + `],"total_count":2}`))
			}))
			defer server.Close()
			access := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
			err := access.MountKnowledgeBases(context.Background(), token, "release", []string{"2", "1", "1"})
			if (err == nil) != (scenario == "complete") {
				t.Fatal("invalid mount acknowledgement", err)
			}
			if scenario == "forbidden" {
				var denied *interfaces.BknBackendError
				if !errors.As(err, &denied) || denied.HTTPCode != 403 {
					t.Fatal("permission failure lost", err)
				}
			}
		})
	}
}
