// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

type fakeActionTypeAuthz struct {
	err   error
	calls [][2]string
}

func (f *fakeActionTypeAuthz) AuthorizeActionTypeView(_ context.Context, knID, atID string) error {
	f.calls = append(f.calls, [2]string{knID, atID})
	return f.err
}

type fakeProxyResolver struct {
	err      error
	bindings []interfaces.KNProxyBinding
}

func (f *fakeProxyResolver) ResolveKNProxyBinding(_ context.Context,
	binding interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, error) {
	f.bindings = append(f.bindings, binding)
	if f.err != nil {
		return nil, f.err
	}
	return &interfaces.KNProxyAccount{
		KNID: binding.KNID, ProxyAccountID: "proxy-1", ProxyAccountType: "app",
		LifecycleStatus: "active", Version: 4, SyncStatus: "ready",
	}, nil
}

// boundToolFixture wires get_action_info around one action type bound to box-1/tool-1 or to
// mcp-1/send_sms, with the proxy-read dependencies observable.
type boundToolFixture struct {
	service  *knActionRecallServiceImpl
	query    *mocks.MockDrivenOntologyQuery
	operator *mocks.MockDrivenOperatorIntegration
	reader   *mocks.MockKNProxyDefinitionReader
	authz    *fakeActionTypeAuthz
	resolver *fakeProxyResolver
}

func newBoundToolFixture(t *testing.T) *boundToolFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	f := &boundToolFixture{
		query:    mocks.NewMockDrivenOntologyQuery(ctrl),
		operator: mocks.NewMockDrivenOperatorIntegration(ctrl),
		reader:   mocks.NewMockKNProxyDefinitionReader(ctrl),
		authz:    &fakeActionTypeAuthz{},
		resolver: &fakeProxyResolver{},
	}
	f.service = &knActionRecallServiceImpl{
		logger: logger,
		config: &config.Config{OntologyQuery: config.PrivateBaseConfig{
			PrivateProtocol: "http", PrivateHost: "ontology-query", PrivatePort: 13018,
		}},
		ontologyQuery:       f.query,
		operatorIntegration: f.operator,
		actionAuthz:         f.authz,
		proxyResolver:       f.resolver,
		proxyReader:         f.reader,
	}
	return f
}

func (f *boundToolFixture) expectAction(source interfaces.ActionSource) {
	f.query.EXPECT().QueryActions(gomock.Any(), gomock.Any()).Return(&interfaces.QueryActionsResponse{
		ActionSource: &source,
		Actions:      []interfaces.ActionParams{{Parameters: map[string]any{"order_id": "o-1"}}},
	}, nil)
}

var (
	toolSource = interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box-1", ToolID: "tool-1"}
	mcpSource  = interfaces.ActionSource{Type: interfaces.ActionSourceTypeMCP, McpID: "mcp-1", ToolName: "send_sms"}
)

func boundToolDetail() *interfaces.GetToolDetailResponse {
	return &interfaces.GetToolDetailResponse{
		ToolID:      "tool-1",
		Name:        "send_sms",
		Description: "Send an SMS",
		Metadata: interfaces.ToolMetadata{APISpec: map[string]any{
			"parameters": []any{map[string]any{
				"name": "phone", "in": "query", "required": true, "schema": map[string]any{"type": "string"},
			}},
			"responses": []any{map[string]any{
				"status_code": "200",
				"content": map[string]any{"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "properties": map[string]any{"sent": map[string]any{"type": "boolean"}}},
				}},
			}},
		}},
	}
}

func boundMCPToolDetail() *interfaces.GetMCPToolDetailResponse {
	return &interfaces.GetMCPToolDetailResponse{
		Name:        "send_sms",
		Description: "Send an SMS",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"phone": map[string]any{"type": "string"}},
			"required":   []any{"phone"},
		},
	}
}

