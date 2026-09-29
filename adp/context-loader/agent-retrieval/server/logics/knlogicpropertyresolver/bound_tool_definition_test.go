// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knlogicpropertyresolver

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// callSequence records, in order, which of the proxy-read steps ran.
type callSequence struct{ steps []string }

func (c *callSequence) add(step string) { c.steps = append(c.steps, step) }

type fakeObjectTypeAuthz struct {
	err   error
	calls [][2]string
	seq   *callSequence
}

func (f *fakeObjectTypeAuthz) AuthorizeObjectTypeView(_ context.Context, knID, otID string) error {
	f.seq.add("authz")
	f.calls = append(f.calls, [2]string{knID, otID})
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

// logicToolFixture wires generateToolParams around object type ot-1 in kn-1, whose logic property
// "margin" is computed by box-1/tool-1. The prompt sent to the model is captured, because whether
// the tool's schema reached it is the whole point.
type logicToolFixture struct {
	service  *knLogicPropertyResolverService
	operator *mocks.MockDrivenOperatorIntegration
	reader   *mocks.MockToolDetailReaderAs
	authz    *fakeObjectTypeAuthz
	resolver *fakeProxyResolver
	seq      *callSequence
	prompt   string
}

func newLogicToolFixture(t *testing.T) *logicToolFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	seq := &callSequence{}
	mfClient := mocks.NewMockDrivenMFModelAPIClient(ctrl)
	f := &logicToolFixture{
		operator: mocks.NewMockDrivenOperatorIntegration(ctrl),
		reader:   mocks.NewMockToolDetailReaderAs(ctrl),
		authz:    &fakeObjectTypeAuthz{seq: seq},
		resolver: &fakeProxyResolver{seq: seq},
		seq:      seq,
	}
	mfClient.EXPECT().Chat(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *interfaces.LLMChatReq) (string, error) {
			f.prompt = req.Messages[len(req.Messages)-1].Content
			return `{"margin": {"order_id": "o-1"}}`, nil
		})
	f.service = &knLogicPropertyResolverService{
		logger:          &noopLogger{},
		dynamicLLM:      newDynamicParamsLLM(&noopLogger{}, mfClient),
		toolReader:      f.operator,
		toolReaderAs:    f.reader,
		proxyResolver:   f.resolver,
		objectTypeAuthz: f.authz,
	}
	return f
}

func (f *logicToolFixture) generate(t *testing.T) map[string]any {
	t.Helper()
	params, missing, err := f.service.generateToolParams(context.Background(),
		&interfaces.ResolveLogicPropertiesRequest{KnID: "kn-1", OtID: "ot-1", Query: "margin of o-1"},
		&interfaces.LogicPropertyDef{
			Name: "margin", Type: "tool",
			DataSource: map[string]any{"type": "tool", "box_id": "box-1", "tool_id": "tool-1"},
		}, "margin", nil)
	if err != nil || missing != nil {
		t.Fatalf("generateToolParams err = %v missing = %#v", err, missing)
	}
	return params
}

func boundToolDetail() *interfaces.GetToolDetailResponse {
	detail := &interfaces.GetToolDetailResponse{ToolID: "tool-1", Name: "gross_margin"}
	detail.Metadata.APISpec = map[string]any{"request_body": map[string]any{"marker": "tool-1-schema"}}
	return detail
}

func forbidden() error {
	return infraErr.DefaultHTTPError(context.Background(), http.StatusForbidden, "no grant on the tool box")
}

// A caller that holds a grant on the tool box reads it as itself; the proxy is never asked.
func TestLogicToolSchemaWithToolGrantNeverUsesTheProxy(t *testing.T) {
	f := newLogicToolFixture(t)
	f.operator.EXPECT().GetToolDetail(gomock.Any(), &interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}).
		Return(boundToolDetail(), nil)

	if params := f.generate(t); params["order_id"] != "o-1" {
		t.Fatalf("params = %#v", params)
	}
	if !strings.Contains(f.prompt, "tool-1-schema") {
		t.Fatalf("the tool schema did not reach the model: %s", f.prompt)
	}
	if len(f.seq.steps) != 0 {
		t.Fatalf("proxy steps ran for a caller with a tool grant: %v", f.seq.steps)
	}
}

