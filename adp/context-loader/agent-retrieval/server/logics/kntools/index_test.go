// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeOperator implements only the execution-factory calls this layer makes.
type fakeOperator struct {
	interfaces.DrivenOperatorIntegration

	hits       []interfaces.CapabilityHit
	hitsErr    error
	toolsByBox map[string]*interfaces.ListPublishedToolsResponse
	toolsErr   map[string]error
	execResp   map[string]any
	execErr    error

	mu             sync.Mutex
	gotRefs        []string
	gotQuery       string
	gotTopK        int
	listedToolbox  []string
	gotExecuteReq  *interfaces.ExecutePublishedToolRequest
	executionCount int
	mcpTools       map[string]*interfaces.GetMCPToolDetailResponse
	mcpDetailCalls []string
	gotMCPCall     *interfaces.CallMCPToolRequest
	mcpUnusable    map[string]bool
}

func (f *fakeOperator) SearchCapabilities(
	_ context.Context, req *interfaces.SearchCapabilitiesRequest,
) ([]interfaces.CapabilityHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotRefs = make([]string, 0, len(req.Refs))
	for _, ref := range req.Refs {
		if ref.CapabilityType == interfaces.CapabilityTypeMCPTool {
			f.gotRefs = append(f.gotRefs, interfaces.CapabilityTypeMCPTool+":"+ref.OwnerID+"/"+ref.CapabilityID)
			continue
		}
		f.gotRefs = append(f.gotRefs, ref.OwnerID+"/"+ref.CapabilityID)
	}
	f.gotQuery = req.Query
	f.gotTopK = req.TopK
	return f.hits, f.hitsErr
}

func (f *fakeOperator) ListPublishedTools(
	_ context.Context, req *interfaces.ListPublishedToolsRequest,
) (*interfaces.ListPublishedToolsResponse, error) {
	f.mu.Lock()
	f.listedToolbox = append(f.listedToolbox, req.ToolboxID)
	f.mu.Unlock()
	if err, ok := f.toolsErr[req.ToolboxID]; ok {
		return nil, err
	}
	return f.toolsByBox[req.ToolboxID], nil
}

func (f *fakeOperator) MCPServerIsUsable(_ context.Context, mcpID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mcpUnusable == nil {
		return true, nil
	}
	return !f.mcpUnusable[mcpID], nil
}

func (f *fakeOperator) GetMCPToolDetail(
	_ context.Context, req *interfaces.GetMCPToolDetailRequest,
) (*interfaces.GetMCPToolDetailResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpDetailCalls = append(f.mcpDetailCalls, req.McpID+"/"+req.ToolName)
	if detail, ok := f.mcpTools[req.McpID+"/"+req.ToolName]; ok {
		return detail, nil
	}
	return nil, errors.New("mcp tool not found")
}

func (f *fakeOperator) CallMCPTool(
	_ context.Context, req *interfaces.CallMCPToolRequest,
) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotMCPCall = req
	f.executionCount++
	return f.execResp, f.execErr
}

func (f *fakeOperator) ExecutePublishedTool(
	_ context.Context, req *interfaces.ExecutePublishedToolRequest,
) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotExecuteReq = req
	f.executionCount++
	return f.execResp, f.execErr
}

// fakeBkn answers the capability listing.
type fakeBkn struct {
	interfaces.BknBackendAccess

	refs []*interfaces.CapabilityRef
	err  error

	gotKN   string
	gotType string
}

func (f *fakeBkn) ListKNCapabilities(_ context.Context, knID, _, capabilityType string,
) ([]*interfaces.CapabilityRef, error) {
	f.gotKN, f.gotType = knID, capabilityType
	return f.refs, f.err
}

func mcpToolRefs(pairs ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(pairs))
	for _, pair := range pairs {
		mcpID, toolName := splitPair(pair)
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeMCPTool,
			BoxID:          mcpID,
			CapabilityID:   toolName,
		})
	}
	return refs
}

// skillRefs builds Skill bindings. boundRefs drops them, so only the unified entry sees these.
func skillRefs(ids ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill,
			CapabilityID:   id,
		})
	}
	return refs
}

func functionRefs(pairs ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(pairs))
	for _, pair := range pairs {
		box, tool := splitPair(pair)
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeFunction,
			BoxID:          box,
			CapabilityID:   tool,
		})
	}
	return refs
}