func forbidden() error {
	return infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden,
		"AgentOperatorIntegration.Forbidden.CommonOperationForbidden: no permission")
}

func request() *interfaces.KnActionRecallRequest {
	return &interfaces.KnActionRecallRequest{KnID: "kn-1", AtID: "at-1"}
}

func statusOf(err error) int {
	status, _ := infraErr.HTTPStatus(err)
	return status
}

// A caller that holds the tool grant is answered by the direct read, exactly as before: the
// action-type check, the binding lookup and the proxy are never consulted.
func TestGetActionInfoWithToolGrantNeverUsesTheProxy(t *testing.T) {
	f := newBoundToolFixture(t)
	f.expectAction(toolSource)
	f.operator.EXPECT().GetToolDetail(gomock.Any(), &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}).
		Return(boundToolDetail(), nil)

	resp, err := f.service.GetActionInfo(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.DynamicTools) != 1 || resp.DynamicTools[0].Name != "send_sms" {
		t.Fatalf("dynamic tools = %+v", resp.DynamicTools)
	}
	if len(f.authz.calls) != 0 || len(f.resolver.bindings) != 0 {
		t.Fatalf("proxy path consulted: authz=%v bindings=%v", f.authz.calls, f.resolver.bindings)
	}
}

// The #1548 scenario: view on the action type, no grant on the bound tool. The definition is read
// through the network's proxy and get_action_info answers with the very tool the direct read
// would have produced.
func TestGetActionInfoReadsTheBoundToolThroughTheProxyWithoutAToolGrant(t *testing.T) {
	direct := newBoundToolFixture(t)
	direct.expectAction(toolSource)
	direct.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(boundToolDetail(), nil)
	want, err := direct.service.GetActionInfo(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}

	f := newBoundToolFixture(t)
	f.expectAction(toolSource)
	f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
	f.reader.EXPECT().GetToolDefinitionAsProxy(gomock.Any(),
		&interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *interfaces.GetToolDetailRequest,
			proxy *interfaces.KNProxyExecution) (*interfaces.GetToolDetailResponse, error) {
			if proxy.Mapping.ProxyAccountID != "proxy-1" || proxy.Binding.ChildType != "action_type" ||
				proxy.Binding.ChildID != "at-1" || proxy.Binding.TargetID != "box-1" {
				t.Errorf("proxy = %+v / %+v", proxy.Mapping, proxy.Binding)
			}
			return boundToolDetail(), nil
		})

	got, err := f.service.GetActionInfo(context.Background(), request())
	if err != nil {
		t.Fatalf("GetActionInfo() error = %v", err)
	}
	if !reflect.DeepEqual(got.DynamicTools, want.DynamicTools) {
		t.Fatalf("proxy read built %+v, direct read builds %+v", got.DynamicTools, want.DynamicTools)
	}
	if !reflect.DeepEqual(f.authz.calls, [][2]string{{"kn-1", "at-1"}}) {
		t.Fatalf("action-type checks = %v", f.authz.calls)
	}
	wantBinding := interfaces.KNProxyBinding{
		KNID: "kn-1", ChildType: "action_type", ChildID: "at-1",
		TargetType: "tool_box", TargetID: "box-1", Operation: "execute",
	}
	if !reflect.DeepEqual(f.resolver.bindings, []interfaces.KNProxyBinding{wantBinding}) {
		t.Fatalf("bindings = %+v", f.resolver.bindings)
	}
}

