// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestBOMIntersectionRequiresApprovedExecutedCode(t *testing.T) {
	approved := BOMProducer{KnowledgeNetworkID: "supply", ToolboxID: "box", ToolID: "bom_shared_list", FunctionVersion: "published-version", CodeSHA256: "sha256:" + strings.Repeat("a", 64)}
	for _, name := range []string{"normal", "zero", "changed code", "changed version", "body spoof", "missing count", "truncated", "wrong products", "duplicate materials", "artifact unavailable", "wrong data source", "null count", "null substitute"} {
		t.Run(name, func(t *testing.T) {
			publisher, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "agent-retrieval", BaseStreamID: "retrieval", WorkloadIdentity: "retrieval", ProcessBootID: name, CapturePolicyRevision: "41"}, &captureEvidenceSender{})
			if err != nil {
				t.Fatal(err)
			}
			defer publisher.Close(context.Background())
			SetEvidencePublisher(publisher)
			defer SetEvidencePublisher(nil)
			previous := artifactHTTPClient
			t.Cleanup(func() { artifactHTTPClient = previous })
			writes := 0
			var artifact map[string]any
			artifactHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				writes++
				body, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(body, &artifact); err != nil {
					t.Fatal(err)
				}
				status := http.StatusCreated
				if name == "artifact unavailable" {
					status = http.StatusForbidden
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			t.Setenv(envArtifactEndpoint, "http://trace.local/api/agent-observability/v1/evidence/artifacts")
			t.Setenv(envArtifactToken, "fixture")
			proof := map[string]any{"toolbox_id": "box", "tool_id": "bom_shared_list", "function_version": approved.FunctionVersion, "code_sha256": approved.CodeSHA256}
			conclusion := map[string]any{"products": []string{"P1", "P2"}, "include_substitute": false, "depth": nil, "shared_codes": []string{"M1", "M2"}, "shared_count": 2, "caliber": "structure_intersect_only"}
			conclusion["data_source"] = map[string]any{"knowledge_network_id": "supply", "loaded_datasets": []string{"bom"}}
			body := map[string]any{"exit_code": 0, "result": conclusion}
			result := map[string]any{"status_code": 200, "function_execution": proof, "body": body}
			switch name {
			case "null count":
				conclusion["shared_codes"] = []string{}
				conclusion["shared_count"] = nil
			case "null substitute":
				conclusion["include_substitute"] = nil
			case "zero":
				conclusion["shared_codes"] = []string{}
				conclusion["shared_count"] = 0
			case "wrong data source":
				conclusion["data_source"] = map[string]any{"knowledge_network_id": "another-network", "loaded_datasets": []string{"bom"}}
			case "changed code":
				proof["code_sha256"] = "sha256:" + strings.Repeat("b", 64)
			case "changed version":
				proof["function_version"] = "other-version"
			case "body spoof":
				delete(result, "function_execution")
				body["function_execution"] = proof
			case "missing count":
				delete(conclusion, "shared_count")
				conclusion["shared_codes"] = []string{}
			case "truncated":
				conclusion["truncated"] = true
			case "wrong products":
				conclusion["products"] = []string{"P1", "P3"}
			case "duplicate materials":
				conclusion["shared_codes"] = []string{"M1", "M1"}
			}
			ctx := common.SetAccountAuthContextToCtx(testTraceContext(), &interfaces.AccountAuthContext{AccountID: "acct_demo", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "app"}})
			trace, _ := common.GetTraceContextFromCtx(ctx)
			trace.ConversationID = "conv-bom"
			ctx = common.SetTraceContextToCtx(ctx, trace)
			err = RecordBOMIntersection(ctx, approved, approved.FunctionVersion, map[string]any{"products": []string{"P1", "P2"}}, result)
			good := name == "normal" || name == "zero"
			if good {
				if err != nil || writes != 1 || len(publisher.SnapshotQueue()) != 1 {
					t.Fatalf("err=%v writes=%d records=%d", err, writes, len(publisher.SnapshotQueue()))
				}
				if artifact["artifact_type"] != "logic_execution" {
					t.Fatal(artifact)
				}
				var wire struct {
					EventType string `json:"event_type"`
					Claims    []any  `json:"claims"`
					Envelope  struct {
						Payload struct {
							Contract string `json:"result_contract"`
							Hash     string `json:"content_hash"`
						} `json:"payload"`
					} `json:"envelope"`
				}
				if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &wire); err != nil {
					t.Fatal(err)
				}
				if wire.EventType != "logic.execution.observed" || len(wire.Claims) != 0 || wire.Envelope.Payload.Contract != BOMIntersectionContract || wire.Envelope.Payload.Hash != artifact["content_hash"] {
					t.Fatalf("wire=%+v", wire)
				}
			} else if err == nil || len(publisher.SnapshotQueue()) != 0 {
				t.Fatalf("unverified result emitted evidence: %v", err)
			}
		})
	}
	raw, _ := json.Marshal([]BOMProducer{approved})
	t.Setenv("BKN_TRACE_BOM_SHARED_PRODUCERS", string(raw))
	if _, ok := ConfiguredBOMProducer("supply", "box", "bom_shared_list"); !ok {
		t.Fatal("approved identity not selected")
	}
	if _, ok := ConfiguredBOMProducer("other-network", "box", "bom_shared_list"); ok {
		t.Fatal("approval crossed knowledge-network boundary")
	}
}