func splitPair(pair string) (string, string) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == '/' {
			return pair[:i], pair[i+1:]
		}
	}
	return pair, ""
}

// mcpHit is one ranked MCP tool as the execution factory now returns it. Ranking and query
// filtering live there; this side supplies the whitelist and renders what comes back.
func mcpHit(mcpID, toolName string) interfaces.CapabilityHit {
	return interfaces.CapabilityHit{
		SearchCapabilityRef: interfaces.SearchCapabilityRef{
			CapabilityType: interfaces.CapabilityTypeMCPTool,
			OwnerID:        mcpID,
			CapabilityID:   toolName,
		},
		Name: toolName,
	}
}

func hit(boxID, toolID string) interfaces.CapabilityHit {
	return interfaces.CapabilityHit{
		SearchCapabilityRef: interfaces.SearchCapabilityRef{
			CapabilityType: interfaces.CapabilityTypeFunction,
			OwnerID:        boxID,
			CapabilityID:   toolID,
		},
		Name: toolID,
	}
}

func tools(boxID string, toolIDs ...string) *interfaces.ListPublishedToolsResponse {
	resp := &interfaces.ListPublishedToolsResponse{ToolboxID: boxID}
	for _, id := range toolIDs {
		resp.Tools = append(resp.Tools, interfaces.PublishedToolSummary{
			ToolID:      id,
			Name:        id,
			Description: id + " description",
			InputSchema: map[string]any{"type": "object"},
		})
	}
	return resp
}

func newService(bkn *fakeBkn, op *fakeOperator) KnToolsService {
	return NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})
}

// fakeKnAuthz stands in for the per-caller knowledge-network check.
type fakeKnAuthz struct {
	err     error
	gotKNID string
}

func (f *fakeKnAuthz) AuthorizeRead(_ context.Context, knID string) error {
	f.gotKNID = knID
	return f.err
}

// TestSearchNarrowsToTheNetworkBindings is the point of the change: the scope is what the network
// mounted, not what the account can see. A toolbox holding both a mounted and an unmounted tool is
// the case the old per-toolbox walk could not express.
func TestSearchNarrowsToTheNetworkBindings(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/mounted")}
	op := &fakeOperator{
		hits: []interfaces.CapabilityHit{hit("box-1", "mounted")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "mounted", "not_mounted"),
		},
	}

	resp, err := newService(bkn, op).SearchTools(context.Background(),
		&SearchToolsReq{KnID: "kn1", Query: "x"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.gotRefs) != 1 || op.gotRefs[0] != "box-1/mounted" {
		t.Fatalf("expected the mounted ref as the whitelist, got %v", op.gotRefs)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].ToolID != "mounted" {
		t.Fatalf("expected only the mounted tool, got %+v", resp.Tools)
	}
	// Both transports are read in one call, so the listing is no longer narrowed by type; the
	// split happens here. Asking per type would cost a round trip per capability kind.
	if bkn.gotType != "" {
		t.Fatalf("expected one unfiltered listing, got type=%q", bkn.gotType)
	}
}

// TestSearchOnUnmountedNetworkReturnsEmpty pins the fail-closed shape: a network that mounted
// nothing gets nothing, not the account's catalogue.
func TestSearchOnUnmountedNetworkReturnsEmpty(t *testing.T) {
	bkn := &fakeBkn{refs: []*interfaces.CapabilityRef{}}
	op := &fakeOperator{hits: []interfaces.CapabilityHit{hit("box-1", "should_not_appear")}}

	resp, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Tools) != 0 {
		t.Fatalf("expected no tools, got %+v", resp.Tools)
	}
	if resp.Message == "" {
		t.Fatal("an empty result must say why")
	}
	if op.gotRefs != nil {
		t.Fatal("an unmounted network must not reach the search at all")
	}
}

// TestSearchRequiresKnID keeps the tool from answering without a scope, which is what the old
// account-wide behaviour amounted to.
func TestSearchRequiresKnID(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{}

	_, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{Query: "x"})

	if err == nil {
		t.Fatal("expected a request without kn_id to fail")
	}
	var he *infraErr.HTTPError
	if !errors.As(err, &he) || he.HTTPCode != 400 {
		t.Fatalf("expected a 400, got %v", err)
	}
}

