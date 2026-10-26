// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestFunctionResultRequiresVerifiedExecutionIdentity(t *testing.T) {
	identity := ResultProducer{KnowledgeNetworkID: "supply", ToolboxID: "box", ToolID: "quality_check", FunctionVersion: "published-version", CodeSHA256: "sha256:" + strings.Repeat("a", 64)}
	for _, name := range []string{"normal", "zero", "changed code", "invalid code hash", "changed version", "body spoof", "missing result", "null result", "text result", "empty object", "array result", "embedded declaration", "artifact unavailable", "artifact unsafe error", "artifact timeout", "closed publication", "sample fixture", "null exit", "numeric content"} {
		t.Run(name, func(t *testing.T) {
			identity := identity
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
				if name == "artifact unsafe error" {
					return nil, errors.New("must-not-log-secret-response")
				}
				if name == "artifact timeout" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
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
			proof := map[string]any{"toolbox_id": "box", "tool_id": "quality_check", "function_version": identity.FunctionVersion, "code_sha256": identity.CodeSHA256}
			content := map[string]any{"measurement": "passed", "count": 2}
			if name == "zero" {
				content["count"] = 0
			}
			nativeResult := content
			body := map[string]any{"exit_code": 0, "result": nativeResult}
			result := map[string]any{"status_code": 200, "function_execution": proof, "body": body}
			switch name {
			case "changed code":
				proof["code_sha256"] = "sha256:" + strings.Repeat("b", 64)
			case "invalid code hash":
				proof["code_sha256"] = "unverified"
			case "changed version":
				proof["function_version"] = "other-version"
			case "body spoof":
				delete(result, "function_execution")
				body["function_execution"] = proof
			case "null exit":
				body["exit_code"] = nil
			case "numeric content":
				body["result"] = json.RawMessage(`{"count":9007199254740993,"text":"<verified>"}`)
			case "missing result":
				delete(body, "result")
			case "null result":
				body["result"] = nil
			case "text result":
				body["result"] = "unstructured answer"
			case "empty object":
				body["result"] = map[string]any{}
			case "array result":
				body["result"] = []any{1, 2}
			case "embedded declaration":
				nativeResult["trace_evidence"] = map[string]any{"claim_type": "untrusted/v1", "complete": false}

			}
			var fixtureProof map[string]any
			if name == "sample fixture" {
				raw, err := os.ReadFile("../../../../../../bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore/testdata/sample-function-result.json")
				if err != nil {
					t.Fatal(err)
				}
				var fixture struct {
					Output struct {
						Inline map[string]any `json:"inline"`
					} `json:"output"`
					Source struct {
						Envelope struct {
							Payload map[string]any `json:"payload"`
						} `json:"envelope"`
					} `json:"source"`
				}
				if err := json.Unmarshal(raw, &fixture); err != nil {
					t.Fatal(err)
				}
				fixtureProof = fixture.Source.Envelope.Payload
				producerRaw, _ := json.Marshal(fixtureProof["producer"])
				if err := json.Unmarshal(producerRaw, &identity); err != nil {
					t.Fatal(err)
				}
				result = fixture.Output.Inline
			}
			ctx := common.SetAccountAuthContextToCtx(testTraceContext(), &interfaces.AccountAuthContext{AccountID: "acct_demo", AccountType: interfaces.AccessorTypeUser, TokenInfo: &interfaces.TokenInfo{ClientID: "app"}})
			trace, _ := common.GetTraceContextFromCtx(ctx)
			trace.ConversationID = "conv-result"
			ctx = common.SetTraceContextToCtx(ctx, trace)
			ctx = WithTraceAvailability(ctx)
			if name == "closed publication" {
				ctx = withEvidenceOutcome(ctx)
				evidenceOutcomeFromContext(ctx).closed = true
			}
			if name == "artifact timeout" {
				ctx.Value(traceAvailabilityKey{}).(*traceAvailability).post = 300 * time.Millisecond
			}
			capture, captureErr := os.CreateTemp(t.TempDir(), "snapshot-warning")
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			previousStdout := os.Stdout
			os.Stdout = capture
			t.Cleanup(func() { os.Stdout = previousStdout; _ = capture.Close() })
			err = RecordFunctionResult(ctx, identity, identity.FunctionVersion, result)
			os.Stdout = previousStdout
			if _, seekErr := capture.Seek(0, 0); seekErr != nil {
				t.Fatal(seekErr)
			}
			warnings, readErr := io.ReadAll(capture)
			if readErr != nil {
				t.Fatal(readErr)
			}
			runtimeFailure := name == "artifact unavailable" || name == "artifact unsafe error" || name == "artifact timeout"
			if strings.Contains(string(warnings), "stage=publish") != (name == "closed publication") {
				t.Fatalf("publication diagnostic=%s", warnings)
			}
			if (runtimeFailure || name == "closed publication") && !strings.Contains(string(warnings), "operation_id="+trace.OperationID) {
				t.Fatalf("missing correlation: %s", warnings)
			}
			if strings.Contains(string(warnings), "stage=artifact_put") != runtimeFailure || strings.Contains(string(warnings), "must-not-log-secret-response") || strings.Contains(string(warnings), "measurement") {
				t.Fatalf("unsafe or missing diagnostic: %s", warnings)
			}

			good := name == "changed code" || name == "normal" || name == "zero" || name == "sample fixture" || name == "numeric content" || name == "empty object" || name == "array result" || name == "embedded declaration"
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
							Complete bool   `json:"complete"`
							Hash     string `json:"content_hash"`

							OutputHash string         `json:"output_hash"`
							Producer   ResultProducer `json:"producer"`
						} `json:"payload"`
					} `json:"envelope"`
				}
				if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &wire); err != nil {
					t.Fatal(err)
				}
				if !wire.Envelope.Payload.Complete || wire.EventType != "logic.execution.observed" || len(wire.Claims) != 0 || wire.Envelope.Payload.Contract != FunctionResultContract || wire.Envelope.Payload.Hash != artifact["content_hash"] {
					t.Fatalf("wire=%+v", wire)
				}
				execution := result["function_execution"].(map[string]any)
				if wire.Envelope.Payload.Producer.CodeSHA256 != execution["code_sha256"] {
					t.Fatal("recorded code hash differs from actual execution")
				}
				if fixtureProof != nil && (wire.Envelope.Payload.Hash != fixtureProof["content_hash"] || wire.Envelope.Payload.OutputHash != fixtureProof["output_hash"]) {
					t.Fatalf("sample producer contract differs from fixture: %+v", wire)
				}
			} else if err == nil || len(publisher.SnapshotQueue()) != 0 {
				t.Fatalf("unverified result emitted evidence: %v", err)
			}
			if name == "artifact timeout" {
				finishes := 0
				client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
					if err := r.Context().Err(); err != nil {
						return nil, err
					}
					finishes++
					done := pendingGuardState().Result
					done.Operation.AttemptStatus, done.Receipt.ReceiptStatus = "completed", "completed"
					return lifecycleJSONResponse(200, done), nil
				})
				for range 2 {
					done, apiErr, finishErr := NewGuard(client).Finish(ctx, pendingGuardState(), result, false, false)
					if finishErr != nil || apiErr != nil || done.Receipt.ReceiptStatus != "completed" {
						t.Fatalf("artifact exhausted finish budget: result=%+v apiErr=%v err=%v", done, apiErr, finishErr)
					}
				}
				if finishes != 2 {
					t.Fatalf("terminal writes=%d", finishes)
				}
			}
		})
	}
}
