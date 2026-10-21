package sandbox

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// run_code sends the caller's bearer token in the event, and the execution context
// carries it as BKN_TOKEN (#2294). Neither may leave through telemetry or errors.
const callerToken = "ory_at_caller_secret"

func credentialBearingRequest() *interfaces.ExecuteCodeReq {
	return &interfaces.ExecuteCodeReq{
		Code:                  "def handler(event):\n    return event",
		Event:                 map[string]any{"token": callerToken, "mcp": "http://agent-retrieval"},
		Language:              "python",
		Timeout:               30,
		EnvVars:               map[string]any{"BKN_TOKEN": callerToken},
		Dependencies:          []*interfaces.DependencyInfo{{Name: "requests", Version: "2.32.3"}},
		PythonPackageIndexURL: "https://pypi.org/simple/",
	}
}

func newCredentialTestPool(client *mocks.MockSandBoxControlPlane) *sessionPoolImpl {
	return &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        1,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		stopCh:             make(chan struct{}),
		templateID:         "python-basic",
		reqConfig:          config.SessionResourcesConfig{CPU: "1", Memory: "512Mi", Disk: "1Gi", Timeout: 3600},
	}
}

// expectRunningSession has the pool create sess_aoi_0 and see it running.
func expectRunningSession(client *mocks.MockSandBoxControlPlane) []any {
	return []any{
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").
			Return(true, &interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusRunning}, nil),
	}
}

// recordSpans routes the global tracer, which oteltrace.StartInternalSpan reads on
// every call, into an in-memory recorder for the duration of the test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

// assertSpansCarryNoToken checks every exported span, not just the one ExecuteCode
// opens, so a token written by any span on the path is caught. It also requires
// the execute span's own attributes, so a recorder that saw nothing cannot pass.
func assertSpansCarryNoToken(t *testing.T, recorder *tracetest.SpanRecorder) {
	t.Helper()
	spans := recorder.Ended()
	sawExecuteSpan := false
	for _, span := range spans {
		for _, kv := range span.Attributes() {
			if strings.Contains(kv.Value.Emit(), callerToken) {
				t.Fatalf("span %q attribute %q leaks the caller token", span.Name(), kv.Key)
			}
			if kv.Key == "language" && kv.Value.AsString() == "python" {
				sawExecuteSpan = true
			}
		}
		for _, event := range span.Events() {
			for _, kv := range event.Attributes {
				if strings.Contains(kv.Value.Emit(), callerToken) {
					t.Fatalf("span %q event %q leaks the caller token", span.Name(), event.Name)
				}
			}
		}
		if strings.Contains(span.Status().Description, callerToken) {
			t.Fatalf("span %q status leaks the caller token", span.Name())
		}
	}
	if !sawExecuteSpan {
		t.Fatalf("no ExecuteCode span recorded among %d spans", len(spans))
	}
}

func TestExecuteCodeSpansCarryNoCredentials(t *testing.T) {
	recorder := recordSpans(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockSandBoxControlPlane(ctrl)
	calls := append(expectRunningSession(client),
		client.EXPECT().ExecuteCodeSync(gomock.Any(), "sess_aoi_0", gomock.Any()).
			Return(&interfaces.ExecuteCodeResp{SessionID: "sess_aoi_0"}, nil),
	)
	gomock.InOrder(calls...)
	req := credentialBearingRequest()
	req.Dependencies = nil

	if _, err := newCredentialTestPool(client).ExecuteCode(context.Background(), req); err != nil {
		t.Fatalf("ExecuteCode: %v", err)
	}

	assertSpansCarryNoToken(t, recorder)
}

func TestDependencyInstallFailureCarriesNoCredentials(t *testing.T) {
	recorder := recordSpans(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockSandBoxControlPlane(ctrl)
	calls := append(expectRunningSession(client),
		client.EXPECT().InstallPythonDependencies(gomock.Any(), "sess_aoi_0", gomock.Any()).
			Return(nil, stderrors.New("index unreachable")),
	)
	gomock.InOrder(calls...)

	_, err := newCredentialTestPool(client).ExecuteCode(context.Background(), credentialBearingRequest())

	var httpErr *errors.HTTPError
	if !stderrors.As(err, &httpErr) {
		t.Fatalf("want an HTTP error, got %v", err)
	}
	raw, _ := json.Marshal(httpErr)
	if strings.Contains(string(raw), callerToken) {
		t.Fatalf("error details leak the caller token: %s", raw)
	}
	details, _ := httpErr.ErrorDetails.(map[string]any)
	if details["dependencies"] == nil || details["dependencies_url"] == nil {
		t.Fatalf("error details lost the dependency context: %s", raw)
	}
	assertSpansCarryNoToken(t, recorder)
}