// Regression: a caller who may view the object type but holds no tool box grant had its
// parameters generated from an empty schema. The schema is now read as the network's proxy, after
// the caller's own object-type check and the binding check, in that order.
func TestLogicToolSchemaIsReadAsTheProxyWithoutAToolGrant(t *testing.T) {
	f := newLogicToolFixture(t)
	f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())
	f.reader.EXPECT().GetToolDetailAs(gomock.Any(),
		interfaces.AccountIdentity{ID: "proxy-1", Type: interfaces.AccessorTypeApp},
		&interfaces.GetToolDetailRequest{BoxID: "box-1", ToolID: "tool-1"}).
		DoAndReturn(func(context.Context, interfaces.AccountIdentity,
			*interfaces.GetToolDetailRequest) (*interfaces.GetToolDetailResponse, error) {
			f.seq.add("read")
			return boundToolDetail(), nil
		})

	f.generate(t)

	if !strings.Contains(f.prompt, "tool-1-schema") {
		t.Fatalf("the tool schema did not reach the model: %s", f.prompt)
	}
	if want := []string{"authz", "resolve", "read"}; !reflect.DeepEqual(f.seq.steps, want) {
		t.Fatalf("steps = %v, want %v", f.seq.steps, want)
	}
	if want := [][2]string{{"kn-1", "ot-1"}}; !reflect.DeepEqual(f.authz.calls, want) {
		t.Fatalf("object-type checks = %v, want %v", f.authz.calls, want)
	}
	want := interfaces.KNProxyBinding{
		KNID:      "kn-1",
		ChildType: "logic_property",
		ChildID:   logicPropertyBindingID("kn-1", "ot-1", "margin"),
		TargetID:  "box-1",
		Operation: "execute",
	}
	if len(f.resolver.bindings) != 1 || f.resolver.bindings[0] != want {
		t.Fatalf("resolved bindings = %#v, want %#v", f.resolver.bindings, want)
	}
}

// Every refusal on the way to the proxy stops there, and the proxy read never runs. Generation
// still goes on without a schema, as it did before the proxy existed.
func TestLogicToolSchemaProxyReadFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*logicToolFixture)
		wantSteps []string
	}{
		{name: "no view on the object type", setup: func(f *logicToolFixture) {
			f.authz.err = forbidden()
		}, wantSteps: []string{"authz"}},
		{name: "not a published binding", setup: func(f *logicToolFixture) {
			f.resolver.err = forbidden()
		}, wantSteps: []string{"authz", "resolve"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLogicToolFixture(t)
			tt.setup(f)
			f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).Return(nil, forbidden())

			f.generate(t)

			if strings.Contains(f.prompt, "tool-1-schema") {
				t.Fatalf("a schema reached the model after a refusal: %s", f.prompt)
			}
			if !reflect.DeepEqual(f.seq.steps, tt.wantSteps) {
				t.Fatalf("steps = %v, want %v", f.seq.steps, tt.wantSteps)
			}
		})
	}
}

// Only a refusal for want of a tool grant is replaced. A missing tool, an expired session or an
// unavailable Execution Factory is the answer, and the proxy is not asked.
func TestLogicToolSchemaFallsBackOnlyForAToolGrantRefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newLogicToolFixture(t)
			f.operator.EXPECT().GetToolDetail(gomock.Any(), gomock.Any()).
				Return(nil, infraErr.DefaultHTTPError(context.Background(), status, "refused"))

			f.generate(t)

			if len(f.seq.steps) != 0 {
				t.Fatalf("proxy steps ran after %d: %v", status, f.seq.steps)
			}
		})
	}
}

// The child id is how BKN, ontology-query and this service name the same grant source. The digest
// below is sha256("kn-1\x00logic_property\x00ot-1\x00margin"), the formula bkn-backend's
// stableProxySourceID and ontology-query's logicPropertyBindingID both use.
func TestLogicPropertyBindingIDMatchesBKN(t *testing.T) {
	const want = "18668bad695b2a39f284a76a4c1b6f05162084964498e708bf8d576f6ee63544"
	if got := logicPropertyBindingID("kn-1", "ot-1", "margin"); got != want {
		t.Fatalf("logicPropertyBindingID = %s, want %s", got, want)
	}
}
