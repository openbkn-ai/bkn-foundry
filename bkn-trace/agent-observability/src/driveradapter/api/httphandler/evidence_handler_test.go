// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type staticProjectionSource struct {
	result iprojectionsource.Result
}

func (s staticProjectionSource) LoadExecutionProjection(
	_ context.Context,
	query iprojectionsource.Query,
) (iprojectionsource.Result, error) {
	result := iprojectionsource.Result{Artifacts: s.result.Artifacts, Truncated: s.result.Truncated}
	for _, trace := range s.result.Traces {
		if query.TraceID != "" && trace.TraceID != query.TraceID {
			continue
		}
		if !evidencevo.MatchesScope(trace, query.Scope) {
			continue
		}
		result.Traces = append(result.Traces, trace)
	}
	return result, nil
}

type fakeAccessScopeResolver struct {
	profile         evidencevo.AccessProfile
	err             error
	calls           int
	authorization   string
	trustedIdentity iauthorizationscope.TrustedIdentity
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f *fakeAccessScopeResolver) Resolve(
	_ context.Context,
	authorization string,
	identity iauthorizationscope.TrustedIdentity,
) (evidencevo.AccessProfile, error) {
	f.calls++
	f.authorization = authorization
	f.trustedIdentity = identity
	return f.profile, f.err
}

func newDevEvidenceHandler(service *evidencesvc.Service) *EvidenceHandler {
	return NewEvidenceHandlerWithSecurityConfig(service, EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedIngest: true,
		AllowUnauthenticatedQuery:  true,
	})
}

func authenticatedQueryRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("x-account-id", "acct_demo")
	req.Header.Set("x-account-type", "app")
	return req
}

func TestEvidenceHandlerFailsClosedWhenIngestTokenIsUnconfigured(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurity(evidencesvc.New(evidencestore.New()), "", false)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "INGEST_AUTH_NOT_CONFIGURED") {
		t.Fatalf("expected fail-closed configuration error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalLifecycleIdentityRequiresInternalListenerNotSharedToken(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{})
	next := handler.RequireTrustedLifecycleIdentity(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	request := authenticatedQueryRequest(http.MethodPost, "/api/agent-observability/v1/conversations", nil)
	request.Header.Set("X-BKN-Trace-Query-Token", "legacy-shared-token")
	request.Header.Set("X-BKN-Application-Principal-ID", "agent-retrieval")
	request.Header.Set("X-BKN-Effective-Subject-Type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "acct_demo")
	recorder := httptest.NewRecorder()
	next(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("public request with legacy shared token = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	trusted := httptest.NewRecorder()
	handler.InternalLifecycle(handler.RequireTrustedLifecycleIdentity(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))(trusted, request)
	if trusted.Code != http.StatusNoContent {
		t.Fatalf("internal lifecycle request = %d, want %d: %s", trusted.Code, http.StatusNoContent, trusted.Body.String())
	}
}

func TestPublicLifecycleIdentityDerivesOwnerFromOAuth(t *testing.T) {
	tests := []struct {
		name          string
		introspection string
		accountType   string
		subjectType   string
		subjectID     string
		applicationID string
		resolverAppID string
	}{
		{
			name:          "user",
			introspection: `{"active":true,"sub":"user-1","client_id":"openbkn-cli","ext":{"visitor_type":"user"}}`,
			accountType:   "user", subjectType: "user", subjectID: "user-1", applicationID: "openbkn-cli",
			resolverAppID: "",
		},
		{
			name:          "application",
			introspection: `{"active":true,"sub":"agent-app","client_id":"agent-app","ext":{"visitor_type":"app"}}`,
			accountType:   "app", subjectType: "service", subjectID: "agent-app", applicationID: "agent-app",
			resolverAppID: "agent-app",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
				ActorID:            test.subjectID,
				EffectiveSubjectID: test.subjectID, ApplicationPrincipalID: test.resolverAppID,
				AccountActive: true}}
			handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
				HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != "/admin/oauth2/introspect" {
						t.Fatalf("unexpected OAuth introspection path: %s", request.URL.Path)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(test.introspection)),
					}, nil
				})},
				AuthorizationScopeResolver: resolver,
			})
			request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
			request.Header.Set("Authorization", "Bearer lifecycle-token")
			response := httptest.NewRecorder()
			nextCalled := false

			handler.RequirePublicLifecycleIdentity(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				owner, ok := trustedOwnerFromRequest(r)
				if !ok {
					t.Fatalf("public lifecycle owner is incomplete: %+v", owner)
				}
				if owner.ApplicationPrincipalID != test.applicationID ||
					string(owner.EffectiveSubjectType) != test.subjectType ||
					owner.EffectiveSubjectID != test.subjectID {
					t.Fatalf("unexpected public lifecycle owner: %+v", owner)
				}
				w.WriteHeader(http.StatusNoContent)
			})(response, request)

			if response.Code != http.StatusNoContent || !nextCalled {
				t.Fatalf("public lifecycle identity = %d, want 204: %s", response.Code, response.Body.String())
			}
			if resolver.trustedIdentity.ApplicationPrincipalID != test.resolverAppID ||
				resolver.trustedIdentity.EffectiveSubjectID != test.subjectID ||
				request.Header.Get("x-account-type") != test.accountType {
				t.Fatalf("unexpected authorized identity: scope=%+v headers=%v", resolver.trustedIdentity, request.Header)
			}
		})
	}
}

func TestPublicLifecycleIdentityRejectsAnonymousQueryCompatibilityMode(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("x-account-id", "forged-user")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()
	nextCalled := false

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})(response, request)

	if response.Code != http.StatusUnauthorized || nextCalled {
		t.Fatalf("anonymous compatibility mode authorized public lifecycle: %d %s", response.Code, response.Body.String())
	}
}

func TestPublicLifecycleIdentityRejectsDisabledSurface(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AuthorizationScopeResolver: &fakeAccessScopeResolver{},
		DisablePublicLifecycle:     true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()
	nextCalled := false

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})(response, request)

	if response.Code != http.StatusServiceUnavailable || nextCalled {
		t.Fatalf("disabled public lifecycle surface accepted a write: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "authorization_unavailable") {
		t.Fatalf("disabled public lifecycle surface must answer authorization_unavailable: %s", response.Body.String())
	}
}

func TestPublicLifecycleEnabledEnvOnlyDisablesOnExplicitFalse(t *testing.T) {
	for _, value := range []string{"", "true", "TRUE", "1", "yes"} {
		if publicLifecycleDisabled(value) {
			t.Fatalf("value %q must keep the public lifecycle surface open", value)
		}
	}
	for _, value := range []string{"false", "FALSE", " false "} {
		if !publicLifecycleDisabled(value) {
			t.Fatalf("value %q must disable the public lifecycle surface", value)
		}
	}
}

