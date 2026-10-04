// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package driveradapters

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

func TestLifecycleMiddlewarePreservesKnownRequestTargetsWithoutEvidence(t *testing.T) {
	for _, test := range []struct {
		name, tool, input, headerKN string
		want                        []bkntrace.BusinessRef
	}{
		{"cypher", "run_cypher", `"kn_id":"supply","query":"RETURN 1"`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}},
		{"cypher header fallback", "run_cypher", `"query":"RETURN 1"`, "supply", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}},
		{"explicit network wins", "run_cypher", `"kn_id":"supply","query":"RETURN 1"`, "other", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}},
		{"object", "query_object_instance", `"kn_id":"supply","ot_id":"bom"`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "object_type", RefID: "object:supply:bom", Version: "unversioned"}}},
		{"exploration source", "explore_subgraph", `"kn_id":"supply","source_object_type_id":"bom"`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "object_type", RefID: "object:supply:bom", Version: "unversioned"}}},
		{"semantic search scope", "search_instance", `"kn_id":"supply","query":"库存"`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}},
		{"subgraph fixed targets", "query_instance_subgraph", `"kn_id":"supply","relation_type_paths":[{"object_types":[{"id":"bom"}],"relation_types":[{"relation_type_id":"uses"}]}]`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "object_type", RefID: "object:supply:bom", Version: "unversioned"}, {RefType: "relation_type", RefID: "relation:supply:uses", Version: "unversioned"}}},
		{"metric", "query_metric", `"kn_id":"supply","metric_id":"inventory"`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "metric", RefID: "metric:supply:inventory", Version: "unversioned"}}},
		{"schema", "get_object_types", `"kn_id":"supply","ot_ids":["display name"]`, "", []bkntrace.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}},
		{"execution only", "execute_tool", `"kn_id":"supply","tool_id":"tool"`, "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var finishedRefs []bkntrace.BusinessRef
			client := bkntrace.NewLifecycleClient("http://core.test", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var value any
				switch {
				case strings.HasSuffix(r.URL.Path, "/interactions/int-1"):
					value = bkntrace.Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1}
				case strings.HasSuffix(r.URL.Path, "/operations:ensure"):
					value = bkntrace.OperationResult{Created: true, Execute: true, Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "pending"}}
				case strings.HasSuffix(r.URL.Path, "/attempts/1:fail"):
					var body struct {
						BusinessRefs []bkntrace.BusinessRef `json:"business_refs"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					finishedRefs = body.BusinessRefs
					value = bkntrace.OperationResult{Operation: bkntrace.Operation{OperationID: "op-1", Attempt: 1, AttemptStatus: "failed"}, Receipt: bkntrace.Receipt{ReceiptID: "receipt-1", ReceiptStatus: "failed"}}
				default:
					t.Fatalf("unexpected lifecycle call: %s", r.URL.Path)
				}
				raw, _ := json.Marshal(value)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})})
			router := gin.New()
			router.Use(trustedLifecycleHTTPContext(), middlewareLifecycle(client))
			route := "/kn/" + test.tool
			router.POST(route, func(c *gin.Context) { c.JSON(http.StatusBadGateway, gin.H{"error": "dependency unavailable"}) })
			request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{`+test.input+`,"bkn_context":{"conversation_id":"conv-1","interaction_id":"int-1","business_refs":[{"ref_type":"object_type","ref_id":"object:supply:invented"}]}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Kn-ID", test.headerKN)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("business response changed: status=%d body=%s", response.Code, response.Body.String())
			}
			if !reflect.DeepEqual(finishedRefs, test.want) {
				t.Fatalf("request target refs = %#v, want %#v", finishedRefs, test.want)
			}
		})
	}
}
