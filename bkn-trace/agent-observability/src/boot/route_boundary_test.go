// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
)

func TestWriteRoutesKeepLifecycleAndEvidenceOnSeparateListeners(t *testing.T) {
	evidenceHandler := httphandler.NewEvidenceHandlerWithIngestToken(
		evidencesvc.New(evidencestore.New()), "ingest-token",
	)
	app := newApp(
		conf.HTTPServerConfig{}, nil, evidenceHandler, nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{
			IngestToken: "ingest-token",
		}),
		nil,
	)

	tests := []struct {
		name       string
		server     http.Handler
		path       string
		wantStatus int
	}{
		{name: "public lifecycle requires OAuth identity", server: app.server, path: APIBasePath + "/conversations", wantStatus: http.StatusUnauthorized},
		{name: "internal evidence events are absent", server: app.internalServer, path: APIBasePath + "/evidence/events", wantStatus: http.StatusNotFound},
		{name: "internal evidence artifacts are absent", server: app.internalServer, path: APIBasePath + "/evidence/artifacts", wantStatus: http.StatusNotFound},
		{name: "internal lifecycle requires owner", server: app.internalServer, path: APIBasePath + "/conversations", wantStatus: http.StatusUnauthorized},
		{name: "public evidence requires token", server: app.server, path: APIBasePath + "/evidence/events", wantStatus: http.StatusUnauthorized},
		{name: "public artifact requires token", server: app.server, path: APIBasePath + "/evidence/artifacts", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{}`))
			response := httptest.NewRecorder()
			test.server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("POST %s = %d, want %d: %s", test.path, response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestPublicManagedLifecycleFlowUsesServerDerivedOwner(t *testing.T) {
	sessionStore := sessionstore.New()
	evidenceHandler := httphandler.NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		httphandler.EvidenceHandlerSecurityConfig{
			HydraAdminURL:                  "http://hydra.test",
			DeploymentTenantID:             "openbkn-local",
			PublicLifecycleBusinessDomains: "customer-service,inventory",
			QueryHTTPClient: &http.Client{Transport: routeBoundaryRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/admin/oauth2/introspect" {
					t.Fatalf("unexpected OAuth introspection path: %s", request.URL.Path)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"active":true,"sub":"user-1","client_id":"openbkn-cli","ext":{"visitor_type":"user"}}`,
					)),
				}, nil
			})},
			AuthorizationScopeResolver: routeBoundaryScopeResolver{},
		},
	)
	app := newApp(
		conf.HTTPServerConfig{}, nil, evidenceHandler, nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionStore, sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{}),
		nil,
	)

	var conversation sessionvo.Conversation
	publicLifecycleRequest(t, app.server, http.MethodPost, APIBasePath+"/conversations:ensure-current", map[string]any{
		"external_conversation_key": "customer-order-a123",
		"idempotency_key":           "ensure-order-a123",
	}, http.StatusCreated, &conversation)
	assertPublicLifecycleOwner(t, conversation.Owner)

	var interaction sessionvo.Interaction
	publicLifecycleRequest(t, app.server, http.MethodPost, APIBasePath+"/conversations/"+conversation.ID+"/interactions", map[string]any{
		"idempotency_key": "interaction-order-a123",
		"agent_name":      "customer-service-agent",
		"lease_seconds":   60,
	}, http.StatusCreated, &interaction)

	var ensured struct {
		Operation sessionvo.Operation `json:"operation"`
		Receipt   sessionvo.Receipt   `json:"receipt"`
	}
	publicLifecycleRequest(t, app.server, http.MethodPost,
		APIBasePath+"/conversations/"+conversation.ID+"/interactions/"+interaction.ID+"/operations:ensure",
		map[string]any{
			"operation_key": "query-order-a123", "tool_name": "orders.get", "protocol": "sdk",
			"source_module": "managed-trace-sdk", "required": true,
			"lease_token": interaction.LeaseToken, "lease_epoch": interaction.LeaseEpoch,
			"input": map[string]any{
				"mode": "inline", "media_type": "application/json", "byte_length": 2,
				"inline": map[string]any{},
			},
		}, http.StatusCreated, &ensured)
	assertPublicLifecycleOwner(t, ensured.Receipt.Owner)

	var completed struct {
		Receipt sessionvo.Receipt `json:"receipt"`
	}
	publicLifecycleRequest(t, app.server, http.MethodPost,
		APIBasePath+"/operations/"+ensured.Operation.ID+"/attempts/1:complete",
		map[string]any{
			"receipt_id": ensured.Receipt.ID, "evidence_durability": "durable",
			"request_id": "request-order-a123", "trace_id": "0123456789abcdef0123456789abcdef",
			"output": map[string]any{
				"mode": "inline", "media_type": "application/json", "byte_length": 2,
				"inline": map[string]any{},
			},
		}, http.StatusOK, &completed)

	var readReceipt sessionvo.Receipt
	publicLifecycleRequest(t, app.server, http.MethodGet,
		APIBasePath+"/receipts/"+ensured.Receipt.ID, nil, http.StatusOK, &readReceipt)
	assertPublicLifecycleOwner(t, readReceipt.Owner)
	if readReceipt.Status != sessionvo.ReceiptCompleted || readReceipt.ID != completed.Receipt.ID {
		t.Fatalf("unexpected durable receipt: %+v", readReceipt)
	}

	var completedInteraction sessionvo.Interaction
	publicLifecycleRequest(t, app.server, http.MethodPost,
		APIBasePath+"/interactions/"+interaction.ID+"/complete",
		map[string]any{
			"terminal_idempotency_key": "complete-order-a123",
			"lease_token":              interaction.LeaseToken, "lease_epoch": interaction.LeaseEpoch,
			"completion_manifest_version": "3.0.0", "completion_reason": "answer_completed",
			"expected_operations": []map[string]any{{"operation_id": ensured.Operation.ID, "required": true}},
			"expected_receipts":   []map[string]any{{"receipt_id": ensured.Receipt.ID, "required": true}},
		}, http.StatusOK, &completedInteraction)
	if completedInteraction.ExecutionStatus != sessionvo.InteractionCompleted {
		t.Fatalf("public interaction was not completed: %+v", completedInteraction)
	}
}