// TestBindingLookupFailureFailsTheSearch: an unreadable binding list must not degrade into either
// "mounted nothing" or "everything".
func TestBindingLookupFailureFailsTheSearch(t *testing.T) {
	bkn := &fakeBkn{err: errors.New("bkn-backend unreachable")}
	op := &fakeOperator{hits: []interfaces.CapabilityHit{hit("box-1", "t1")}}

	_, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})

	if err == nil {
		t.Fatal("expected the search to fail")
	}
	if op.gotRefs != nil {
		t.Fatal("a failed binding lookup must not fall through to a search")
	}
}

// TestToolboxIDNarrowsWithinTheMountedSet checks the filter cannot reach outside the bindings.
func TestToolboxIDNarrowsWithinTheMountedSet(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1", "box-2/t2")}
	op := &fakeOperator{
		hits:       []interfaces.CapabilityHit{hit("box-2", "t2")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-2": tools("box-2", "t2")},
	}

	_, err := newService(bkn, op).SearchTools(context.Background(),
		&SearchToolsReq{KnID: "kn1", ToolboxID: "box-2"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.gotRefs) != 1 || op.gotRefs[0] != "box-2/t2" {
		t.Fatalf("expected only box-2's mounted ref, got %v", op.gotRefs)
	}
}

// TestUnreadableToolboxKeepsItsHitsWithoutSchema covers what an unreadable tool box means.
//
// It used to mean "drop these hits", on the reading that a tool the caller cannot see should not be
// listed. But an unreadable catalogue is not a denial — it is an unanswered question, and the two
// were being treated the same. The visible consequence was a search that reported five matches and
// returned nothing, blaming an unpublished tool box that was in fact published.
//
// Denial is still honoured where it can be observed: a box that answers, without this tool in it,
// still drops it (see TestVisibleCatalogueStillFilters). What changes is the case where nothing can
// be observed at all: the hit survives with the name and description the index holds, and without
// an input schema, because none was read. execute_tool re-checks the caller-visible catalogue
// before anything runs, so this discloses a name, not an ability.
func TestUnreadableToolboxKeepsItsHitsWithoutSchema(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1", "box-2/t2")}
	op := &fakeOperator{
		hits: []interfaces.CapabilityHit{hit("box-1", "t1"), hit("box-2", "t2")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1"),
		},
		toolsErr: map[string]error{"box-2": errors.New("forbidden")},
	}

	resp, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("one unreadable toolbox must not fail the search, got %v", err)
	}
	if len(resp.Tools) != 2 {
		t.Fatalf("读得到的和读不到的都该在，got %+v", resp.Tools)
	}
	byBox := map[string]ToolEntry{}
	for _, e := range resp.Tools {
		byBox[e.ToolboxID] = e
	}
	if byBox["box-1"].InputSchema == nil {
		t.Fatal("目录读得到的工具应当带 input_schema")
	}
	if byBox["box-2"].InputSchema != nil {
		t.Fatal("目录读不到时不该凭空造出 input_schema")
	}
	if byBox["box-2"].Name == "" {
		t.Fatal("读不到目录时也该保留索引里的名称")
	}
}

// TestHitsCarryTheInputSchema guards what execute_tool depends on: ranking does not return the
// schema, so it has to be filled from the catalogue.
func TestHitsCarryTheInputSchema(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{
		hits:       []interfaces.CapabilityHit{hit("box-1", "t1")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
	}

	resp, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].InputSchema == nil {
		t.Fatalf("expected the input schema to travel, got %+v", resp.Tools)
	}
	if resp.Tools[0].Description == "" {
		t.Fatal("expected the description to travel")
	}
}

// TestOneRequestPerToolboxNotPerHit pins the fan-out shape, which is the cost this change was
// meant to remove.
func TestOneRequestPerToolboxNotPerHit(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1", "box-1/t2", "box-1/t3")}
	op := &fakeOperator{
		hits: []interfaces.CapabilityHit{hit("box-1", "t1"), hit("box-1", "t2"), hit("box-1", "t3")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1", "t2", "t3"),
		},
	}

	if _, err := newService(bkn, op).SearchTools(context.Background(),
		&SearchToolsReq{KnID: "kn1"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.listedToolbox) != 1 {
		t.Fatalf("expected one catalogue read for one toolbox, got %v", op.listedToolbox)
	}
}

// TestExecuteRefusesAToolNotMountedOnTheNetwork is the control that matters: a tool id outlives
// the search that produced it, and this is the call that writes.
func TestExecuteRefusesAToolNotMountedOnTheNetwork(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/mounted")}
	op := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "mounted", "not_mounted"),
		},
	}

	_, err := newService(bkn, op).ExecuteTool(context.Background(), &ExecuteToolReq{
		KnID: "kn1", ToolboxID: "box-1", ToolID: "not_mounted",
	})

	if err == nil {
		t.Fatal("expected an unmounted tool to be refused")
	}
	if op.executionCount != 0 {
		t.Fatal("an unmounted tool must never reach the proxy")
	}
}

