package boot

import (
	"context"
	"net/http"
	"strings"

	docs "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/docs/swagger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchtraceaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/server/httpserver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type App struct {
	server *httpserver.Server
}

const APIBasePath = "/api/agent-observability/v1"

func NewApp() *App {
	httpServerConfig := conf.NewHTTPServerConfig()
	openSearchConfig := conf.NewOpenSearchConfig()
	evidenceConfig := conf.NewEvidenceConfig()
	docs.SwaggerInfo.BasePath = APIBasePath

	openSearchClient := opensearch.New(
		openSearchConfig.Endpoint,
		opensearch.AuthConfig{
			Enabled:  openSearchConfig.Auth.Enabled,
			Username: openSearchConfig.Auth.Username,
			Password: openSearchConfig.Auth.Password,
		},
		openSearchConfig.Timeout,
	)
	traceDetailClient := opensearchtraceaccess.New(openSearchClient, openSearchConfig.TraceIndex)
	traceQueryService := tracesvc.New(traceDetailClient)
	traceHandler := httphandler.NewTraceHandler(traceQueryService)
	var evidenceStore ievidencestore.EvidenceStorePort = evidencestore.New()
	if strings.EqualFold(evidenceConfig.Store, "opensearch") {
		evidenceStore = opensearchevidencestore.New(openSearchClient, openSearchConfig.EvidenceIndex)
	}
	evidenceService := evidencesvc.New(evidenceStore)
	evidenceHandler := httphandler.NewEvidenceHandler(evidenceService)

	return newApp(httpServerConfig, traceHandler, evidenceHandler)
}

func newApp(httpServerConfig conf.HTTPServerConfig, traceHandler *httphandler.TraceHandler, evidenceHandler *httphandler.EvidenceHandler) *App {
	mux := http.NewServeMux()
	mux.HandleFunc(APIBasePath+"/traces/_search", traceHandler.SearchTraces)
	mux.HandleFunc(APIBasePath+"/traces/by-conversation", traceHandler.SearchTracesByConversationID)
	mux.HandleFunc(APIBasePath+"/traces/by-request/business-graph", evidenceHandler.GetBusinessGraphByRequestID)
	mux.HandleFunc(APIBasePath+"/traces/by-request/snapshot-preview", evidenceHandler.GetSnapshotPreviewByRequestID)
	mux.HandleFunc(APIBasePath+"/traces/by-request", evidenceHandler.GetEvidenceChainByRequestID)
	mux.HandleFunc(APIBasePath+"/traces/", evidenceHandler.GetTraceSubresource)
	mux.HandleFunc(APIBasePath+"/evidence-nodes/", evidenceHandler.GetEvidenceNode)
	mux.HandleFunc(APIBasePath+"/evidence/events", evidenceHandler.IngestEvidenceEvents)
	mux.Handle(APIBasePath+"/swagger/", httpSwagger.Handler(
		httpSwagger.URL(APIBasePath+"/swagger/doc.json"),
	))

	return &App{
		server: httpserver.New(httpServerConfig.Address, mux),
	}
}

func (a *App) Start() error {
	return a.server.Start()
}

func (a *App) Shutdown(ctx context.Context) error {
	if a.server != nil {
		return a.server.Shutdown(ctx)
	}

	return nil
}
