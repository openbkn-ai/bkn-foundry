// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package bknbackend

import (
	"context"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishedBindingCheckReadsActualNetworkWithoutBorrowingOperatorCredentials(t *testing.T) {
	for _, scenario := range []string{"match", "resource", "field", "primary_key", "property_type", "missing", "extra_entry"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/bkn-backend/in/v1/knowledge-networks/released/object-types/asset_doc" || r.URL.Query().Get("branch") != "main" || r.Header.Get("Authorization") != "" || r.Header.Get("x-account-id") != "publisher" || r.Header.Get("x-account-type") != "user" {
					t.Error("wrong native schema boundary")
				}
				resource, field, propertyType := "owned-resource", "f_number", "string"
				keys := []string{"number"}
				switch scenario {
				case "resource":
					resource = "foreign-resource"
				case "field":
					field = "other"
				case "primary_key":
					keys = []string{"other"}
				case "property_type":
					propertyType = "integer"
				case "missing":
					w.WriteHeader(404)
					return
				}
				entry := map[string]any{"id": "asset_doc", "data_source": map[string]string{"type": "resource", "id": resource}, "primary_keys": keys, "data_properties": []any{map[string]any{"name": "number", "type": propertyType, "mapped_field": map[string]string{"name": field}}}}
				entries := []any{entry}
				if scenario == "extra_entry" {
					entries = append(entries, entry)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
			}))
			defer server.Close()
			access := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
			record := &interfaces.KBPublication{NetworkID: "released", Publisher: interfaces.AccountInfo{ID: "publisher", Type: "user"}}
			draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "asset_doc", Adopted: true, Table: interfaces.DraftTable{ResourceID: "owned-resource"}, PrimaryKeys: []string{"number"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string", MappedField: "f_number"}}}}}}
			err := access.CheckPublishedBindings(context.Background(), record, draft)
			if (err == nil) != (scenario == "match") {
				t.Fatalf("changed binding accepted: %v", err)
			}
			if err != nil && err != kb.ErrPublishedBindingsChanged {
				t.Fatal("private backend details escaped binding check")
			}
		})
	}
}
