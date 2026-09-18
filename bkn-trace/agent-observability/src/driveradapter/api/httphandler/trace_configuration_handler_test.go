package httphandler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

func TestTraceEvidenceConfigurationHandlerReturnsDefaultDisabledAndQueuesAuthorizedChange(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID: "admin-a", EffectiveSubjectID: "admin-a", Roles: []string{"super_admin"}, AccountActive: true,
	}}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	handler := NewTraceEvidenceConfigurationHandler(traceconfig.NewConfigurationService(), authorizer)

	get := authenticatedQueryRequest(http.MethodGet, "/api/observability/v1/trace-evidence-configuration", nil)
	getRec := httptest.NewRecorder()
	authorizer.RequireTrustedQueryIdentity(handler.ServeHTTP)(getRec, get)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `"desired_enabled":false`) {
		t.Fatalf("expected default disabled response, got %d: %s", getRec.Code, getRec.Body.String())
	}

	put := authenticatedQueryRequest(http.MethodPut, "/api/observability/v1/trace-evidence-configuration", strings.NewReader(`{"enabled":true,"expected_revision":0}`))
	putRec := httptest.NewRecorder()
	authorizer.RequireTrustedQueryIdentity(handler.ServeHTTP)(putRec, put)
	if putRec.Code != http.StatusAccepted || !strings.Contains(putRec.Body.String(), `"effective_enabled":false`) || !strings.Contains(putRec.Body.String(), `"phase":"pending"`) {
		t.Fatalf("expected queued operation, got %d: %s", putRec.Code, putRec.Body.String())
	}
}

func TestTraceEvidenceConfigurationHandlerFailsClosedWithoutWriteCapability(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID: "user-a", EffectiveSubjectID: "user-a", AccountActive: true,
	}}
	authorizer := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	handler := NewTraceEvidenceConfigurationHandler(traceconfig.NewConfigurationService(), authorizer)
	put := authenticatedQueryRequest(http.MethodPut, "/api/observability/v1/trace-evidence-configuration", strings.NewReader(`{"enabled":true,"expected_revision":0}`))
	putRec := httptest.NewRecorder()
	authorizer.RequireTrustedQueryIdentity(handler.ServeHTTP)(putRec, put)
	if putRec.Code != http.StatusForbidden || !strings.Contains(putRec.Body.String(), "OBSERVABILITY_CONFIGURATION_FORBIDDEN") {
		t.Fatalf("expected write denial, got %d: %s", putRec.Code, putRec.Body.String())
	}
}
