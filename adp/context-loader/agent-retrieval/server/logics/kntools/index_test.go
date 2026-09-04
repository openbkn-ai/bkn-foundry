// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeOperator implements only the three published-tool methods this layer uses.
type fakeOperator struct {
	interfaces.DrivenOperatorIntegration

	toolboxes    *interfaces.ListPublishedToolboxesResponse
	toolboxesErr error
	toolsByBox   map[string]*interfaces.ListPublishedToolsResponse
	toolsErr     map[string]error
	execResp     map[string]any
	execErr      error

	mu             sync.Mutex
	gotToolboxReq  *interfaces.ListPublishedToolboxesRequest
	listedToolbox  []string
	gotExecuteReq  *interfaces.ExecutePublishedToolRequest
	executionCount int
}

func (f *fakeOperator) ListPublishedToolboxes(
	_ context.Context, req *interfaces.ListPublishedToolboxesRequest,
) (*interfaces.ListPublishedToolboxesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotToolboxReq = req
	return f.toolboxes, f.toolboxesErr
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

func toolbox(id, name string) interfaces.PublishedToolboxSummary {
	return interfaces.PublishedToolboxSummary{ToolboxID: id, Name: name}
}

func tools(boxID string, names ...string) *interfaces.ListPublishedToolsResponse {
	resp := &interfaces.ListPublishedToolsResponse{ToolboxID: boxID}
	for _, name := range names {
		resp.Tools = append(resp.Tools, interfaces.PublishedToolSummary{
			ToolID: boxID + ":" + name, Name: name, Description: name + " description",
		})
	}
	return resp
}

// The catalogue walk must cover every visible toolbox: a tool whose own toolbox
// is named nothing like the query is precisely what a keyword search misses if
// the query is pushed down as a toolbox filter.
func TestSearchToolsWalksEveryVisibleToolbox(t *testing.T) {
	fake := &fakeOperator{
		toolboxes: &interfaces.ListPublishedToolboxesResponse{
			Toolboxes: []interfaces.PublishedToolboxSummary{toolbox("box_a", "Finance"), toolbox("box_b", "Logistics")},
		},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box_a": tools("box_a", "fx_convert"),
			"box_b": tools("box_b", "fx_quote"),
		},
	}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{Query: "fx_quote"})
	if err != nil {
		t.Fatalf("SearchTools: %v", err)
	}
	if fake.gotToolboxReq.Keyword != "" {
		t.Fatalf("the query was pushed down as a toolbox name filter: %q", fake.gotToolboxReq.Keyword)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].ToolID != "box_b:fx_quote" {
		t.Fatalf("search did not reach the tool in the differently named toolbox: %+v", resp.Tools)
	}
	if resp.Tools[0].ToolboxName != "Logistics" {
		t.Fatalf("the hit lost its toolbox name: %+v", resp.Tools[0])
	}
}

// One broken toolbox must not hide every other tool the caller can reach.
func TestSearchToolsSkipsAToolboxThatFails(t *testing.T) {
	fake := &fakeOperator{
		toolboxes: &interfaces.ListPublishedToolboxesResponse{
			Toolboxes: []interfaces.PublishedToolboxSummary{toolbox("box_a", "Finance"), toolbox("box_b", "Logistics")},
		},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box_b": tools("box_b", "ship_track")},
		toolsErr:   map[string]error{"box_a": errors.New("toolbox revoked")},
	}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{})
	if err != nil {
		t.Fatalf("SearchTools: %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].ToolID != "box_b:ship_track" {
		t.Fatalf("a failing toolbox took the whole search down: %+v", resp.Tools)
	}
}

