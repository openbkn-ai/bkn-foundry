package boot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
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