type routeBoundaryRoundTrip func(*http.Request) (*http.Response, error)

func (f routeBoundaryRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type routeBoundaryScopeResolver struct{}

func (routeBoundaryScopeResolver) Resolve(
	_ context.Context,
	_ string,
	identity iauthorizationscope.TrustedIdentity,
) (evidencevo.AccessProfile, error) {
	return evidencevo.AccessProfile{
		TenantID: identity.TenantID, BusinessDomain: identity.BusinessDomain,
		ActorID: identity.ActorID, EffectiveSubjectID: identity.EffectiveSubjectID,
		ApplicationPrincipalID: identity.ApplicationPrincipalID, DelegationID: identity.DelegationID,
		AccountActive: true, TenantActive: true,
	}, nil
}

func publicLifecycleRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	wantStatus int,
	target any,
) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode public lifecycle request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer lifecycle-token")
	request.Header.Set("x-business-domain", "customer-service")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != wantStatus {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	if target != nil {
		if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
			t.Fatalf("decode %s %s response: %v: %s", method, path, err, response.Body.String())
		}
	}
}

func assertPublicLifecycleOwner(t *testing.T, owner sessionvo.Owner) {
	t.Helper()
	if owner.TenantID != "openbkn-local" || owner.BusinessDomainID != "customer-service" ||
		owner.ApplicationPrincipalID != "openbkn-cli" ||
		owner.EffectiveSubjectType != sessionvo.SubjectUser || owner.EffectiveSubjectID != "user-1" {
		t.Fatalf("lifecycle owner was not derived from OAuth: %+v", owner)
	}
}

func TestPublicTypedTraceListRouteIsRegistered(t *testing.T) {
	store := evidencestore.New()
	evidenceHandler := httphandler.NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(store, evidencesvc.WithProjectionSource(store)),
		httphandler.EvidenceHandlerSecurityConfig{
			IngestToken: "ingest-token", AllowUnauthenticatedQuery: true,
		},
	)
	app := newApp(
		conf.HTTPServerConfig{}, nil, evidenceHandler, nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{
			IngestToken: "ingest-token",
		}),
		nil,
	)
	for _, path := range []string{APIBasePath + "/traces", APIBasePath + "/traces/"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("x-account-id", "user-1")
		request.Header.Set("x-account-type", "user")
		request.Header.Set("x-tenant-id", "tenant-1")
		request.Header.Set("x-business-domain", "domain-1")
		response := httptest.NewRecorder()

		app.server.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("typed GET %s = %d, want 200: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestPublicRawTraceQueryRoutesAreUnavailable(t *testing.T) {
	store := evidencestore.New()
	evidenceHandler := httphandler.NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(store, evidencesvc.WithProjectionSource(store)),
		httphandler.EvidenceHandlerSecurityConfig{
			IngestToken: "ingest-token", AllowUnauthenticatedQuery: true,
		},
	)
	app := newApp(
		conf.HTTPServerConfig{}, httphandler.NewTraceHandler(nil), evidenceHandler, nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{
			IngestToken: "ingest-token",
		}),
		nil,
	)

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: APIBasePath + "/traces/_search"},
		{method: http.MethodGet, path: APIBasePath + "/traces/by-conversation?conversation_id=conv-1"},
		{method: http.MethodGet, path: APIBasePath + "/traces/by-request?request_id=req-1"},
		{method: http.MethodGet, path: APIBasePath + "/traces/by-request/business-graph?request_id=req-1"},
		{method: http.MethodGet, path: APIBasePath + "/traces/by-request/snapshot-preview?request_id=req-1"},
		{method: http.MethodGet, path: APIBasePath + "/traces/trace-1/trace-graph"},
		{method: http.MethodGet, path: APIBasePath + "/evidence/by-trace?trace_id=trace-1"},
		{method: http.MethodGet, path: APIBasePath + "/trace-executions"},
		{method: http.MethodGet, path: APIBasePath + "/evidence-nodes/claim%3Alegacy?trace_id=trace-1"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		response := httptest.NewRecorder()

		app.server.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404: %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestLegacyLogFacetRouteIsUnavailable(t *testing.T) {
	evidenceHandler := httphandler.NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(evidencestore.New()),
		httphandler.EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true},
	)
	app := newApp(
		conf.HTTPServerConfig{}, nil, evidenceHandler,
		httphandler.NewLogHandler(logsvc.New(nil), evidenceHandler),
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{}),
		nil,
	)
	request := httptest.NewRequest(http.MethodGet, ObservabilityAPIBasePath+"/log-facets?facet=event_name", nil)
	request.Header.Set("x-account-id", "admin-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-1")
	request.Header.Set("x-business-domain", "domain-1")
	response := httptest.NewRecorder()

	app.server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy log facet route = %d, want 404: %s", response.Code, response.Body.String())
	}
}