// TestExecuteRequiresKnID keeps the gate from being skipped by omission.
func TestExecuteRequiresKnID(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{}

	_, err := newService(bkn, op).ExecuteTool(context.Background(),
		&ExecuteToolReq{ToolboxID: "box-1", ToolID: "t1"})

	if err == nil {
		t.Fatal("expected a request without kn_id to fail")
	}
	if op.executionCount != 0 {
		t.Fatal("nothing must run without a scope")
	}
}

// TestExecutePassesOnlyBusinessArguments keeps the mounted path intact end to end.
func TestExecutePassesOnlyBusinessArguments(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
		execResp:   map[string]any{"ok": true},
	}

	resp, err := newService(bkn, op).ExecuteTool(context.Background(), &ExecuteToolReq{
		KnID: "kn1", ToolboxID: "box-1", ToolID: "t1",
		Arguments: map[string]any{"city": "上海"},
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("expected the tool response to travel, got %+v", resp)
	}
	if op.gotExecuteReq == nil || op.gotExecuteReq.Parameters["city"] != "上海" {
		t.Fatalf("expected the business arguments to travel, got %+v", op.gotExecuteReq)
	}
}

// TestExecuteRequiresBothIDs keeps the original argument validation.
func TestExecuteRequiresBothIDs(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{}

	for _, req := range []*ExecuteToolReq{
		{KnID: "kn1", ToolID: "t1"},
		{KnID: "kn1", ToolboxID: "box-1"},
	} {
		if _, err := newService(bkn, op).ExecuteTool(context.Background(), req); err == nil {
			t.Fatalf("expected %+v to be refused", req)
		}
	}
	if op.executionCount != 0 {
		t.Fatal("an invalid request must never reach the proxy")
	}
}

// TestUnauthorizedNetworkIsRefusedForBothEntryPoints: the bindings and the ranking are both read
// with this service's identity, so without a per-caller check the scope would be whatever kn_id
// the caller typed.
func TestUnauthorizedNetworkIsRefusedForBothEntryPoints(t *testing.T) {
	for _, name := range []string{"search", "execute"} {
		bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
		op := &fakeOperator{
			hits:       []interfaces.CapabilityHit{hit("box-1", "t1")},
			toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
		}
		authz := &fakeKnAuthz{err: errors.New("forbidden")}
		svc := NewKnToolsServiceWith(op, bkn, authz)

		var err error
		if name == "search" {
			_, err = svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "someone-elses-kn"})
		} else {
			_, err = svc.ExecuteTool(context.Background(), &ExecuteToolReq{
				KnID: "someone-elses-kn", ToolboxID: "box-1", ToolID: "t1",
			})
		}

		if err == nil {
			t.Fatalf("%s: expected an unauthorized network to be refused", name)
		}
		if bkn.gotKN != "" {
			t.Fatalf("%s: nothing may be read before the caller is authorized", name)
		}
		if op.executionCount != 0 {
			t.Fatalf("%s: nothing may run before the caller is authorized", name)
		}
	}
}

// TestMissingAuthorizerFailsClosed keeps a service wired without the check from answering.
func TestMissingAuthorizerFailsClosed(t *testing.T) {
	svc := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{refs: functionRefs("box-1/t1")}, nil)

	if _, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"}); err == nil {
		t.Fatal("expected a service without an authorizer to refuse")
	}
}