// The limit is what keeps a wide account from spending a model's context on
// input schemas, so exceeding it has to be both enforced and reported.
func TestSearchToolsCapsAndReportsTruncation(t *testing.T) {
	listed := &interfaces.ListPublishedToolsResponse{ToolboxID: "box_a"}
	for i := 0; i < 5; i++ {
		listed.Tools = append(listed.Tools, interfaces.PublishedToolSummary{
			ToolID: fmt.Sprintf("box_a:t%d", i), Name: fmt.Sprintf("tool_%d", i),
		})
	}
	fake := &fakeOperator{
		toolboxes:  &interfaces.ListPublishedToolboxesResponse{Toolboxes: []interfaces.PublishedToolboxSummary{toolbox("box_a", "Finance")}},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box_a": listed},
	}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{Limit: 2})
	if err != nil {
		t.Fatalf("SearchTools: %v", err)
	}
	if len(resp.Tools) != 2 || !resp.Truncated || resp.TotalMatched != 5 {
		t.Fatalf("limit was not enforced or not reported: %+v", resp)
	}
	if resp.Message == "" {
		t.Fatal("a truncated result carries no explanation, so a model cannot tell it from the whole answer")
	}
}

// An empty result must say why, or a model cannot tell "nothing published" from
// "nothing visible to this account".
func TestSearchToolsExplainsAnEmptyResult(t *testing.T) {
	fake := &fakeOperator{toolboxes: &interfaces.ListPublishedToolboxesResponse{}}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{Query: "invoice"})
	if err != nil {
		t.Fatalf("SearchTools: %v", err)
	}
	if resp.Tools == nil || len(resp.Tools) != 0 || resp.Message == "" {
		t.Fatalf("an empty search returned no explanation: %+v", resp)
	}
}

// An explicit toolbox_id scopes the walk to one request instead of the whole
// directory.
func TestSearchToolsHonoursAnExplicitToolbox(t *testing.T) {
	fake := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box_a": tools("box_a", "fx_convert")},
	}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.SearchTools(context.Background(), &SearchToolsReq{ToolboxID: "box_a"})
	if err != nil {
		t.Fatalf("SearchTools: %v", err)
	}
	if fake.gotToolboxReq != nil {
		t.Fatal("an explicit toolbox_id still listed the whole directory")
	}
	if len(resp.Tools) != 1 {
		t.Fatalf("scoped search returned %d tools", len(resp.Tools))
	}
}

// A tool that is not in the caller-visible enabled catalogue must fail as a 400
// that names the reason, and must never reach the execution proxy.
func TestExecuteToolRefusesAToolOutsideTheEnabledCatalogue(t *testing.T) {
	fake := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box_a": tools("box_a", "fx_convert")},
	}
	svc := NewKnToolsServiceWith(fake)

	_, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{ToolboxID: "box_a", ToolID: "box_a:disabled"})
	if err == nil {
		t.Fatal("a tool outside the enabled catalogue was executed")
	}
	if status, ok := infraErr.HTTPStatus(err); !ok || status != 400 {
		t.Fatalf("want a 400 that explains the refusal, got %v (status ok=%v)", err, ok)
	}
	if fake.executionCount != 0 {
		t.Fatalf("the refused tool still reached the execution proxy %d times", fake.executionCount)
	}
}

func TestExecuteToolPassesOnlyBusinessArguments(t *testing.T) {
	fake := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box_a": tools("box_a", "fx_convert")},
		execResp:   map[string]any{"rate": 7.1},
	}
	svc := NewKnToolsServiceWith(fake)

	resp, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{
		ToolboxID: " box_a ", ToolID: " box_a:fx_convert ", Arguments: map[string]any{"from": "USD"},
	})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if resp["rate"] != 7.1 {
		t.Fatalf("the tool response was not returned verbatim: %+v", resp)
	}
	if fake.gotExecuteReq.ToolboxID != "box_a" || fake.gotExecuteReq.ToolID != "box_a:fx_convert" {
		t.Fatalf("ids reached the proxy unpadded: %+v", fake.gotExecuteReq)
	}
	if len(fake.gotExecuteReq.Parameters) != 1 || fake.gotExecuteReq.Parameters["from"] != "USD" {
		t.Fatalf("the business arguments were altered: %+v", fake.gotExecuteReq.Parameters)
	}
}

func TestExecuteToolRequiresBothIDs(t *testing.T) {
	svc := NewKnToolsServiceWith(&fakeOperator{})

	if _, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{ToolID: "t"}); err == nil {
		t.Fatal("a missing toolbox_id was accepted")
	}
	if _, err := svc.ExecuteTool(context.Background(), &ExecuteToolReq{ToolboxID: "b"}); err == nil {
		t.Fatal("a missing tool_id was accepted")
	}
}
