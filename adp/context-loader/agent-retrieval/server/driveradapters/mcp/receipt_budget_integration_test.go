// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// longListingBackend lists object and relation types long enough that a page is cut by
// characters: long names, five tags and a long comment each.
type longListingBackend struct {
	interfaces.BknBackendAccess
	interfaces.RelationTypeReader
	objectTypes   []*interfaces.ObjectType
	relationTypes []*interfaces.RelationType
}

func newLongListingBackend(count int) *longListingBackend {
	b := &longListingBackend{}
	tags := []string{"采购业务标签", "销售业务标签", "库存业务标签", "财务业务标签", "物流业务标签"}
	comment := strings.Repeat("客户购买商品的订单记录，订单金额、状态与采购销售业务。", 6)
	for i := range count {
		b.objectTypes = append(b.objectTypes, &interfaces.ObjectType{
			ID: fmt.Sprintf("ot_%03d", i), Name: fmt.Sprintf("销售采购订单业务记录对象类_%03d_%s", i, strings.Repeat("x", 30)),
			Tags: tags, Comment: comment, DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res"},
		})
		b.relationTypes = append(b.relationTypes, &interfaces.RelationType{
			ID: fmt.Sprintf("rt_%03d", i), Name: fmt.Sprintf("订单属于客户的关系类_%03d_%s", i, strings.Repeat("y", 30)),
			Tags: tags, Comment: comment, SourceObjectTypeID: "ot_000", TargetObjectTypeID: fmt.Sprintf("ot_%03d", i),
		})
	}
	return b
}

func window[T any](entries []T, offset, limit int) []T {
	end := min(offset+limit, len(entries))
	if offset >= end {
		return nil
	}
	return entries[offset:end]
}

func (b *longListingBackend) ListObjectTypes(_ context.Context, _ string, offset, limit int) (*interfaces.ObjectTypePage, error) {
	page := window(b.objectTypes, offset, limit)
	return &interfaces.ObjectTypePage{Entries: page, TotalCount: int64(len(b.objectTypes)), Scanned: len(page)}, nil
}

func (b *longListingBackend) ListRelationTypesBranch(_ context.Context, _, _ string, _ []string, offset, limit int) (*interfaces.RelationTypePage, error) {
	page := window(b.relationTypes, offset, limit)
	return &interfaces.RelationTypePage{Entries: page, TotalCount: int64(len(b.relationTypes)), Scanned: len(page)}, nil
}

