// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// callSequence records, in order, which of the proxy-read steps ran.
type callSequence struct{ steps []string }

func (c *callSequence) add(step string) { c.steps = append(c.steps, step) }

type fakeActionTypeAuthz struct {
	err   error
	calls [][2]string
	seq   *callSequence
}

func (f *fakeActionTypeAuthz) AuthorizeActionTypeView(_ context.Context, knID, atID string) error {
	f.seq.add("authz")
	f.calls = append(f.calls, [2]string{knID, atID})
	return f.err
}

type fakeProxyResolver struct {
	err      error
	bindings []interfaces.KNProxyBinding
	seq      *callSequence
}

func (f *fakeProxyResolver) ResolveKNProxyBinding(_ context.Context,
	binding interfaces.KNProxyBinding) (*interfaces.KNProxyAccount, error) {
	f.seq.add("resolve")
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
// mcp-1/send_sms, with the proxy-read dependencies and the info log observable.
type boundToolFixture struct {
	service  *knActionRecallServiceImpl
	query    *mocks.MockDrivenOntologyQuery
	operator *mocks.MockDrivenOperatorIntegration
	reader   *mocks.MockToolDetailReaderAs
	authz    *fakeActionTypeAuthz
	resolver *fakeProxyResolver
	seq      *callSequence
	infoLogs []string
}

func newBoundToolFixture(t *testing.T) *boundToolFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	seq := &callSequence{}
	f := &boundToolFixture{
		query:    mocks.NewMockDrivenOntologyQuery(ctrl),
		operator: mocks.NewMockDrivenOperatorIntegration(ctrl),
		reader:   mocks.NewMockToolDetailReaderAs(ctrl),
		authz:    &fakeActionTypeAuthz{seq: seq},
		resolver: &fakeProxyResolver{seq: seq},
		seq:      seq,
	}
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes().Do(func(format string, args ...any) {
		f.infoLogs = append(f.infoLogs, fmt.Sprintf(format, args...))
	})
	f.service = &knActionRecallServiceImpl{
		logger: logger,
		config: &config.Config{OntologyQuery: config.PrivateBaseConfig{
			PrivateProtocol: "http", PrivateHost: "ontology-query", PrivatePort: 13018,
		}},
		ontologyQuery:       f.query,
		operatorIntegration: f.operator,
		actionAuthz:         f.authz,
		proxyResolver:       f.resolver,
		toolReaderAs:        f.reader,
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
	toolSource    = interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box-1", ToolID: "tool-1"}
	mcpSource     = interfaces.ActionSource{Type: interfaces.ActionSourceTypeMCP, McpID: "mcp-1", ToolName: "send_sms"}
	proxyIdentity = interfaces.AccountIdentity{ID: "proxy-1", Type: interfaces.AccessorTypeApp}
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

func callerCtx() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID: "user-1", AccountType: interfaces.AccessorTypeUser,
	})
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

	resp, err := f.service.GetActionInfo(callerCtx(), request())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.DynamicTools) != 1 || resp.DynamicTools[0].Name != "send_sms" {
		t.Fatalf("dynamic tools = %+v", resp.DynamicTools)
	}
	if len(f.seq.steps) != 0 || len(f.infoLogs) != 0 {
		t.Fatalf("proxy path consulted: steps=%v logs=%v", f.seq.steps, f.infoLogs)
	}
}

// The #1548 scenario: view on the action type, no grant on the bound tool. After the caller check
// and the binding check, the definition is read as the network's proxy account, and get_action_info
// answers with the very tool the direct read would have produced. One log line names both
// principals and the target.
func TestGetActionInfoReadsTheBoundToolAsTheProxyWithoutAToolGrant(t *testing.T) {
	direct := newBoundToolFixture(t)
	direct.expectAction(toolSource)
	direct.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(boundToolDetail(), nil)
	want, err := direct.service.GetActionInfo(callerCtx(), request())
	if err != nil {
		t.Fatal(err)
	}

	f := newBoundToolFixture(t)
	f.expectAction(toolSource)
	f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
	f.reader.EXPECT().GetToolDetailAs(gomock.Any(), proxyIdentity,
		&interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}).
		DoAndReturn(func(context.Context, interfaces.AccountIdentity,
			*interfaces.GetToolDetailRequest) (*interfaces.GetToolDetailResponse, error) {
			f.seq.add("read")
			return boundToolDetail(), nil
		})

	got, err := f.service.GetActionInfo(callerCtx(), request())
	if err != nil {
		t.Fatalf("GetActionInfo() error = %v", err)
	}
	if !reflect.DeepEqual(got.DynamicTools, want.DynamicTools) {
		t.Fatalf("proxy read built %+v, direct read builds %+v", got.DynamicTools, want.DynamicTools)
	}
	if !reflect.DeepEqual(f.seq.steps, []string{"authz", "resolve", "read"}) {
		t.Fatalf("steps = %v, want the caller check, then the binding, then the read", f.seq.steps)
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
	if len(f.infoLogs) != 1 {
		t.Fatalf("info logs = %v, want exactly one line for the proxied read", f.infoLogs)
	}
	for _, part := range []string{"caller=user-1", "kn_id=kn-1", "proxy=proxy-1", "tool_box=box-1", "tool=tool-1", "result=ok"} {
		if !strings.Contains(f.infoLogs[0], part) {
			t.Errorf("log line %q lacks %q", f.infoLogs[0], part)
		}
	}
}

