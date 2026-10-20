package sandbox

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

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

func TestExecutionSpanAttributesLeaveOutTheEvent(t *testing.T) {
	attrs := executionSpanAttributes(credentialBearingRequest())

	if _, ok := attrs["event"]; ok {
		t.Fatalf("span attributes must not include the event: %v", attrs)
	}
	raw, _ := json.Marshal(attrs)
	if containsToken(raw) {
		t.Fatalf("span attributes leak the caller token: %s", raw)
	}
	if attrs["code"] == "" || attrs["language"] != "python" {
		t.Fatalf("span attributes lost the execution shape: %v", attrs)
	}
}

func TestDependencyInstallFailureDetailsLeaveOutCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockSandBoxControlPlane(ctrl)
	pool := &sessionPoolImpl{
		client:             client,
		sessions:           map[string]*sessionItem{},
		maxSessions:        1,
		maxConcurrentTasks: 10,
		logger:             logger.DefaultLogger(),
		stopCh:             make(chan struct{}),
		templateID:         "python-basic",
		reqConfig:          config.SessionResourcesConfig{CPU: "1", Memory: "512Mi", Disk: "1Gi", Timeout: 3600},
	}
	gomock.InOrder(
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").Return(false, nil, nil),
		client.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil, nil),
		client.EXPECT().QuerySession(gomock.Any(), "sess_aoi_0").
			Return(true, &interfaces.SessionDetail{ID: "sess_aoi_0", Status: interfaces.SessionStatusRunning}, nil),
		client.EXPECT().InstallPythonDependencies(gomock.Any(), "sess_aoi_0", gomock.Any()).
			Return(nil, stderrors.New("index unreachable")),
	)

	_, err := pool.ExecuteCode(context.Background(), credentialBearingRequest())

	var httpErr *errors.HTTPError
	if !stderrors.As(err, &httpErr) {
		t.Fatalf("want an HTTP error, got %v", err)
	}
	raw, _ := json.Marshal(httpErr)
	if containsToken(raw) {
		t.Fatalf("error details leak the caller token: %s", raw)
	}
	details, _ := httpErr.ErrorDetails.(map[string]any)
	if details["dependencies"] == nil || details["dependencies_url"] == nil {
		t.Fatalf("error details lost the dependency context: %s", raw)
	}
}

func containsToken(raw []byte) bool {
	return strings.Contains(string(raw), callerToken)
}