// receiptEchoingCore is a fake Core that answers a completed attempt with the business and
// evidence refs the attempt submitted, as Core does, so the receipt the session guard attaches
// grows with the page the way a deployed one does.
func receiptEchoingCore(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	operations := map[string]bool{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/interactions/"):
			_ = json.NewEncoder(w).Encode(bkntrace.Interaction{
				InteractionID: pathTail(r.URL.Path), ConversationID: "conv-a",
				ExecutionStatus: "active", LeaseToken: "lease", LeaseEpoch: 1,
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/operations:ensure"):
			var body struct {
				OperationKey string `json:"operation_key"`
				ToolName     string `json:"tool_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			operationID := "op-" + body.OperationKey
			mu.Lock()
			operations[operationID] = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Created: true, Execute: true,
				Operation: bkntrace.Operation{OperationID: operationID, ConversationID: "conv-a", InteractionID: "int-a",
					OperationKey: body.OperationKey, ToolName: body.ToolName, Attempt: 1, AttemptStatus: "pending"},
				Receipt: bkntrace.Receipt{ReceiptID: "receipt-" + body.OperationKey, OperationID: operationID,
					ConversationID: "conv-a", InteractionID: "int-a", Attempt: 1, ReceiptStatus: "pending"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attempts/1:complete"):
			var body struct {
				ReceiptID            string                 `json:"receipt_id"`
				EvidenceDurability   string                 `json:"evidence_durability"`
				ObservedEvidenceRefs []string               `json:"observed_evidence_refs"`
				BusinessRefs         []bkntrace.BusinessRef `json:"business_refs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			operationID := pathTail(strings.TrimSuffix(r.URL.Path, "/attempts/1:complete"))
			mu.Lock()
			registered := operations[operationID]
			mu.Unlock()
			if !registered {
				t.Errorf("finish used an unregistered operation %q", operationID)
			}
			observed := body.ObservedEvidenceRefs
			if len(observed) == 0 {
				observed = []string{"evt_" + strings.Repeat("a", 64)}
			}
			_ = json.NewEncoder(w).Encode(bkntrace.OperationResult{
				Operation: bkntrace.Operation{OperationID: operationID, ConversationID: "conv-a", InteractionID: "int-a",
					Attempt: 1, AttemptStatus: "completed"},
				Receipt: bkntrace.Receipt{ReceiptID: body.ReceiptID, OperationID: operationID,
					ConversationID: "conv-a", InteractionID: "int-a", Attempt: 1, ReceiptStatus: "completed",
					EvidenceDurability: body.EvidenceDurability, ObservedEvidenceRefs: observed, BusinessRefs: body.BusinessRefs},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// finalResultRunes measures a delivered result the way a host reads it: every text item, and
// structuredContent with the receipt in it.
func finalResultRunes(t *testing.T, result *mcpsdk.CallToolResult) (text, structured int) {
	t.Helper()
	for _, content := range result.Content {
		if item, ok := mcpsdk.AsTextContent(content); ok {
			text += utf8.RuneCountInString(item.Text)
		}
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return text, utf8.RuneCount(raw)
}

// A managed listing of object or relation types stays within the character limit after the
// session guard has attached the receipt, whose business refs list every type on the page, in
// JSON and TOON; and walking next_offset returns every type exactly once (#3413).
func TestManagedListingStaysWithinTheLimitWithItsReceipt(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	core := receiptEchoingCore(t)
	defer core.Close()

	backend := newLongListingBackend(210)
	srv, _ := newMCPServer(bkntrace.NewLifecycleClient(core.URL, core.Client()))
	for name, handler := range map[string]func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error){
		toolKeyGetObjectTypes:   handleGetObjectTypes(backend, knmetrics.NewKnMetricsServiceWith(nil, backend, nil), nil),
		toolKeyGetRelationTypes: handleGetRelationTypes(backend),
	} {
		registered := srv.GetTool(name)
		if registered == nil {
			t.Fatalf("assembled MCP server omitted %s", name)
		}
		srv.AddTool(registered.Tool, handler)
	}
	var sequence atomic.Uint64
	handler := newMCPStreamableHTTPHandler(srv, endpointPath)
	mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(trustedMCPIntegrationContext(r.Context(), sequence.Add(1))))
	}))
	defer mcpHTTP.Close()

	ctx := context.Background()
	client := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath, "2025-11-25")
	defer func() { _ = client.Close() }()

	for _, tool := range []struct{ name, key, prefix string }{
		{toolKeyGetObjectTypes, "object_types", "ot_"},
		{toolKeyGetRelationTypes, "relation_types", "rt_"},
	} {
		for _, format := range []string{"json", "toon"} {
			seen := map[string]int{}
			offset, pages, receipts := 0, 0, 0
			for {
				result, err := client.CallTool(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
					Name: tool.name,
					Arguments: map[string]any{
						"kn_id": "kn-001", "branch": "main", "limit": 999, "offset": offset, "response_format": format,
						"bkn_context": map[string]any{"conversation_id": "conv-a", "interaction_id": "int-a"},
					},
				}})
				if err != nil || result.IsError {
					t.Fatalf("%s/%s offset %d: %v %+v", tool.name, format, offset, err, result)
				}
				pages++
				text, structured := finalResultRunes(t, result)
				if max(text, structured) > interfaces.MaxObjectTypePageChars {
					t.Fatalf("%s/%s page %d: text %d, structuredContent %d characters, past the %d limit",
						tool.name, format, pages, text, structured, interfaces.MaxObjectTypePageChars)
				}
				payload, _ := result.StructuredContent.(map[string]any)
				if receipt, _ := payload["bkn_receipt"].(map[string]any); receipt["receipt_status"] == "completed" {
					receipts++
					refs, _ := receipt["business_refs"].([]any)
					entries, _ := payload[tool.key].([]any)
					if len(refs) < len(entries) {
						t.Fatalf("%s/%s page %d: receipt lists %d business refs for %d entries", tool.name, format, pages, len(refs), len(entries))
					}
				}
				entries, _ := payload[tool.key].([]any)
				for _, entry := range entries {
					id, _ := entry.(map[string]any)["id"].(string)
					seen[id]++
				}
				next, ok := payload["next_offset"].(float64)
				if !ok {
					break
				}
				if int(next) != offset+len(entries) {
					t.Fatalf("%s/%s page %d: next_offset %v, want %d after %d entries", tool.name, format, pages, next, offset+len(entries), len(entries))
				}
				offset = int(next)
				if pages > 50 {
					t.Fatalf("%s/%s: the walk does not end", tool.name, format)
				}
			}
			if pages < 2 || receipts != pages {
				t.Fatalf("%s/%s: %d pages, %d with a completed receipt; want a cut walk, each page managed", tool.name, format, pages, receipts)
			}
			for i := range 210 {
				if id := fmt.Sprintf("%s%03d", tool.prefix, i); seen[id] != 1 {
					t.Fatalf("%s/%s: %s returned %d times, want once", tool.name, format, id, seen[id])
				}
			}
		}
	}
}

