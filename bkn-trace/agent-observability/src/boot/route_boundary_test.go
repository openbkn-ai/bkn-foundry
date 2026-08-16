package boot

import (
	"context"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
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
		{name: "public lifecycle is absent", server: app.server, path: APIBasePath + "/conversations", wantStatus: http.StatusNotFound},
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
