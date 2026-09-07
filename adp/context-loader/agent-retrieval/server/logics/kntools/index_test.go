// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"errors"
	"sync"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeOperator implements only the execution-factory calls this layer makes.
type fakeOperator struct {
	interfaces.DrivenOperatorIntegration

	hits       []interfaces.ToolHit
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
}

func (f *fakeOperator) SearchBoundTools(
	_ context.Context, req *interfaces.SearchBoundToolsRequest,
) ([]interfaces.ToolHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotRefs = req.ToolRefs
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

func hit(boxID, toolID string) interfaces.ToolHit {
	return interfaces.ToolHit{BoxID: boxID, ToolID: toolID, Name: toolID}
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
		hits:       []interfaces.ToolHit{hit("box-1", "mounted")},
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
	if bkn.gotType != interfaces.CapabilityTypeFunction {
		t.Fatalf("expected the listing narrowed to functions, got %q", bkn.gotType)
	}
}

// TestSearchOnUnmountedNetworkReturnsEmpty pins the fail-closed shape: a network that mounted
// nothing gets nothing, not the account's catalogue.
func TestSearchOnUnmountedNetworkReturnsEmpty(t *testing.T) {
	bkn := &fakeBkn{refs: []*interfaces.CapabilityRef{}}
	op := &fakeOperator{hits: []interfaces.ToolHit{hit("box-1", "should_not_appear")}}

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
	op := &fakeOperator{hits: []interfaces.ToolHit{hit("box-1", "t1")}}

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
		hits:       []interfaces.ToolHit{hit("box-2", "t2")},
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

// TestUnreadableToolboxDropsItsHits covers the second half of the scope: a mounted tool the caller
// cannot see is not listed, because execute_tool would refuse it anyway.
func TestUnreadableToolboxDropsItsHits(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1", "box-2/t2")}
	op := &fakeOperator{
		hits: []interfaces.ToolHit{hit("box-1", "t1"), hit("box-2", "t2")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1"),
		},
		toolsErr: map[string]error{"box-2": errors.New("forbidden")},
	}

	resp, err := newService(bkn, op).SearchTools(context.Background(), &SearchToolsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("one unreadable toolbox must not fail the search, got %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].ToolboxID != "box-1" {
		t.Fatalf("expected only the readable toolbox's tool, got %+v", resp.Tools)
	}
}

// TestHitsCarryTheInputSchema guards what execute_tool depends on: ranking does not return the
// schema, so it has to be filled from the catalogue.
func TestHitsCarryTheInputSchema(t *testing.T) {
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	op := &fakeOperator{
		hits:       []interfaces.ToolHit{hit("box-1", "t1")},
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
		hits: []interfaces.ToolHit{hit("box-1", "t1"), hit("box-1", "t2"), hit("box-1", "t3")},
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
			hits:       []interfaces.ToolHit{hit("box-1", "t1")},
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