func TestPublicLifecycleIdentityOverridesForgedOwnerHeaders(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "user-1",
		EffectiveSubjectID: "user-1", AccountActive: true}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"active":true,"sub":"user-1","client_id":"openbkn-cli","ext":{"visitor_type":"user"}}`)),
			}, nil
		})},
		AuthorizationScopeResolver: resolver,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	request.Header.Set("X-BKN-Effective-Subject-Type", "service")
	response := httptest.NewRecorder()

	handler.RequirePublicLifecycleIdentity(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := trustedOwnerFromRequest(r)
		if !ok {
			t.Fatalf("public lifecycle owner is incomplete: %+v", owner)
		}
		if owner.ApplicationPrincipalID != "openbkn-cli" || owner.EffectiveSubjectType != "user" ||
			owner.EffectiveSubjectID != "user-1" {
			t.Fatalf("forged owner headers were not overwritten: %+v", owner)
		}
		w.WriteHeader(http.StatusNoContent)
	})(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("public lifecycle identity = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestPublicLifecycleIdentityUsesLifecycleAuthenticationErrorContract(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test"})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("X-Request-ID", "request-auth")
	response := httptest.NewRecorder()

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated lifecycle request reached handler")
	})(response, request)

	var envelope lifecycleErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode lifecycle authentication error: %v: %s", err, response.Body.String())
	}
	if response.Code != http.StatusUnauthorized || envelope.Error.Code != "permission_denied" ||
		envelope.Error.Retryable || envelope.Error.RequiredAction != "request_authorization" ||
		envelope.Error.RequestID != "request-auth" {
		t.Fatalf("unexpected lifecycle authentication error: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublicLifecycleIdentityReportsScopeResolverUnavailableAsRetryable(t *testing.T) {
	resolver := &fakeAccessScopeResolver{err: errors.New("BKN Safe connection refused")}
	handler := newPublicLifecycleTestHandler(t, resolver)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	response := httptest.NewRecorder()

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unavailable authorization dependency reached lifecycle handler")
	})(response, request)

	var envelope lifecycleErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode authorization dependency error: %v: %s", err, response.Body.String())
	}
	if response.Code != http.StatusServiceUnavailable || envelope.Error.Code != "authorization_unavailable" ||
		!envelope.Error.Retryable {
		t.Fatalf("unexpected authorization dependency error: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublicLifecycleIdentityReportsScopeResolverDenial(t *testing.T) {
	resolver := &fakeAccessScopeResolver{err: iauthorizationscope.ErrDenied}
	handler := newPublicLifecycleTestHandler(t, resolver)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	response := httptest.NewRecorder()

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		t.Fatal("denied authorization scope reached lifecycle handler")
	})(response, request)

	var envelope lifecycleErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode authorization denial: %v: %s", err, response.Body.String())
	}
	if response.Code != http.StatusForbidden || envelope.Error.Code != "permission_denied" || envelope.Error.Retryable {
		t.Fatalf("unexpected authorization denial: status=%d body=%s", response.Code, response.Body.String())
	}
}

func newPublicLifecycleTestHandler(t *testing.T, resolver iauthorizationscope.Resolver) *EvidenceHandler {
	t.Helper()
	return NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
				`{"active":true,"sub":"user-1","client_id":"openbkn-cli","ext":{"visitor_type":"user"}}`,
			))}, nil
		})},
		AuthorizationScopeResolver: resolver,
	})
}

func TestPublicLifecycleIdentityRejectsMismatchedAccessProfileBoundary(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "other-user",
		EffectiveSubjectID: "user-1", AccountActive: true}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: "http://hydra.test", QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"active":true,"sub":"user-1","client_id":"openbkn-cli","ext":{"visitor_type":"user"}}`,
				)),
			}, nil
		})},
		AuthorizationScopeResolver: resolver,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	response := httptest.NewRecorder()
	nextCalled := false

	handler.RequirePublicLifecycleIdentity(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})(response, request)

	if response.Code != http.StatusForbidden || nextCalled {
		t.Fatalf("mismatched access profile authorized public lifecycle: %d %s", response.Code, response.Body.String())
	}
}