func TestGetActionInfoReadsTheBoundMCPToolAsTheProxyWithoutAnMCPGrant(t *testing.T) {
	direct := newBoundToolFixture(t)
	direct.expectAction(mcpSource)
	direct.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(boundMCPToolDetail(), nil)
	want, err := direct.service.GetActionInfo(callerCtx(), request())
	if err != nil {
		t.Fatal(err)
	}

	f := newBoundToolFixture(t)
	f.expectAction(mcpSource)
	f.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
	f.reader.EXPECT().GetMCPToolDetailAs(gomock.Any(), proxyIdentity,
		&interfaces.GetMCPToolDetailRequest{McpID: "mcp-1", ToolName: "send_sms"}).
		Return(boundMCPToolDetail(), nil)

	got, err := f.service.GetActionInfo(callerCtx(), request())
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
	if len(f.infoLogs) != 1 || !strings.Contains(f.infoLogs[0], "mcp=mcp-1") ||
		!strings.Contains(f.infoLogs[0], "tool=send_sms") {
		t.Fatalf("info logs = %v", f.infoLogs)
	}
}

// Every check on the way to the proxy fails closed with its own answer, and a refusal stays a
// refusal: nothing here turns into 502. The read is never made unless both checks passed.
func TestGetActionInfoProxyReadFailsClosed(t *testing.T) {
	unavailable := infraErr.DefaultHTTPError(context.Background(), http.StatusServiceUnavailable, "sync pending")
	tests := []struct {
		name        string
		source      interfaces.ActionSource
		authzErr    error
		resolverErr error
		readerErr   error
		dropReader  bool
		wantStatus  int
		wantSteps   []string
		wantRead    bool
	}{
		{name: "no view on the action type", source: toolSource,
			authzErr:   infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, "no view"),
			wantStatus: http.StatusForbidden, wantSteps: []string{"authz"}},
		{name: "not the current published binding", source: toolSource,
			resolverErr: infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, "binding invalid"),
			wantStatus:  http.StatusForbidden, wantSteps: []string{"authz", "resolve"}},
		{name: "proxy not synchronized", source: mcpSource, resolverErr: unavailable,
			wantStatus: http.StatusServiceUnavailable, wantSteps: []string{"authz", "resolve"}},
		{name: "proxy grant revoked", source: toolSource, readerErr: forbidden(),
			wantStatus: http.StatusForbidden, wantSteps: []string{"authz", "resolve"}, wantRead: true},
		{name: "action source names no box", source: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, ToolID: "tool-1"},
			wantStatus: http.StatusForbidden, wantSteps: []string{"authz"}},
		{name: "reader not wired", source: toolSource, dropReader: true,
			wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newBoundToolFixture(t)
			f.authz.err = test.authzErr
			f.resolver.err = test.resolverErr
			if test.dropReader {
				f.service.toolReaderAs = nil
			}
			f.expectAction(test.source)
			if test.source.Type == interfaces.ActionSourceTypeTool {
				f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
				if test.wantRead {
					f.reader.EXPECT().GetToolDetailAs(gomock.Any(), proxyIdentity, gomock.Any()).
						Return(nil, test.readerErr)
				}
			} else {
				f.operator.EXPECT().GetMCPToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
			}

			resp, err := f.service.GetActionInfo(callerCtx(), request())

			if resp != nil || statusOf(err) != test.wantStatus {
				t.Fatalf("GetActionInfo() = %+v, %v; want HTTP %d", resp, err, test.wantStatus)
			}
			if !reflect.DeepEqual(f.seq.steps, test.wantSteps) && !(len(f.seq.steps) == 0 && len(test.wantSteps) == 0) {
				t.Fatalf("steps = %v, want %v", f.seq.steps, test.wantSteps)
			}
		})
	}
}

// Only a refusal for want of a tool grant is replaced. A 401, a 404 or a dependency failure on the
// direct read is the answer, and the proxy is not asked.
func TestGetActionInfoFallsBackOnlyForAToolGrantRefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newBoundToolFixture(t)
			f.expectAction(toolSource)
			f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).
				Return(nil, infraErr.DefaultHTTPError(context.Background(), status, "direct read failed"))

			_, err := f.service.GetActionInfo(callerCtx(), request())

			if statusOf(err) != status {
				t.Fatalf("status = %d, want %d", statusOf(err), status)
			}
			if len(f.seq.steps) != 0 {
				t.Fatalf("proxy path consulted: %v", f.seq.steps)
			}
		})
	}
}