// A managed call repeated against a newer draft answers from its second run, so its receipt
// names the types that run returned and none from the first: refs left over from the first
// run would name the old draft and outgrow the room the second run left for its receipt.
func TestRepeatedCallReceiptNamesOnlyTheLastRun(t *testing.T) {
	t.Setenv("CONFIG_PROFILE", "../../infra/config")
	core := receiptEchoingCore(t)
	defer core.Close()

	srv, _ := newMCPServer(bkntrace.NewLifecycleClient(core.URL, core.Client()))
	registered := srv.GetTool(toolKeyGetObjectTypes)
	if registered == nil {
		t.Fatalf("assembled MCP server omitted %s", toolKeyGetObjectTypes)
	}
	runs := 0
	srv.AddTool(registered.Tool, withKnVersion(func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		runs++
		if runs == 1 {
			bkntrace.EmitSchemaSnapshotEvents(ctx, nil, "object", "kn-001", []string{"ot_first_a", "ot_first_b"}, nil, true)
			knversion.FromContext(ctx).Observe(http.StatusConflict, http.Header{},
				[]byte(`{"error_code":"`+knversion.CodeDraftRevisionMismatch+`"}`))
			return mcpsdk.NewToolResultError("draft moved"), nil
		}
		bkntrace.EmitSchemaSnapshotEvents(ctx, nil, "object", "kn-001", []string{"ot_second"}, nil, true)
		return mcpsdk.NewToolResultStructured(map[string]any{"object_types": []any{map[string]any{"id": "ot_second"}}}, "ot_second"), nil
	}))
	var sequence atomic.Uint64
	handler := newMCPStreamableHTTPHandler(srv, endpointPath)
	mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(trustedMCPIntegrationContext(r.Context(), sequence.Add(1))))
	}))
	defer mcpHTTP.Close()

	ctx := context.Background()
	client := newInitializedMCPClient(t, ctx, mcpHTTP.URL+endpointPath, "2025-11-25")
	defer func() { _ = client.Close() }()

	result, err := client.CallTool(ctx, mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Name: toolKeyGetObjectTypes,
		Arguments: map[string]any{
			"kn_id": "kn-001", "kn_version": "draft",
			"bkn_context": map[string]any{"conversation_id": "conv-a", "interaction_id": "int-a"},
		},
	}})
	if err != nil || result.IsError {
		t.Fatalf("call: %v %+v", err, result)
	}
	if runs != 2 {
		t.Fatalf("runs = %d, want the call repeated once against the new draft", runs)
	}
	payload, _ := result.StructuredContent.(map[string]any)
	receipt, _ := payload["bkn_receipt"].(map[string]any)
	if receipt["receipt_status"] != "completed" {
		t.Fatalf("no completed receipt: %+v", payload)
	}
	var objectRefs []string
	refs, _ := receipt["business_refs"].([]any)
	for _, ref := range refs {
		if entry, _ := ref.(map[string]any); entry["ref_type"] == "object_type" {
			objectRefs = append(objectRefs, entry["ref_id"].(string))
		}
	}
	if len(objectRefs) != 1 || objectRefs[0] != "object:kn-001:ot_second" {
		t.Fatalf("receipt names object types %v, want only the second run's object:kn-001:ot_second", objectRefs)
	}
}

// Outside a managed call nothing is attached after the handler, so nothing is reserved and an
// unmanaged page is as long as before.
func TestUnmanagedResultReservesNothingForAReceipt(t *testing.T) {
	resp := &interfaces.ObjectTypesResp{KnID: "kn-001", ObjectTypes: newLongListingBackend(3).objectTypes}
	for _, format := range []string{"json", "toon"} {
		parsed, err := rest.ParseResponseFormat(format)
		if err != nil {
			t.Fatal(err)
		}
		want, err := toolResultRunes(resp, parsed)
		if err != nil {
			t.Fatal(err)
		}
		got, err := managedToolResultRunes(context.Background(), resp, parsed, "object", "kn-001", schemaObjectTypeIDs(resp.ObjectTypes))
		if err != nil || got != want {
			t.Fatalf("%s: unmanaged measure = %d, %v; want %d", format, got, err, want)
		}
	}
}