// TestMCPToolsAreSearchableAndCallable covers #1359 on the retrieval side. Mounting an MCP tool
// that cannot then be found or run is the same as not mounting it.
func TestMCPToolsAreSearchableAndCallable(t *testing.T) {
	newSvc := func() (KnToolsService, *fakeOperator) {
		op := &fakeOperator{
			hits: []interfaces.CapabilityHit{mcpHit("mcp-1", "expedite")},
			mcpTools: map[string]*interfaces.GetMCPToolDetailResponse{
				"mcp-1/expedite": {Name: "expedite", Description: "催单", InputSchema: map[string]any{"type": "object"}},
			},
			execResp: map[string]any{"ok": true},
		}
		bkn := &fakeBkn{refs: mcpToolRefs("mcp-1/expedite")}
		return NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{}), op
	}

	t.Run("出现在搜索结果并带 input_schema", func(t *testing.T) {
		svc, _ := newSvc()
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(resp.Tools) != 1 || resp.Tools[0].ToolID != "expedite" {
			t.Fatalf("expected the mounted MCP tool, got %+v", resp.Tools)
		}
		if resp.Tools[0].ToolboxID != "mcp-1" {
			t.Fatalf("expected the MCP server id to travel, got %+v", resp.Tools[0])
		}
		if resp.Tools[0].InputSchema == nil {
			t.Fatal("execute_tool needs the input schema")
		}
	})

	t.Run("执行走 MCP proxy 而非工具箱代理", func(t *testing.T) {
		svc, op := newSvc()
		out, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{
			KnID: "kn1", ToolboxID: "mcp-1", ToolID: "expedite",
			Arguments: map[string]any{"order": "A-1"},
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if out["ok"] != true {
			t.Fatalf("expected the tool response, got %+v", out)
		}
		if op.gotMCPCall == nil || op.gotMCPCall.ToolName != "expedite" {
			t.Fatalf("expected the MCP proxy to be used, got %+v", op.gotMCPCall)
		}
		if op.gotExecuteReq != nil {
			t.Fatal("an MCP tool must not go through the toolbox proxy")
		}
	})

	t.Run("未挂载的 MCP 工具被拒", func(t *testing.T) {
		svc, op := newSvc()
		if _, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{
			KnID: "kn1", ToolboxID: "mcp-1", ToolID: "not_mounted",
		}); err == nil {
			t.Fatal("expected an unmounted MCP tool to be refused")
		}
		if op.executionCount != 0 {
			t.Fatal("an unmounted MCP tool must never reach the proxy")
		}
	})

	t.Run("MCP 工具进入统一检索的白名单", func(t *testing.T) {
		// Ranking and query filtering are the execution factory's job now — both transports are
		// rows in one index. What this side owes is the whitelist: every mounted MCP tool must
		// reach it, or the tool is unfindable no matter how good the ranking is.
		svc, op := newSvc()
		if _, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "催单"}); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if op.gotQuery != "催单" {
			t.Fatalf("query 没有透传给执行工厂，got %q", op.gotQuery)
		}
		if len(op.gotRefs) != 1 || op.gotRefs[0] != "mcp_tool:mcp-1/expedite" {
			t.Fatalf("挂载的 MCP 工具没进白名单，got %+v", op.gotRefs)
		}
	})

	t.Run("检索面返回空就是空，不在本地兜底放宽", func(t *testing.T) {
		svc, op := newSvc()
		op.hits = nil
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "完全无关"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(resp.Tools) != 0 {
			t.Fatalf("expected no match, got %+v", resp.Tools)
		}
	})
}

// TestOfflineMCPServerIsNotExecutable pins the second check on the MCP path. The mount says this
// network may use the tool; it does not say the tool still works. A server published at mount
// time can be taken offline afterwards — bkn-backend then refuses new bindings, but the existing
// one survives, and the MCP proxy performs no status check of its own.
func TestOfflineMCPServerIsNotExecutable(t *testing.T) {
	op := &fakeOperator{
		mcpTools: map[string]*interfaces.GetMCPToolDetailResponse{
			// Still listed: an offline server's tool listing answers exactly as before, which is
			// why the listing cannot stand in for the status check.
			"mcp-1/expedite": {Name: "expedite"},
		},
		mcpUnusable: map[string]bool{"mcp-1": true},
		execResp:    map[string]any{"ok": true},
	}
	bkn := &fakeBkn{refs: mcpToolRefs("mcp-1/expedite")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	_, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{
		KnID: "kn1", ToolboxID: "mcp-1", ToolID: "expedite",
	})

	if err == nil {
		t.Fatal("下架的 MCP Server，其存量绑定不该还能执行")
	}
	if op.gotMCPCall != nil {
		t.Fatal("不可执行的工具不该到达 MCP proxy")
	}
}