func TestCommunityTraceDetailDoesNotDispatchEnterpriseSubresources(t *testing.T) {
	store := evidencestore.New()
	evidenceHandler := httphandler.NewEvidenceHandlerWithSecurityConfig(
		evidencesvc.New(store, evidencesvc.WithProjectionSource(store)),
		httphandler.EvidenceHandlerSecurityConfig{
			IngestToken: "ingest-token", AllowUnauthenticatedQuery: true,
		},
	)
	app := newApp(
		conf.HTTPServerConfig{}, httphandler.NewTraceHandler(nil), evidenceHandler, nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{
			IngestToken: "ingest-token",
		}),
		nil,
	)
	request := httptest.NewRequest(http.MethodGet, APIBasePath+"/traces/trace-1/business-graph", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-1")
	request.Header.Set("x-business-domain", "domain-1")
	response := httptest.NewRecorder()

	app.server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || response.Body.String() != "404 page not found\n" {
		t.Fatalf("community Trace route dispatched an enterprise subresource: %d %s", response.Code, response.Body.String())
	}
}

func TestPublicRouteMountsEnterpriseExtensionOnlyWhenAssembled(t *testing.T) {
	enterpriseroute.ResetForTest()
	t.Cleanup(enterpriseroute.ResetForTest)
	enterpriseroute.Register(func(routes enterpriseroute.Registrar, reader enterpriseroute.Reader) {
		if reader == nil {
			t.Fatal("assembled enterprise route must receive the Core fact reader")
		}
		routes.Handle("/enterprise-probe", func(next http.Handler) http.Handler { return next }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	})
	app := newApp(
		conf.HTTPServerConfig{}, nil,
		httphandler.NewEvidenceHandlerWithSecurityConfig(
			evidencesvc.New(evidencestore.New()),
			httphandler.EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true},
		),
		nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{}),
		nil, routeBoundaryReader{},
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/enterprise-probe", nil)
	request.Header.Set("x-account-id", "user-1")
	request.Header.Set("x-account-type", "user")
	request.Header.Set("x-tenant-id", "tenant-1")
	request.Header.Set("x-business-domain", "domain-1")
	app.server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("assembled enterprise route = %d, want 204", response.Code)
	}
}

type routeBoundaryReader struct{}

func (routeBoundaryReader) ReadInteraction(context.Context, string) (enterpriseroute.InteractionFacts, bool, error) {
	return enterpriseroute.InteractionFacts{}, false, nil
}

func (routeBoundaryReader) ListConversations(context.Context, enterpriseroute.ListQuery) (evidencevo.ConversationSummaryPage, error) {
	return evidencevo.ConversationSummaryPage{}, nil
}

func (routeBoundaryReader) ListInteractions(context.Context, enterpriseroute.ListQuery) (evidencevo.InteractionSummaryPage, error) {
	return evidencevo.InteractionSummaryPage{}, nil
}

func TestCommunityDoesNotMountLegacyBusinessProvenanceDataRoutes(t *testing.T) {
	app := newApp(
		conf.HTTPServerConfig{}, nil,
		httphandler.NewEvidenceHandlerWithSecurityConfig(
			evidencesvc.New(evidencestore.New()),
			httphandler.EvidenceHandlerSecurityConfig{AllowUnauthenticatedQuery: true},
		),
		nil,
		httphandler.NewSessionHandler(sessionsvc.New(sessionstore.New(), sessionsvc.Options{})),
		httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{}),
		nil,
	)

	response := httptest.NewRecorder()
	app.server.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet, APIBasePath+"/business-provenance/conversations", nil,
	))
	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy business provenance data route = %d, want 404", response.Code)
	}
}
