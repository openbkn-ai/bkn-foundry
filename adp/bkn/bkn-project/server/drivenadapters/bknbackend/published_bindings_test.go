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
	"strings"
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

func TestPublishedDocumentBindingsUseCompilerIdentityMirrors(t *testing.T) {
	for _, domain := range []string{"supply", "patent", "equipment"} {
		for _, scenario := range []string{"match", "resource", "primary_key", "field", "property_type", "direct_edge", "source_edge", "target_edge", "unsafe_mirror"} {
			t.Run(domain+"/"+scenario, func(t *testing.T) {
				alias := "bkn_record_id_2"
				fileID, blockID := domain+"_file", domain+"_block"
				file := &interfaces.DraftObjectType{ID: fileID, Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "file"}, Table: interfaces.DraftTable{ResourceID: "file-resource"}, PrimaryKeys: []string{"_id"}, Properties: []*interfaces.DraftProperty{
					{Name: "_id", Type: "string", MappedField: alias, ReadOnly: true},
					{Name: "bkn_record_id", Type: "string", MappedField: "f_business"},
					{Name: "number", Type: "string", MappedField: "f_number"},
				}}
				block := &interfaces.DraftObjectType{ID: blockID, Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_block"}, Table: interfaces.DraftTable{ResourceID: "block-resource"}, PrimaryKeys: []string{"_id"}, Properties: []*interfaces.DraftProperty{
					{Name: "_id", Type: "string", MappedField: "bkn_record_id", ReadOnly: true},
					{Name: "document_id", Type: "string", MappedField: "document_id"},
				}}
				direct := &interfaces.DraftRelationType{ID: "contains", Adopted: true, Source: fileID, Target: blockID, Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "_id", TargetProperty: "document_id"}}}
				indirect := &interfaces.DraftRelationType{ID: "mentions", Adopted: true, Source: fileID, Target: blockID, LinkTable: &interfaces.DraftTable{ResourceID: "edge-resource"}, SourceMapping: []interfaces.DraftFieldMapping{{SourceProperty: "_id", TargetProperty: "source_record_id"}}, TargetMapping: []interfaces.DraftFieldMapping{{SourceProperty: "target_record_id", TargetProperty: "_id"}}}
				draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{file, block}, RelationTypes: []*interfaces.DraftRelationType{direct, indirect}}}
				property := func(name, field string) map[string]any {
					return map[string]any{"name": name, "type": "string", "mapped_field": map[string]string{"name": field}}
				}
				pair := func(source, target string) map[string]any {
					return map[string]any{"source_property": map[string]string{"name": source}, "target_property": map[string]string{"name": target}}
				}
				fileProperties := []any{property(alias, alias), property("bkn_record_id", "f_business"), property("number", "f_number")}
				fileWire := map[string]any{"id": fileID, "data_source": map[string]string{"type": "resource", "id": "file-resource"}, "primary_keys": []string{alias}, "data_properties": fileProperties}
				blockWire := map[string]any{"id": blockID, "data_source": map[string]string{"type": "resource", "id": "block-resource"}, "primary_keys": []string{"bkn_record_id"}, "data_properties": []any{property("bkn_record_id", "bkn_record_id"), property("document_id", "document_id")}}
				directWire := map[string]any{"id": "contains", "type": "direct", "source_object_type_id": fileID, "target_object_type_id": blockID, "mapping_rules": []any{pair(alias, "document_id")}}
				indirectRules := map[string]any{"backing_data_source": map[string]string{"type": "resource", "id": "edge-resource"}, "source_mapping_rules": []any{pair(alias, "source_record_id")}, "target_mapping_rules": []any{pair("target_record_id", "bkn_record_id")}, "properties": []any{}}
				indirectWire := map[string]any{"id": "mentions", "type": "indirect", "source_object_type_id": fileID, "target_object_type_id": blockID, "mapping_rules": indirectRules}
				switch scenario {
				case "resource":
					fileWire["data_source"] = map[string]string{"type": "resource", "id": "foreign"}
				case "primary_key":
					fileWire["primary_keys"] = []string{"bkn_record_id"}
				case "field":
					fileProperties[2] = property("number", "foreign")
				case "property_type":
					fileProperties[2].(map[string]any)["type"] = "integer"
				case "direct_edge":
					directWire["mapping_rules"] = []any{pair("_id", "document_id")}
				case "source_edge":
					indirectRules["source_mapping_rules"] = []any{pair("_id", "source_record_id")}
				case "target_edge":
					indirectRules["target_mapping_rules"] = []any{pair("target_record_id", "_id")}
				case "unsafe_mirror":
					file.Properties[0].ReadOnly = false
				}
				before, _ := json.Marshal(draft)
				wire := map[string]any{"/object-types/" + fileID: fileWire, "/object-types/" + blockID: blockWire, "/relation-types/contains": directWire, "/relation-types/mentions": indirectWire}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					prefix := "/api/bkn-backend/in/v1/knowledge-networks/released"
					entry := wire[strings.TrimPrefix(r.URL.Path, prefix)]
					if entry == nil {
						t.Error("unexpected schema request")
						w.WriteHeader(404)
						return
					}
					if r.Header.Get("Authorization") != "" || r.Header.Get("x-account-id") != "publisher" {
						t.Error("borrowed credentials or lost publisher")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"entries": []any{entry}})
				}))
				defer server.Close()
				access := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
				record := &interfaces.KBPublication{NetworkID: "released", Publisher: interfaces.AccountInfo{ID: "publisher", Type: "user"}}
				err := access.CheckPublishedBindings(context.Background(), record, draft)
				if (err == nil) != (scenario == "match") {
					t.Fatalf("wrong published mirror decision: %v", err)
				}
				if err != nil && err != kb.ErrPublishedBindingsChanged {
					t.Fatal("private error escaped")
				}
				after, _ := json.Marshal(draft)
				if string(before) != string(after) {
					t.Fatal("binding check mutated logical model")
				}
			})
		}
	}
}