// TestMCPTruncationCountsMatchesNotMounts: total is what the query kept, not what is mounted.
// Counting the mounted set would tell a caller its results were truncated and to narrow a query
// that was already working.
func TestMCPTruncationCountsMatchesNotMounts(t *testing.T) {
	op := &fakeOperator{
		// Three tools are mounted; the query kept one, so that is what the ranking returns.
		hits: []interfaces.CapabilityHit{mcpHit("mcp-1", "expedite")},
		mcpTools: map[string]*interfaces.GetMCPToolDetailResponse{
			"mcp-1/expedite":   {Name: "expedite", Description: "催单"},
			"mcp-1/substitute": {Name: "substitute", Description: "替换"},
			"mcp-1/cancel":     {Name: "cancel", Description: "取消"},
		},
	}
	bkn := &fakeBkn{refs: mcpToolRefs("mcp-1/expedite", "mcp-1/substitute", "mcp-1/cancel")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "催单"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Tools) != 1 {
		t.Fatalf("expected one match, got %+v", resp.Tools)
	}
	if resp.TotalMatched != 1 {
		t.Fatalf("total 应当是命中数而非挂载数，got %d", resp.TotalMatched)
	}
	if resp.Truncated {
		t.Fatal("没有截断却报了截断，调用方会去缩小一个本来就好用的 query")
	}
}

// TestUnreadableCatalogueKeepsTheHit covers the answer that used to contradict itself.
//
// The ranking found the tools, so total_matched said five — and the tool list came back empty with
// "no tools matched; register your tool, publish its box and enable it", while the box was
// published and its tools enabled. The real cause was that the caller-visible catalogue could not
// be read at all, which is not the same as the caller being denied. An unreadable catalogue now
// leaves the hit in place without an input schema.
func TestUnreadableCatalogueKeepsTheHit(t *testing.T) {
	op := &fakeOperator{
		hits:     []interfaces.CapabilityHit{hit("box-1", "t1")},
		toolsErr: map[string]error{"box-1": errors.New("caller token missing")},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "汇率"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].ToolID != "t1" {
		t.Fatalf("命中不该因为读不到目录而消失，got %+v (message=%q)", resp.Tools, resp.Message)
	}
	if resp.Tools[0].InputSchema != nil {
		t.Fatal("读不到目录时不该凭空造出 input_schema")
	}
	if resp.TotalMatched != len(resp.Tools) {
		t.Fatalf("total 与返回条数不该互相矛盾: total=%d tools=%d", resp.TotalMatched, len(resp.Tools))
	}
}

// TestVisibleCatalogueStillFilters keeps the second layer where it can actually be evaluated: a
// readable catalogue that does not list the tool means the caller cannot see it, and advertising it
// would promise something execute_tool refuses.
func TestVisibleCatalogueStillFilters(t *testing.T) {
	op := &fakeOperator{
		hits: []interfaces.CapabilityHit{hit("box-1", "hidden")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": {ToolboxID: "box-1", Tools: []interfaces.PublishedToolSummary{{ToolID: "other"}}},
		},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/hidden")}, &fakeKnAuthz{})

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "汇率"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Tools) != 0 {
		t.Fatalf("目录可读但工具不在其中，说明调用方看不到，不该返回: %+v", resp.Tools)
	}
}