func TestInternalLifecycleMarkerDoesNotBypassEvidenceIngestToken(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		IngestToken: "evidence-token",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	response := httptest.NewRecorder()

	handler.InternalLifecycle(handler.IngestEvidenceArtifact)(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("internal marker without evidence token = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

func TestEvidenceHandlerDevelopmentBypassesDefaultToDisabled(t *testing.T) {
	t.Setenv(evidenceIngestTokenEnv, "")
	t.Setenv(evidenceAllowUnauthenticatedIngestEnv, "")
	t.Setenv(evidenceAllowUnauthenticatedQueryEnv, "")
	handler := NewEvidenceHandler(evidencesvc.New(evidencestore.New()))

	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusServiceUnavailable || !strings.Contains(ingestRec.Body.String(), "INGEST_AUTH_NOT_CONFIGURED") {
		t.Fatalf("unauthenticated ingest must default off, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(queryRec, queryReq)
	if queryRec.Code != http.StatusServiceUnavailable || !strings.Contains(queryRec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("unauthenticated query must default off, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestIngestDevelopmentBypassDoesNotEnableQueryBypass(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurity(evidencesvc.New(evidencestore.New()), "", true)
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("ingest development bypass must not enable query bypass, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRequiresTrustedQueryIdentity(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_IDENTITY_REQUIRED") {
		t.Fatalf("expected trusted identity rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerBuildsQueryScopeFromCurrentSafeAccessProfile(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "actor-a",
		EffectiveSubjectID: "user-a", ApplicationPrincipalID: "app-a",
		Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-a"},
		AccountActive: true, Fingerprint: "sha256:profile-a",
	}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	request := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/request-a", nil)
	request.Header.Set("Authorization", "Bearer current-token")
	request.Header.Set("x-account-id", "actor-a")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "user-a")
	request.Header.Set("X-BKN-Application-Principal-ID", "app-a")
	response := httptest.NewRecorder()

	scope, ok := handler.queryScopeFromRequest(response, request, false)
	if !ok || scope.AccessProfile == nil || scope.AccessProfile.Fingerprint != "sha256:profile-a" {
		t.Fatalf("expected current access profile, scope=%+v response=%d %s", scope, response.Code, response.Body.String())
	}
	if scope.AccountID != "user-a" {
		t.Fatalf("candidate owner filter must use the effective subject, got %q", scope.AccountID)
	}
	if resolver.authorization != "Bearer current-token" || resolver.trustedIdentity.ActorID != "actor-a" ||
		resolver.trustedIdentity.EffectiveSubjectID != "user-a" || resolver.trustedIdentity.ApplicationPrincipalID != "app-a" {
		t.Fatalf("resolver did not receive trusted identity: auth=%q identity=%+v", resolver.authorization, resolver.trustedIdentity)
	}
}

func TestEvidenceHandlerReturnsCurrentAccessProfileCapabilities(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "builder-a",
		EffectiveSubjectID: "builder-a", Roles: []string{"network_builder"},
		ManagedKnowledgeNetworkIDs: []string{"kn-a"},
		AccountActive:              true, Fingerprint: "sha256:profile-a",
	}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AllowUnauthenticatedQuery: true, AuthorizationScopeResolver: resolver,
	})
	request := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/access-profile", nil)
	request.Header.Set("Authorization", "Bearer current-token")
	request.Header.Set("x-account-id", "builder-a")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	handler.GetAccessProfile(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected access profile, got %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["business_provenance_own"] != true || body["business_provenance_managed_networks"] != true ||
		body["technical_trace"] != true || body["security_audit"] != false ||
		body["management_audit"] != false || body["global_log_search"] != true ||
		body["log_export"] != true || body["log_policy_read"] != false ||
		body["log_sensitive_fields"] != false {
		t.Fatalf("unexpected capabilities: %#v", body)
	}
	categories, ok := body["allowed_log_categories"].([]any)
	if !ok || len(categories) != 3 || categories[0] != "runtime.system" ||
		categories[1] != "runtime.business" || categories[2] != "runtime.model" {
		t.Fatalf("unexpected allowed log categories: %#v", body)
	}
	if body["access_scope_fingerprint"] != "sha256:profile-a" {
		t.Fatalf("missing fingerprint: %#v", body)
	}
	if _, leaked := body["roles"]; leaked {
		t.Fatalf("access profile must not expose the complete role table: %#v", body)
	}
	if _, leaked := body["managed_knowledge_network_ids"]; leaked {
		t.Fatalf("access profile must not expose managed network identifiers: %#v", body)
	}
}

func TestEvidenceHandlerAccessProfileFailsClosedWithoutScopeResolver(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	request := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/access-profile", nil)
	response := httptest.NewRecorder()

	handler.GetAccessProfile(response, request)

	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "ACCESS_PROFILE_NOT_CONFIGURED") {
		t.Fatalf("expected fail-closed access profile response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestEvidenceHandlerRejectsForgedIdentityWithoutOAuth(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		IngestToken: "producer-ingest-token",
	})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("X-BKN-Trace-Ingest-Token", "producer-ingest-token")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("producer token and forged identity must not authorize query, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerFailsClosedWhenQueryGatewayTokenIsUnconfigured(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "QUERY_AUTH_NOT_CONFIGURED") {
		t.Fatalf("missing query gateway auth must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAuthenticatesStudioQueryWithHydra(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" || r.FormValue("token") != "studio-token" {
			t.Fatalf("unexpected introspection request: %s token=%q", r.URL.Path, r.FormValue("token"))
		}
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"realname"}}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer studio-token")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("valid OAuth query must reach scoped lookup, got %d: %s", rec.Code, rec.Body.String())
	}
	if req.Header.Get("x-account-id") != "acct_demo" || req.Header.Get("x-account-type") != "user" {
		t.Fatalf("trusted OAuth identity was not derived: headers=%v", req.Header)
	}
}

func TestTrustedQueryMiddlewareCachesResolvedScope(t *testing.T) {
	introspectionCalls := 0
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		introspectionCalls++
		if r.URL.Path != "/admin/oauth2/introspect" || r.FormValue("token") != "studio-token" {
			t.Fatalf("unexpected introspection request: %s token=%q", r.URL.Path, r.FormValue("token"))
		}
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"realname"}}`)
	}))
	defer hydra.Close()

	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "acct_demo",
		EffectiveSubjectID: "acct_demo", Roles: []string{"super_admin"},
		AccountActive: true, Fingerprint: "sha256:scope",
	}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL, AuthorizationScopeResolver: resolver,
	})
	next := handler.RequireTrustedQueryIdentity(func(w http.ResponseWriter, r *http.Request) {
		if !handler.authorizeQueryGateway(w, r) {
			return
		}
		scope, ok := handler.queryScopeFromRequest(w, r, false)
		if !ok || scope.AccessProfile == nil || scope.AccessProfile.Fingerprint != "sha256:scope" {
			http.Error(w, "trusted access scope was not cached", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/observability/v1/logs", nil)
	request.Header.Set("Authorization", "Bearer studio-token")
	response := httptest.NewRecorder()

	handler.InternalLifecycle(next)(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("trusted OAuth query must reach the protected handler: %d %s", response.Code, response.Body.String())
	}
	if introspectionCalls != 1 || resolver.calls != 1 {
		t.Fatalf("trusted query scope must be resolved once, introspections=%d resolver_calls=%d", introspectionCalls, resolver.calls)
	}
}

func TestTrustedQueryMiddlewareResolvesAuthenticatedScope(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"realname"}}`)
	}))
	defer hydra.Close()

	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "acct_demo",
		EffectiveSubjectID: "acct_demo", Roles: []string{"super_admin"},
		AccountActive: true, Fingerprint: "sha256:scope",
	}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL, AuthorizationScopeResolver: resolver,
	})
	next := handler.RequireTrustedQueryIdentity(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := trustedQueryScopeFromContext(r.Context())
		if !ok || scope.AccountID != "acct_demo" || scope.AccessProfile == nil {
			http.Error(w, "authenticated scope was not resolved", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/observability/v1/logs", nil)
	request.Header.Set("Authorization", "Bearer studio-token")
	response := httptest.NewRecorder()

	handler.InternalLifecycle(next)(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated identity must complete the trusted scope: %d %s", response.Code, response.Body.String())
	}
	if resolver.trustedIdentity.EffectiveSubjectID != "acct_demo" {
		t.Fatalf("resolver must receive the authenticated subject: %+v", resolver.trustedIdentity)
	}
}

func TestLifecycleIdentityRejectsUntrustedCaller(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" || r.FormValue("token") != "lifecycle-token" {
			t.Fatalf("unexpected introspection request: %s token=%q", r.URL.Path, r.FormValue("token"))
		}
		_, _ = io.WriteString(w, `{"active":true,"sub":"user-authenticated","client_id":"agent-authenticated","ext":{"visitor_type":"user"}}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	nextCalled := false
	next := handler.RequireTrustedLifecycleIdentity(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/conversations:ensure-current", nil)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	request.Header.Set("X-BKN-Application-Principal-ID", "forged-app")
	request.Header.Set("X-BKN-Effective-Subject-Type", "service")
	request.Header.Set("X-BKN-Effective-Subject-ID", "forged-subject")
	request.Header.Set("X-BKN-Delegation-ID", "forged-delegation")
	response := httptest.NewRecorder()

	next(response, request)

	if response.Code != http.StatusUnauthorized || nextCalled {
		t.Fatalf(
			"OAuth from an untrusted caller must be rejected: %d %s",
			response.Code, response.Body.String(),
		)
	}
}

func TestLifecycleIdentityRejectsAnonymousQueryCompatibilityMode(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true},
	)
	nextCalled := false
	next := handler.RequireTrustedLifecycleIdentity(func(
		http.ResponseWriter, *http.Request,
	) {
		nextCalled = true
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		nil,
	)
	request.Header.Set("X-BKN-Application-Principal-ID", "forged-app")
	request.Header.Set("X-BKN-Effective-Subject-ID", "forged-subject")
	response := httptest.NewRecorder()

	next(response, request)

	if response.Code != http.StatusUnauthorized || nextCalled {
		t.Fatalf(
			"anonymous query compatibility must not authorize lifecycle writes: %d %s",
			response.Code, response.Body.String(),
		)
	}
}

func TestLifecycleIdentityRejectsIncompleteOwnerTupleAtGatewayBoundary(t *testing.T) {
	handler := NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		EvidenceHandlerSecurityConfig{},
	)
	nextCalled := false
	next := handler.InternalLifecycle(handler.RequireTrustedLifecycleIdentity(func(
		http.ResponseWriter, *http.Request,
	) {
		nextCalled = true
	}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		nil,
	)
	request.Header.Set("x-account-id", "subject-1")
	request.Header.Set("x-account-type", "service")
	response := httptest.NewRecorder()

	next(response, request)

	if response.Code != http.StatusUnauthorized || nextCalled ||
		!strings.Contains(response.Body.String(), "complete lifecycle owner identity") {
		t.Fatalf(
			"incomplete owner tuple must be rejected at the gateway boundary: %d %s",
			response.Code, response.Body.String(),
		)
	}
}

func TestLifecycleIdentityAcceptsTrustedGatewayProducerWithoutOAuthScope(t *testing.T) {
	resolver := &fakeAccessScopeResolver{err: io.ErrUnexpectedEOF}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		IngestToken:                "producer-ingest-token",
		AuthorizationScopeResolver: resolver,
	})
	nextCalled := false
	next := handler.RequireTrustedLifecycleIdentity(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", nil)
	request.Header.Set("X-BKN-Trace-Query-Token", "gateway-query-token")
	request.Header.Set("x-account-id", "acct_e2e_demo")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("X-BKN-Application-Principal-ID", "bkn-backend")
	request.Header.Set("X-BKN-Effective-Subject-Type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "acct_e2e_demo")
	response := httptest.NewRecorder()

	handler.InternalLifecycle(next)(response, request)

	if response.Code != http.StatusNoContent || !nextCalled {
		t.Fatalf("trusted gateway producer must bypass OAuth scope resolver: %d %s", response.Code, response.Body.String())
	}
	if resolver.calls != 0 {
		t.Fatalf("OAuth scope resolver must not run for trusted gateway producer delivery")
	}
}

func TestQueryScopeWithGatewayTokenStillRunsResolverForReadPaths(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "actor-a",
		EffectiveSubjectID: "user-a", AccountActive: true}}
	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		AuthorizationScopeResolver: resolver,
	})
	request := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req-a/evidence-chain", nil)
	request.Header.Set("X-BKN-Trace-Query-Token", "gateway-query-token")
	request.Header.Set("x-account-id", "actor-a")
	request.Header.Set("x-account-type", "user")
	response := httptest.NewRecorder()

	scope, ok := handler.queryScopeFromRequest(response, request, false)
	if !ok || scope.AccessProfile == nil {
		t.Fatalf("read paths must still resolve record scope with gateway token: ok=%t scope=%+v body=%s", ok, scope, response.Body.String())
	}
	if resolver.trustedIdentity.ActorID != "actor-a" {
		t.Fatalf("OAuth scope resolver must run for read paths even with gateway token: identity=%+v", resolver.trustedIdentity)
	}
}

func TestLifecycleIdentityTrustsGatewayOwnerWithoutReadAuthorizationLookup(t *testing.T) {
	resolver := &fakeAccessScopeResolver{profile: evidencevo.AccessProfile{
		ActorID:            "subject-1",
		EffectiveSubjectID: "subject-1", AccountActive: true}}
	handler := NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		EvidenceHandlerSecurityConfig{
			AuthorizationScopeResolver: resolver,
		},
	)
	nextCalled := false
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent-observability/v1/conversations:ensure-current",
		nil,
	)
	request.Header.Set("X-BKN-Trace-Query-Token", "trusted-gateway-token")
	request.Header.Set("x-account-id", "subject-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("X-BKN-Application-Principal-ID", "context-loader")
	request.Header.Set("X-BKN-Effective-Subject-Type", "user")
	request.Header.Set("X-BKN-Effective-Subject-ID", "subject-1")
	response := httptest.NewRecorder()

	handler.InternalLifecycle(handler.RequireTrustedLifecycleIdentity(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		if _, ok := trustedQueryScopeFromContext(r.Context()); !ok {
			t.Fatal("trusted lifecycle scope was not attached to the request")
		}
		w.WriteHeader(http.StatusNoContent)
	}))(response, request)

	if response.Code != http.StatusNoContent || !nextCalled {
		t.Fatalf("trusted gateway lifecycle write was rejected: %d %s", response.Code, response.Body.String())
	}
	if resolver.calls != 0 {
		t.Fatalf("lifecycle writes must not perform read authorization lookups: %d", resolver.calls)
	}
}

func TestEvidenceHandlerRejectsOAuthIdentityMismatch(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"user"}}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer studio-token")
	req.Header.Set("x-account-id", "forged-account")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_IDENTITY_MISMATCH") {
		t.Fatalf("forged OAuth identity must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsForgedOAuthDelegationIdentity(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":true,"sub":"acct_demo","client_id":"openbkn-studio","ext":{"visitor_type":"user"}}`)
	}))
	defer hydra.Close()

	for _, test := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "effective subject", header: "X-BKN-Effective-Subject-ID", value: "victim"},
		{name: "application principal", header: "X-BKN-Application-Principal-ID", value: "victim-app"},
		{name: "delegation", header: "X-BKN-Delegation-ID", value: "forged-delegation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
				HydraAdminURL: hydra.URL,
			})
			req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
			req.Header.Set("Authorization", "Bearer studio-token")
			req.Header.Set(test.header, test.value)
			rec := httptest.NewRecorder()

			handler.GetEvidenceChainByTraceID(rec, req)

			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_IDENTITY_MISMATCH") {
				t.Fatalf("forged OAuth delegation identity must be rejected, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEvidenceHandlerRejectsInactiveOAuthToken(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"active":false}`)
	}))
	defer hydra.Close()

	handler := NewEvidenceHandlerWithSecurityConfig(evidencesvc.New(evidencestore.New()), EvidenceHandlerSecurityConfig{
		HydraAdminURL: hydra.URL,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	req.Header.Set("Authorization", "Bearer inactive-token")
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "QUERY_OAUTH_REQUIRED") {
		t.Fatalf("inactive OAuth token must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerFiltersEvidenceByOwnership(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("ingest failed: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	req.Header.Set("x-account-id", "acct_other")
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account query must not reveal existence, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAuthorizesTechnicalTraceByEvidenceOwnership(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	allowed := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001", nil)
	if rec := httptest.NewRecorder(); !handler.AuthorizeTechnicalTraceQuery(rec, allowed) {
		t.Fatalf("owner must access technical trace: %d %s", rec.Code, rec.Body.String())
	}
	denied := authenticatedQueryRequest(http.MethodGet, allowed.URL.String(), nil)
	denied.Header.Set("x-account-id", "other-account")
	rec := httptest.NewRecorder()
	if handler.AuthorizeTechnicalTraceQuery(rec, denied) || rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account trace access must not reveal existence: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAuthorizesTechnicalTraceByCoreProjectionOwnership(t *testing.T) {
	const traceID = "9c0d0000000000000000000000000099"
	store := evidencestore.New()
	service := evidencesvc.NewWithProjectionSource(store, staticProjectionSource{
		result: iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{{
			TraceID: traceID, RequestID: "req-core", ConversationID: "conv-core",
			AccountID: "acct_demo", AccountType: "app",
		}}},
	})
	handler := newDevEvidenceHandler(service)

	allowed := authenticatedQueryRequest(
		http.MethodGet,
		"/api/agent-observability/v1/traces/"+traceID,
		nil,
	)
	if rec := httptest.NewRecorder(); !handler.AuthorizeTechnicalTraceQuery(rec, allowed) {
		t.Fatalf("Core projection owner must access technical trace: %d %s", rec.Code, rec.Body.String())
	}

	denied := authenticatedQueryRequest(http.MethodGet, allowed.URL.String(), nil)
	denied.Header.Set("x-account-id", "acct_other")
	rec := httptest.NewRecorder()
	if handler.AuthorizeTechnicalTraceQuery(rec, denied) || rec.Code != http.StatusNotFound {
		t.Fatalf("other account must not discover Core projection trace: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAcceptsValidBatch(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"accepted_event_count":1`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsIngestWithoutConfiguredToken(t *testing.T) {
	store := evidencestore.New()
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(store), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.TraceCount("9c0d0000000000000000000000000001") != 0 {
		t.Fatal("rejected event must stay unstored")
	}
}

func TestEvidenceHandlerAcceptsBearerIngestToken(t *testing.T) {
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(evidencestore.New()), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerAcceptsIngestTokenHeader(t *testing.T) {
	handler := NewEvidenceHandlerWithIngestToken(evidencesvc.New(evidencestore.New()), "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	req.Header.Set("X-BKN-Trace-Ingest-Token", "secret-token")
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsSensitivePayload(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(strings.Replace(validHandlerBatch(), `"claim_hash": "sha256:claim"`, `"raw_sql": "select email from customer"`, 1)))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "BKN_TRACE_FORBIDDEN_RAW_PAYLOAD_FIELD") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsValidationErrorDetails(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	body := strings.Replace(validHandlerBatch(), `"claim_hash": "sha256:claim",`, "", 1)
	body = strings.Replace(body, `"visibility": "visible",`, "", 1)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("expected validation details, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `claim_hash`) || !strings.Contains(rec.Body.String(), `visibility`) {
		t.Fatalf("expected all validation errors in details, got: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsEvidenceChainByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"claims"`) {
		t.Fatalf("expected claims in body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsEvidenceChainByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_handler_001/evidence-chain", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceChainByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_001"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInvalidEvidenceQueryLimit(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_handler_001/evidence-chain?limit=0", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByRequestID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"limit must be an integer between 1 and 1000"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsBusinessGraphByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/business-graph", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"nodes"`) || !strings.Contains(rec.Body.String(), `"edges"`) {
		t.Fatalf("expected graph data in body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"target_id":"business:object:kn_demo:customer"`) {
		t.Fatalf("expected business node edge in body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsBusinessGraphByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_handler_002/business-graph", nil)
	rec := httptest.NewRecorder()
	handler.GetBusinessGraphByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsSnapshotPreviewByTraceWithoutStorageURI(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000002/snapshot-preview", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"mode":"preview"`) || !strings.Contains(body, `"manifest_hash":"sha256:`) {
		t.Fatalf("expected snapshot preview manifest in body: %s", body)
	}
	if strings.Contains(body, `"uri"`) || strings.Contains(body, "s3://") || strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Fatalf("snapshot preview must not expose object storage uri or bare urls: %s", body)
	}
}

func TestEvidenceHandlerReturnsSnapshotPreviewByRequest(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_handler_002/snapshot-preview", nil)
	rec := httptest.NewRecorder()
	handler.GetSnapshotPreviewByRequestID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) || !strings.Contains(rec.Body.String(), `"snapshot_ref"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestBusinessProvenanceRequestSubresourcesUsePathScopedRoutes(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBusinessBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	mux := http.NewServeMux()
	registerLegacySummaryTestRoutes(mux, "/api/agent-observability/v1", handler)
	for _, subresource := range []string{"evidence-chain", "business-graph", "snapshot-preview"} {
		req := authenticatedQueryRequest(http.MethodGet,
			"/api/agent-observability/v1/business-provenance/requests/req_handler_002/"+subresource, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bkn.request.id":"req_handler_002"`) {
			t.Fatalf("request %s route must use the path-scoped request id: %d %s", subresource, rec.Code, rec.Body.String())
		}
	}
}

func TestEvidenceHandlerReportsUnauthorizedRefsWithoutLeakingDetails(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(unauthorizedHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("expected ingest 202, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000003/evidence-chain", nil)
	rec := httptest.NewRecorder()
	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"unauthorized_ref_count":1`) || !strings.Contains(body, `"evidence_ref_unauthorized"`) {
		t.Fatalf("expected unauthorized summary and partial reason: %s", body)
	}
	if strings.Contains(body, "row:unauthorized") || strings.Contains(body, "policy:deny:handler") {
		t.Fatalf("unauthorized ref detail must not leak: %s", body)
	}
}

func TestEvidenceHandlerReturnsEvidenceNodeByTrace(t *testing.T) {
	store := evidencestore.New()
	handler := newDevEvidenceHandler(evidencesvc.New(store))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)

	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/evidence-nodes/claim%3Aclaim_handler?trace_id=9c0d0000000000000000000000000001", nil)
	rec := httptest.NewRecorder()
	handler.GetEvidenceNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"node_id":"claim:claim_handler"`) || !strings.Contains(rec.Body.String(), `"node_type":"claim"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerRejectsEvidenceNodeWithoutScope(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/evidence-nodes/claim%3Aclaim_handler", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceNode(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "trace_id or request_id is required") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsNotFoundForUnknownTraceSubresource(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/unknown", nil)
	rec := httptest.NewRecorder()

	handler.GetTraceSubresource(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsNotFoundForMissingEvidenceChain(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/missing/evidence-chain", nil)
	rec := httptest.NewRecorder()

	handler.GetEvidenceChainByTraceID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerIngestsAndQueriesAuthorizedArtifact(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	ingestRec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(ingestRec, ingestReq)

	if ingestRec.Code != http.StatusCreated || !strings.Contains(ingestRec.Body.String(), `"created":true`) {
		t.Fatalf("expected artifact created, got %d: %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence/artifacts/artifact_handler_001", nil)
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceArtifact(queryRec, queryReq)

	if queryRec.Code != http.StatusOK || !strings.Contains(queryRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected authorized artifact content, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestEvidenceHandlerArtifactQueryDoesNotLeakUnauthorizedPreview(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	queryReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/evidence/artifacts/artifact_handler_001", nil)
	queryReq.Header.Set("x-account-id", "other-account")
	queryRec := httptest.NewRecorder()
	handler.GetEvidenceArtifact(queryRec, queryReq)

	if queryRec.Code != http.StatusNotFound || strings.Contains(queryRec.Body.String(), "用户原始问题") {
		t.Fatalf("unauthorized artifact must look absent without preview leak, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestEvidenceHandlerRejectsSecretBearingArtifact(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	body := strings.Replace(validHandlerArtifact(), `"text":"用户原始问题"`, `"Authorization":"Bearer secret"`, 1)
	req := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ARTIFACT_SECRET_FORBIDDEN") || strings.Contains(rec.Body.String(), "Bearer secret") {
		t.Fatalf("expected non-leaking secret rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerReturnsConflictForArtifactIDReuse(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	firstReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	firstRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(firstRec, firstReq)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", firstRec.Code, firstRec.Body.String())
	}
	conflicting := strings.Replace(validHandlerArtifact(), "用户原始问题", "不同问题", 1)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(conflicting))
	secondRec := httptest.NewRecorder()

	handler.IngestEvidenceArtifact(secondRec, secondReq)

	if secondRec.Code != http.StatusConflict || !strings.Contains(secondRec.Body.String(), "BKN_TRACE_ARTIFACT_ID_CONFLICT") {
		t.Fatalf("expected artifact conflict, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
}

func TestEvidenceHandlerListsBusinessRequestsAndRequestDetail(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	artifactReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	artifactRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(artifactRec, artifactReq)
	if artifactRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", artifactRec.Code, artifactRec.Body.String())
	}
	eventReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerArtifactEventBatch()))
	eventRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(eventRec, eventReq)
	if eventRec.Code != http.StatusAccepted {
		t.Fatalf("seed artifact-linked event: %d %s", eventRec.Code, eventRec.Body.String())
	}

	mux := http.NewServeMux()
	registerLegacySummaryTestRoutes(mux, "/api/agent-observability/v1", handler)

	listReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests?keyword=原始问题&limit=10", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"request_id":"req_artifact_handler"`) ||
		!strings.Contains(listRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected business request list, got %d: %s", listRec.Code, listRec.Body.String())
	}

	detailReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_artifact_handler", nil)
	detailRec := httptest.NewRecorder()
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "用户原始问题") {
		t.Fatalf("expected request detail, got %d: %s", detailRec.Code, detailRec.Body.String())
	}
}

func TestEvidenceHandlerListsBusinessProvenanceConversationsAndInteractions(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	artifactReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/artifacts", strings.NewReader(validHandlerArtifact()))
	artifactRec := httptest.NewRecorder()
	handler.IngestEvidenceArtifact(artifactRec, artifactReq)
	if artifactRec.Code != http.StatusCreated {
		t.Fatalf("seed artifact: %d %s", artifactRec.Code, artifactRec.Body.String())
	}
	eventReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerArtifactEventBatch()))
	eventRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(eventRec, eventReq)
	if eventRec.Code != http.StatusAccepted {
		t.Fatalf("seed event: %d %s", eventRec.Code, eventRec.Body.String())
	}
	mux := http.NewServeMux()
	registerLegacySummaryTestRoutes(mux, "/api/agent-observability/v1", handler)

	conversationReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/conversations?limit=10", nil)
	conversationRec := httptest.NewRecorder()
	mux.ServeHTTP(conversationRec, conversationReq)
	if conversationRec.Code != http.StatusOK || !strings.Contains(conversationRec.Body.String(), `"conversation_id":"conversation_handler"`) ||
		!strings.Contains(conversationRec.Body.String(), `"interaction_count":1`) || !strings.Contains(conversationRec.Body.String(), `"request_count":1`) {
		t.Fatalf("expected conversation projection, got %d: %s", conversationRec.Code, conversationRec.Body.String())
	}

	interactionReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/interactions?conversation_id=conversation_handler&limit=10", nil)
	interactionRec := httptest.NewRecorder()
	mux.ServeHTTP(interactionRec, interactionReq)
	if interactionRec.Code != http.StatusOK || !strings.Contains(interactionRec.Body.String(), `"interaction_id":"interaction_handler"`) ||
		!strings.Contains(interactionRec.Body.String(), `"request_count":1`) {
		t.Fatalf("expected interaction projection, got %d: %s", interactionRec.Code, interactionRec.Body.String())
	}

	detailReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/interactions/interaction_handler", nil)
	detailRec := httptest.NewRecorder()
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), `"interaction_id":"interaction_handler"`) ||
		!strings.Contains(detailRec.Body.String(), `"request_id":"req_artifact_handler"`) {
		t.Fatalf("expected interaction detail, got %d: %s", detailRec.Code, detailRec.Body.String())
	}
}

func TestEvidenceHandlerListsRequestTracesAndTechnicalExecutions(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("seed evidence: %d %s", ingestRec.Code, ingestRec.Body.String())
	}
	mux := http.NewServeMux()
	registerLegacySummaryTestRoutes(mux, "/api/agent-observability/v1", handler)

	requestTracesReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests/req_handler_001/traces", nil)
	requestTracesRec := httptest.NewRecorder()
	mux.ServeHTTP(requestTracesRec, requestTracesReq)
	if requestTracesRec.Code != http.StatusOK || !strings.Contains(requestTracesRec.Body.String(), `"request_id":"req_handler_001"`) ||
		!strings.Contains(requestTracesRec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("expected request traces, got %d: %s", requestTracesRec.Code, requestTracesRec.Body.String())
	}

	executionsReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces?status=running", nil)
	executionsRec := httptest.NewRecorder()
	handler.ListTraceExecutions(executionsRec, executionsReq)
	if executionsRec.Code != http.StatusOK || !strings.Contains(executionsRec.Body.String(), `"trace_id":"9c0d0000000000000000000000000001"`) {
		t.Fatalf("expected technical execution list, got %d: %s", executionsRec.Code, executionsRec.Body.String())
	}
}

func TestEvidenceHandlerTechnicalTraceListUsesTechnicalAccessView(t *testing.T) {
	const traceID = "9c0d0000000000000000000000000099"
	projection := staticProjectionSource{result: iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{{
		TraceID: traceID, RequestID: "req_other_account", AccountID: "other_user", AccountType: "user",
		EffectiveSubjectID: "other_user", ApplicationPrincipalID: "other_app",
		Events: []evidencevo.EvidenceEvent{{
			EventID: "evt_other_account", EventType: "retrieval.completed",
			TraceID: traceID, RequestID: "req_other_account",
			ObservedAt: "2026-08-10T01:51:36Z", EmittedAt: "2026-08-10T01:51:37Z",
		}},
	}}}}
	handler := newDevEvidenceHandler(evidencesvc.NewWithProjectionSource(evidencestore.New(), projection))
	profile := evidencevo.AccessProfile{
		EffectiveSubjectID: "admin_user",
		Roles:              []string{"super_admin"}, AccountActive: true}
	scope := evidencevo.QueryScope{
		AccountID:   "admin_user",
		AccountType: "user", AccessProfile: &profile,
	}
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces?limit=20", nil)
	req = req.WithContext(context.WithValue(req.Context(), trustedQueryScopeContextKey{}, scope))
	rec := httptest.NewRecorder()

	handler.ListTraceExecutions(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"trace_id":"`+traceID+`"`) {
		t.Fatalf("super admin must see cross-account technical traces, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEvidenceHandlerPlatformRoleDoesNotReadBusinessEvidenceAcrossAccounts(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	ingestReq := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", strings.NewReader(validHandlerBatch()))
	ingestRec := httptest.NewRecorder()
	handler.IngestEvidenceEvents(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusAccepted {
		t.Fatalf("seed evidence: %d %s", ingestRec.Code, ingestRec.Body.String())
	}

	userReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?limit=10", nil)
	userReq.Header.Set("x-account-id", "other_user")
	userReq.Header.Set("x-account-type", "user")
	userRec := httptest.NewRecorder()
	handler.ListRequests(userRec, userReq)
	if userRec.Code != http.StatusOK || strings.Contains(userRec.Body.String(), `"request_id":"req_handler_001"`) {
		t.Fatalf("normal cross-account user must not see evidence, got %d: %s", userRec.Code, userRec.Body.String())
	}

	adminReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?limit=10", nil)
	adminReq.Header.Set("x-account-id", "admin_user")
	adminReq.Header.Set("x-account-type", "super_admin")
	adminRec := httptest.NewRecorder()
	handler.ListRequests(adminRec, adminReq)
	if adminRec.Code != http.StatusOK || strings.Contains(adminRec.Body.String(), `"request_id":"req_handler_001"`) {
		t.Fatalf("platform role must not imply cross-account business evidence access, got %d: %s", adminRec.Code, adminRec.Body.String())
	}

	chainReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/traces/9c0d0000000000000000000000000001/evidence-chain", nil)
	chainReq.Header.Set("x-account-id", "admin_user")
	chainReq.Header.Set("x-account-type", "super_admin")
	chainRec := httptest.NewRecorder()
	handler.GetTraceSubresource(chainRec, chainReq)
	if chainRec.Code != http.StatusNotFound || strings.Contains(chainRec.Body.String(), `"claim_id":"claim_handler"`) {
		t.Fatalf("platform role must not read another account's evidence chain, got %d: %s", chainRec.Code, chainRec.Body.String())
	}

	otherDomainReq := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?limit=10", nil)
	otherDomainReq.Header.Set("x-account-id", "admin_user")
	otherDomainReq.Header.Set("x-account-type", "super_admin")
	otherDomainRec := httptest.NewRecorder()
	handler.ListRequests(otherDomainRec, otherDomainReq)
	if otherDomainRec.Code != http.StatusOK || strings.Contains(otherDomainRec.Body.String(), `"request_id":"req_handler_001"`) {
		t.Fatalf("platform role must not disclose business evidence in another domain, got %d: %s", otherDomainRec.Code, otherDomainRec.Body.String())
	}
}

func TestEvidenceHandlerRejectsInvalidSummaryPaginationAndTime(t *testing.T) {
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	for _, target := range []string{
		"/api/agent-observability/v1/requests?limit=201",
		"/api/agent-observability/v1/requests?from=not-a-time",
		"/api/agent-observability/v1/traces?cursor=not-a-cursor",
	} {
		req := authenticatedQueryRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		if strings.Contains(target, "/traces") {
			handler.ListTraceExecutions(rec, req)
		} else {
			handler.ListRequests(rec, req)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid summary query rejection for %s, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestNewAPIErrorResponseIncludesStandardTraceFieldsAndCompatibilityCode(t *testing.T) {
	const traceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/requests?limit=201", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-bbbbbbbbbbbbbbbb-01")
	rec := httptest.NewRecorder()

	handler.ListRequests(rec, req)

	if rec.Code != http.StatusBadRequest || rec.Header().Get("x-trace-id") != traceID {
		t.Fatalf("standard trace response header is required: status=%d headers=%+v", rec.Code, rec.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error_code"] != "INVALID_ARGUMENT" || body["trace_id"] != traceID ||
		body["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("error must expose standard fields and legacy code: %s", rec.Body.String())
	}
}

func TestNewAPISuccessResponseIncludesPropagatedTraceHeader(t *testing.T) {
	const traceID = "cccccccccccccccccccccccccccccccc"
	handler := newDevEvidenceHandler(evidencesvc.New(evidencestore.New()))
	req := authenticatedQueryRequest(http.MethodGet, "/api/agent-observability/v1/business-provenance/requests", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-dddddddddddddddd-01")
	rec := httptest.NewRecorder()

	handler.ListRequests(rec, req)

	if rec.Code != http.StatusOK || rec.Header().Get("x-trace-id") != traceID {
		t.Fatalf("success response must propagate trace id: status=%d headers=%+v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestArtifactIDPathUsesSameSafeFormatAsArtifactIngest(t *testing.T) {
	if got := artifactIDFromPath("/api/agent-observability/v1/evidence/artifacts/artifact_valid-01"); got != "artifact_valid-01" {
		t.Fatalf("valid artifact id must remain queryable: %q", got)
	}
	for _, path := range []string{
		"/api/agent-observability/v1/evidence/artifacts/artifact%2Fchild",
		"/api/agent-observability/v1/evidence/artifacts/artifact%20child",
		"/api/agent-observability/v1/evidence/artifacts/%09artifact",
	} {
		if got := artifactIDFromPath(path); got != "" {
			t.Fatalf("unsafe encoded artifact path must be rejected: path=%q id=%q", path, got)
		}
	}
}

func TestInteractionSummaryPathRejectsNestedOrEmptyIDs(t *testing.T) {
	if got := interactionIDFromSummaryPath("/api/agent-observability/v1/business-provenance/interactions/int_supply_chain"); got != "int_supply_chain" {
		t.Fatalf("unexpected interaction id: %q", got)
	}
	for _, path := range []string{
		"/api/agent-observability/v1/business-provenance/interactions/",
		"/api/agent-observability/v1/business-provenance/interactions/int%2Fchild",
	} {
		if got := interactionIDFromSummaryPath(path); got != "" {
			t.Fatalf("unsafe interaction path must be rejected: path=%q id=%q", path, got)
		}
	}
}

func validHandlerArtifact() string {
	return `{
	  "artifact_id": "artifact_handler_001",
	  "artifact_type": "question",
	  "bkn.request.id": "req_artifact_handler",
	  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	  "interaction_id": "interaction_handler",
	  "operation_id": "operation_handler",
	  "source_ref": "interaction:interaction_handler",
	  "content_type": "application/json",
	  "schema_version": "2.2.0",
	  "observed_at": "2026-07-26T08:00:00Z",
	  "content": {"text":"用户原始问题"},

	  "bkn.account.id": "acct_demo",
	  "bkn.account.type": "app"
	}`
}

func validHandlerArtifactEventBatch() string {
	return `{
	  "bkn.trace.schema.version": "2.2.0",
	  "trace": {
	    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	    "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-2f12000000000003-01",
	    "bkn.request.id": "req_artifact_handler",
	    "bkn.conversation.id": "conversation_handler",

	    "bkn.account.id": "acct_demo",
	    "bkn.account.type": "app"
	  },
	  "events": [{
	    "event_id": "evt_artifact_question",
	    "event_type": "agent.interaction.started",
	    "bkn.trace.schema.version": "2.2.0",
	    "observed_at": "2026-07-26T08:00:00.000000000Z",
	    "emitted_at": "2026-07-26T08:00:00.001000000Z",
	    "producer_module": "bkn-agent",
	    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
	    "span_id": "2f12000000000003",
	    "bkn.request.id": "req_artifact_handler",
	    "bkn.operation.name": "agent.run",
	    "interaction_id": "interaction_handler",
	    "attempt": 1,
	    "payload": {
	      "intent_hash": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	      "mode": "task",
	      "agent_id": "agent-handler",
	      "question_artifact_ref": "artifact:artifact_handler_001"
	    }
	  }]
	}`
}

func validHandlerBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000001",
    "bkn.request.id": "req_handler_001",
    "traceparent": "00-9c0d0000000000000000000000000001-2f12000000000001-01",

    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "9c0d0000000000000000000000000001",
      "span_id": "2f12000000000001",
      "bkn.request.id": "req_handler_001",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    }
  ]
}`
}

func validHandlerBusinessBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000002",
    "bkn.request.id": "req_handler_002",
    "traceparent": "00-9c0d0000000000000000000000000002-2f12000000000002-01",

    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_business",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "9c0d0000000000000000000000000002",
      "span_id": "2f12000000000002",
      "bkn.request.id": "req_handler_002",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler_business",
        "claim_type": "answer",
        "claim_hash": "sha256:claim",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_business",
      "event_type": "business.refs.resolved",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "9c0d0000000000000000000000000002",
      "span_id": "2f12000000000002",
      "bkn.request.id": "req_handler_002",
      "bkn.operation.name": "bkn_trace.resolve_business_refs",
      "payload": {
        "claim_id": "claim_handler_business",
        "business_refs": [
          {
            "ref_id": "object:kn_demo:customer",
            "ref_type": "object",
            "label": "Customer",
            "visibility": "visible",
            "version_status": "versioned"
          }
        ]
      }
    }
  ]
}`
}

func unauthorizedHandlerBatch() string {
	return `{
  "bkn.trace.schema.version": "2.0.0",
  "trace": {
    "trace_id": "9c0d0000000000000000000000000003",
    "bkn.request.id": "req_handler_003",
    "traceparent": "00-9c0d0000000000000000000000000003-2f12000000000003-01",

    "bkn.account.id": "acct_demo",
    "bkn.account.type": "app"
  },
  "events": [
    {
      "event_id": "evt_claim_authz",
      "event_type": "claim.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.000000000Z",
      "emitted_at": "2026-07-22T04:00:00.001000000Z",
      "producer_module": "third-party-agent",
      "trace_id": "9c0d0000000000000000000000000003",
      "span_id": "2f12000000000003",
      "bkn.request.id": "req_handler_003",
      "bkn.operation.name": "agent.answer",
      "payload": {
        "claim_id": "claim_handler_authz",
        "claim_type": "answer",
        "claim_hash": "sha256:claim-authz",
        "visibility": "visible",
        "version_status": "versioned"
      }
    },
    {
      "event_id": "evt_evidence_authz",
      "event_type": "evidence.refs.created",
      "bkn.trace.schema.version": "2.0.0",
      "observed_at": "2026-07-22T04:00:00.002000000Z",
      "emitted_at": "2026-07-22T04:00:00.003000000Z",
      "producer_module": "bkn-trace",
      "trace_id": "9c0d0000000000000000000000000003",
      "span_id": "2f12000000000003",
      "bkn.request.id": "req_handler_003",
      "bkn.operation.name": "bkn_trace.resolve_evidence_refs",
      "payload": {
        "claim_id": "claim_handler_authz",
        "evidence_refs": [
          {
            "ref_id": "row:visible",
            "ref_type": "row_ref",
            "visibility": "visible"
          },
          {
            "ref_id": "row:unauthorized",
            "ref_type": "row_ref",
            "visibility": "unauthorized",
            "policy_decision_ref": "policy:deny:handler",
            "redaction_reason": "row_scope_denied"
          }
        ]
      }
    }
  ]
}`
}