func TestGetActionInfoReadsTheBoundMCPToolThroughTheProxyWithoutAnMCPGrant(t *testing.T) {
	direct := newBoundToolFixture(t)
	direct.expectAction(mcpSource)
	direct.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(boundMCPToolDetail(), nil)
	want, err := direct.service.GetActionInfo(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}

	f := newBoundToolFixture(t)
	f.expectAction(mcpSource)
	f.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
	f.reader.EXPECT().GetMCPToolDefinitionAsProxy(gomock.Any(),
		&interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "send_sms"}, gomock.Any()).
		Return(boundMCPToolDetail(), nil)

	got, err := f.service.GetActionInfo(context.Background(), request())
	if err != nil {
		t.Fatalf("GetActionInfo() error = %v", err)
	}
	if !reflect.DeepEqual(got.DynamicTools, want.DynamicTools) {
		t.Fatalf("proxy read built %+v, direct read builds %+v", got.DynamicTools, want.DynamicTools)
	}
	if len(f.resolver.bindings) != 1 || f.resolver.bindings[0].TargetType != "mcp" ||
		f.resolver.bindings[0].TargetID != "mcp-1" || f.resolver.bindings[0].ChildType != "action_type" {
		t.Fatalf("bindings = %+v", f.resolver.bindings)
	}
}

// Every check on the way to the proxy fails closed with its own answer, and a refusal stays a
// refusal: nothing here turns into 502.
func TestGetActionInfoProxyReadFailsClosed(t *testing.T) {
	unavailable := infraErr.DefaultHTTPError(context.Background(), http.StatusServiceUnavailable, "sync pending")
	tests := []struct {
		name         string
		source       interfaces.ActionSource
		authzErr     error
		resolverErr  error
		readerErr    error
		dropReader   bool
		wantStatus   int
		wantBindings int
		wantRead     bool
	}{
		{name: "no view on the action type", source: toolSource,
			authzErr:   infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, "no view"),
			wantStatus: http.StatusForbidden},
		{name: "not the current published binding", source: toolSource,
			resolverErr: infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, "binding invalid"),
			wantStatus:  http.StatusForbidden, wantBindings: 1},
		{name: "proxy not synchronized", source: mcpSource, resolverErr: unavailable,
			wantStatus: http.StatusServiceUnavailable, wantBindings: 1},
		{name: "proxy grant revoked", source: toolSource, readerErr: forbidden(),
			wantStatus: http.StatusForbidden, wantBindings: 1, wantRead: true},
		{name: "action source names no box", source: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, ToolID: "tool-1"},
			wantStatus: http.StatusForbidden},
		{name: "proxy reader not wired", source: toolSource, dropReader: true,
			wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newBoundToolFixture(t)
			f.authz.err = test.authzErr
			f.resolver.err = test.resolverErr
			if test.dropReader {
				f.service.proxyReader = nil
			}
			f.expectAction(test.source)
			if test.source.Type == interfaces.ActionSourceTypeTool {
				f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
				if test.wantRead {
					f.reader.EXPECT().GetToolDefinitionAsProxy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, test.readerErr)
				}
			} else {
				f.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
			}

			resp, err := f.service.GetActionInfo(context.Background(), request())

			if resp != nil || statusOf(err) != test.wantStatus {
				t.Fatalf("GetActionInfo() = %+v, %v; want HTTP %d", resp, err, test.wantStatus)
			}
			if len(f.resolver.bindings) != test.wantBindings {
				t.Fatalf("binding lookups = %d, want %d", len(f.resolver.bindings), test.wantBindings)
			}
		})
	}
}

// Only a refusal for want of a tool grant is replaced. A 401 or a dependency failure on the
// direct read is the answer, and the proxy is not asked.
func TestGetActionInfoFallsBackOnlyForAToolGrantRefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newBoundToolFixture(t)
			f.expectAction(toolSource)
			f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).
				Return(nil, infraErr.DefaultHTTPError(context.Background(), status, "direct read failed"))

			_, err := f.service.GetActionInfo(context.Background(), request())

			if statusOf(err) != status {
				t.Fatalf("status = %d, want %d", statusOf(err), status)
			}
			if len(f.authz.calls) != 0 || len(f.resolver.bindings) != 0 {
				t.Fatalf("proxy path consulted: authz=%v bindings=%v", f.authz.calls, f.resolver.bindings)
			}
		})
	}
}
