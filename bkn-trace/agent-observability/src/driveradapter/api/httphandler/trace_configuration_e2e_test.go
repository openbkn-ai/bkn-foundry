package httphandler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	filetraceconfigstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/fileaccess/traceconfigstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

type successfulE2ERollout struct{}

func (successfulE2ERollout) Run(_ context.Context, _ traceconfig.Request) traceconfig.Result {
	return traceconfig.Result{Phase: traceconfig.PhaseSucceeded}
}

func TestTraceEvidenceConfigurationEndToEndPersistsAndCompletesRelease(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	store := filetraceconfigstore.New(statePath)
	service := traceconfig.NewConfigurationServiceWithStore(store)
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		TenantID: "tenant-a", ActorID: "admin-a", EffectiveSubjectID: "admin-a", Roles: []string{"super_admin"}, AccountActive: true, TenantActive: true,
	}}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver})
	handler := NewTraceEvidenceConfigurationHandler(service, authorizer)

	put := authenticatedQueryRequest(http.MethodPut, "/api/observability/v1/trace-evidence-configuration", strings.NewReader(`{"enabled":true,"expected_revision":0}`))
	putResponse := httptest.NewRecorder()
	authorizer.RequireTrustedQueryIdentity(handler.ServeHTTP)(putResponse, put)
	if putResponse.Code != http.StatusAccepted {
		t.Fatalf("put: %d %s", putResponse.Code, putResponse.Body.String())
	}

	controller := traceconfig.NewController(store, successfulE2ERollout{})
	processed, err := controller.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("controller: processed=%v err=%v", processed, err)
	}

	restartedService := traceconfig.NewConfigurationServiceWithStore(filetraceconfigstore.New(statePath))
	restartedState := restartedService.Current(context.Background())
	if len(restartedState.History) != 1 || restartedState.History[0].Phase != traceconfig.PhaseEnabled || restartedState.History[0].CompletedAt == nil {
		t.Fatalf("persistent audit ledger was not restored: %+v", restartedState.History)
	}
	restartedHandler := NewTraceEvidenceConfigurationHandler(restartedService, authorizer)
	get := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/trace-evidence-configuration", nil)
	getResponse := httptest.NewRecorder()
	authorizer.RequireTrustedQueryIdentity(restartedHandler.ServeHTTP)(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"effective_enabled":true`) || !strings.Contains(getResponse.Body.String(), `"phase":"enabled"`) {
		t.Fatalf("get: %d %s", getResponse.Code, getResponse.Body.String())
	}
}