// TestEmptyAnswerNamesItsCause covers the message an empty result carries.
//
// One message used to cover every empty answer: "register a tool, publish its box, enable it, or
// broaden the query". It was wrong in most of the cases it was shown for — the box was published,
// the query had matched — and it sent people to fix things that were not broken.
func TestEmptyAnswerNamesItsCause(t *testing.T) {
	// The old message instructed the caller to publish the tool box. Assert on the cause the
	// message names rather than on a keyword: the accurate text may mention publishing precisely
	// in order to rule it out.
	namesVisibility := func(msg string) bool {
		return strings.Contains(msg, "可见") || strings.Contains(msg, "权限")
	}
	tellsToPublish := func(msg string) bool {
		return strings.Contains(msg, "请先在执行工厂注册工具")
	}

	t.Run("命中了但调用方看不到", func(t *testing.T) {
		// The catalogue answers and does not list this tool: the caller cannot see it.
		op := &fakeOperator{
			hits: []interfaces.CapabilityHit{hit("box-1", "hidden")},
			toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
				"box-1": {ToolboxID: "box-1", Tools: []interfaces.PublishedToolSummary{{ToolID: "other"}}},
			},
		}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/hidden")}, &fakeKnAuthz{})
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "汇率"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(resp.Tools) != 0 {
			t.Fatalf("这个用例要的是空结果, got %+v", resp.Tools)
		}
		if tellsToPublish(resp.Message) || !namesVisibility(resp.Message) {
			t.Fatalf("命中被可见性挡掉时该说可见性，而不是让人去发布工具箱: %q", resp.Message)
		}
	})

	t.Run("类型过滤后为空", func(t *testing.T) {
		op := &fakeOperator{hits: nil}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{
			KnID: "kn1", Query: "汇率", MetadataTypes: []string{"function"},
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !strings.Contains(resp.Message, "metadata_types") {
			t.Fatalf("类型过滤筛空时该点名 metadata_types: %q", resp.Message)
		}
	})

	t.Run("确实没有匹配：不能怪到调用方没传过的参数上", func(t *testing.T) {
		// search_tools pins the kinds when it delegates. If that counted as a caller's filter,
		// this answer would tell the caller to drop a types parameter it never set and cannot set.
		// Asserting only that a message exists is what let the wrong branch through review: every
		// branch satisfies it.
		op := &fakeOperator{hits: nil}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Query: "毫不相关"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if resp.Message == "" {
			t.Fatal("空结果总该给个说法")
		}
		if strings.Contains(resp.Message, "types") {
			t.Fatalf("不该让调用方去改它传不了的参数: %q", resp.Message)
		}
	})

	t.Run("只传了 metadata_types 时，提示不得连带点名 types", func(t *testing.T) {
		// The branch fires correctly, but its text used to name both filters. search_tools sets
		// types itself and exposes no input for it, so half of "drop these two parameters" is
		// advice the caller cannot follow — the same defect one level down.
		op := &fakeOperator{hits: nil}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{
			KnID: "kn1", Query: "汇率", MetadataTypes: []string{"function"},
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !strings.Contains(resp.Message, "metadata_types") {
			t.Fatalf("这次确实是调用方筛空的，该点名: %q", resp.Message)
		}
		if strings.Contains(resp.Message, "types /") || strings.Contains(resp.Message, "/ metadata_types") {
			t.Fatalf("不该连带让调用方去掉它设不了的 types: %q", resp.Message)
		}
	})
}

// TestTruncationIsDetectable covers a flag that had become unreachable.
//
// The ranking caps its answer at top_k. Asking for exactly `limit` makes a full page and a
// truncated page identical, so truncated could never be true and a caller read one page as the
// whole answer. One extra hit is requested purely to learn whether a next one exists.
func TestTruncationIsDetectable(t *testing.T) {
	t.Run("有下一页时报截断，且不把多要的那条返回给调用方", func(t *testing.T) {
		hits := make([]interfaces.CapabilityHit, 0, 4)
		for _, id := range []string{"t1", "t2", "t3", "t4"} {
			hits = append(hits, hit("box-1", id))
		}
		op := &fakeOperator{
			hits: hits,
			toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
				"box-1": tools("box-1", "t1", "t2", "t3", "t4"),
			},
		}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs(
			"box-1/t1", "box-1/t2", "box-1/t3", "box-1/t4")}, &fakeKnAuthz{})

		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Limit: 3})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !resp.Truncated {
			t.Fatal("命中多于一页却没报截断，调用方会把一页当成全部")
		}
		if len(resp.Tools) != 3 {
			t.Fatalf("多要的那条只用来探测，不该返回: %d 条", len(resp.Tools))
		}
		if op.gotTopK != 4 {
			t.Fatalf("该向排序多要一条来探测下一页, got top_k=%d", op.gotTopK)
		}
	})

	t.Run("正好一页不报截断", func(t *testing.T) {
		op := &fakeOperator{
			hits: []interfaces.CapabilityHit{hit("box-1", "t1")},
			toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
				"box-1": tools("box-1", "t1"),
			},
		}
		svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
		resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1", Limit: 3})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if resp.Truncated {
			t.Fatal("没有下一页却报了截断，调用方会去缩一个本来就好用的 query")
		}
	})
}
