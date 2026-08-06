package boot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	docs "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/docs/swagger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	mariadbsessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknsafeaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknsafeaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/businessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchlogaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchtraceaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	memorysessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/coremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/server/httpserver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionrebuild"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type App struct {
	server         *httpserver.Server
	internalServer *httpserver.Server
	closeDatabase  func() error
	stopWorkers    context.CancelFunc
	workers        sync.WaitGroup
	projection     *projectorsvc.Worker
}

const APIBasePath = "/api/agent-observability/v1"
const ObservabilityAPIBasePath = "/api/observability/v1"

func NewApp() (*App, error) {
	httpServerConfig := conf.NewHTTPServerConfig()
	openSearchConfig := conf.NewOpenSearchConfig()
	evidenceConfig := conf.NewEvidenceConfig()
	observabilityConfig := conf.NewObservabilityConfig()
	resolverConfig := conf.NewBusinessResolverConfig()
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
	var resolver ibusinessresolver.BusinessResolverPort
	if resolverConfig.Enabled {
		resolver = businessresolver.New(resolverConfig.BKNBaseURL, resolverConfig.VegaBaseURL, &http.Client{Timeout: resolverConfig.Timeout})
	}
	coreConfig := conf.NewCoreConfig()
	metrics := coremetrics.New()
	sessionStore, ledgerStore, closeDatabase, err := newCoreStores(coreConfig)
	if err != nil {
		return nil, err
	}
	var summaryProjection iprojectionsource.ProjectionSourcePort
	if legacyProjection, ok := evidenceStore.(iprojectionsource.ProjectionSourcePort); ok {
		summaryProjection = legacyProjection
		if coreConfig.ProjectionEnabled {
			summaryProjection = opensearchcoreprojection.New(
				openSearchClient, coreConfig.ProjectionIndex, legacyProjection,
			)
		}
	}
	evidenceOptions := []evidencesvc.Option{
		evidencesvc.WithSessionStore(sessionStore),
		evidencesvc.WithTraceStatsSource(traceQueryService),
	}
	if resolver != nil {
		evidenceOptions = append(evidenceOptions, evidencesvc.WithBusinessResolver(resolver))
	}
	if summaryProjection != nil {
		evidenceOptions = append(evidenceOptions, evidencesvc.WithProjectionSource(summaryProjection))
	}
	evidenceService := evidencesvc.New(evidenceStore, evidenceOptions...)
	accessScopeConfig := conf.NewAccessScopeConfig()
	accessScopeResolver := bknsafeaccess.New(
		accessScopeConfig.BKNBaseURL,
		&http.Client{Timeout: accessScopeConfig.Timeout},
	)
	evidenceHandler := httphandler.NewEvidenceHandlerWithAuthorizationScopeResolver(evidenceService, accessScopeResolver)
	logHandler := httphandler.NewLogHandler(logsvc.NewWithOptions([]logsvc.Source{
		opensearchlogaccess.New(openSearchClient, openSearchConfig.LogIndex),
		bknsafeaudit.New(accessScopeConfig.BKNBaseURL, &http.Client{Timeout: accessScopeConfig.Timeout}),
		logsvc.NewNotIntegratedSource("bkn-safe-access", []string{
			observabilityvo.CategoryAccessUser,
		}, []string{"BKN Safe OAuth"}),
		logsvc.NewNotIntegratedSource("bkn-safe-security", []string{
			observabilityvo.CategoryAuditSecurity,
		}, []string{"BKN Safe Authorization"}),
	}, logsvc.Options{
		CursorKey:            observabilityConfig.CursorSigningKey,
		SourceTimeout:        observabilityConfig.SourceTimeout,
		MaxConcurrentSources: observabilityConfig.MaxConcurrentSources,
	}), evidenceHandler)
	sessionService := sessionsvc.New(sessionStore, sessionsvc.Options{
		EvidenceCollectionState: func() string {
			if coreConfig.EvidenceCollectionState == "" {
				return "enabled"
			}
			return coreConfig.EvidenceCollectionState
		},
		Metrics: metrics,
	})
	sessionHandler := httphandler.NewSessionHandlerWithAssembly(
		sessionService,
		assemblysvc.NewQueryServiceWithBusinessResolver(sessionStore, ledgerStore, resolver),
	)
	ledgerHandler := httphandler.NewConfiguredLedgerHandler(ledgersvc.NewWithMetrics(ledgerStore, metrics))

	app := newApp(httpServerConfig, traceHandler, evidenceHandler, logHandler, sessionHandler, ledgerHandler, metrics)
	app.closeDatabase = closeDatabase
	workerContext, stopWorkers := context.WithCancel(context.Background())
	app.stopWorkers = stopWorkers
	app.workers.Add(1)
	go func() {
		defer app.workers.Done()
		runLeaseReaper(
			workerContext,
			coreConfig.AbandonInterval,
			coreConfig.OneShotIdleTTL,
			sessionService,
		)
	}()
	if coreConfig.ProjectionEnabled {
		outboxStore, supported := sessionStore.(iprojectionoutbox.Store)
		if !supported {
			stopWorkers()
			app.workers.Wait()
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, errors.New("configured Core store does not support projection outbox")
		}
		sink := opensearchprojection.New(openSearchClient, coreConfig.ProjectionIndex)
		var rebuildProjection func(context.Context) error
		if coreConfig.ProjectionRebuildVersion != "" {
			source, rebuildSupported := sessionStore.(iprojectionrebuild.Source)
			if !rebuildSupported {
				stopWorkers()
				app.workers.Wait()
				if closeDatabase != nil {
					_ = closeDatabase()
				}
				return nil, errors.New("configured Core store does not support projection rebuild")
			}
			rebuild := projectionrebuildsvc.New(source, sink, projectionrebuildsvc.Options{})
			rebuildProjection = func(ctx context.Context) error {
				_, err := rebuild.Rebuild(
					ctx, "core", coreConfig.ProjectionIndex,
					coreConfig.ProjectionRebuildVersion,
				)
				return err
			}
		}
		worker := projectorsvc.NewWorker(
			outboxStore, sink, projectorsvc.WorkerOptions{Metrics: metrics},
		)
		app.projection = worker
		app.workers.Add(1)
		go func() {
			defer app.workers.Done()
			runProjectionSupervisor(
				workerContext,
				coreConfig.ProjectionInterval,
				rebuildProjection,
				func(ctx context.Context) {
					runProjectionWorker(
						ctx, coreConfig.ProjectionInterval, worker, metrics,
					)
				},
				metrics,
			)
		}()
	}
	return app, nil
}

func newCoreStores(config conf.CoreConfig) (isessionstore.Store, ievidenceledger.Store, func() error, error) {
	if !strings.EqualFold(config.Store, "mariadb") {
		return memorysessionstore.New(), ledgerstore.New(), nil, nil
	}
	if config.MariaDBDSN == "" {
		return nil, nil, nil, errors.New("BKN_TRACE_CORE_MARIADB_DSN is required when BKN_TRACE_CORE_STORE=mariadb")
	}
	db, err := sql.Open("mysql", config.MariaDBDSN)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open BKN Trace MariaDB: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("connect BKN Trace MariaDB: %w", err)
	}
	store := mariadbsessionstore.New(db)
	if config.AutoMigrate {
		if err := store.Migrate(context.Background()); err != nil {
			_ = db.Close()
			return nil, nil, nil, err
		}
	}
	return store, store, db.Close, nil
}

func runLeaseReaper(
	ctx context.Context,
	interval time.Duration,
	oneShotIdleTTL time.Duration,
	service *sessionsvc.Service,
) {
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = service.AbandonExpiredInteractions(ctx, 100)
			_, _ = service.ExpireIdleOneShotConversations(ctx, oneShotIdleTTL, 100)
			_, _ = service.AssembleDueInteractions(ctx, 100)
		}
	}
}

func runProjectionWorker(
	ctx context.Context,
	interval time.Duration,
	worker *projectorsvc.Worker,
	metrics icoremetrics.Recorder,
) {
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := worker.RunOnce(ctx); err != nil {
				metrics.Increment(icoremetrics.ProjectionErrorsTotal)
				log.Printf("BKN Trace projection worker failed: %v", err)
			}
		}
	}
}

func runProjectionSupervisor(
	ctx context.Context,
	retryInterval time.Duration,
	rebuild func(context.Context) error,
	runWorker func(context.Context),
	metrics icoremetrics.Recorder,
) {
	if metrics == nil {
		metrics = icoremetrics.Noop{}
	}
	metrics.Set(icoremetrics.ProjectionReady, 0)
	if retryInterval <= 0 {
		retryInterval = time.Second
	}
	for rebuild != nil {
		if err := rebuild(ctx); err == nil {
			break
		} else {
			metrics.Increment(icoremetrics.ProjectionErrorsTotal)
			log.Printf("BKN Trace projection rebuild failed; retrying: %v", err)
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
	metrics.Set(icoremetrics.ProjectionReady, 1)
	runWorker(ctx)
}

func newApp(
	httpServerConfig conf.HTTPServerConfig,
	traceHandler *httphandler.TraceHandler,
	evidenceHandler *httphandler.EvidenceHandler,
	logHandler *httphandler.LogHandler,
	sessionHandler *httphandler.SessionHandler,
	ledgerHandler *httphandler.LedgerHandler,
	metrics http.Handler,
) *App {
	mux := http.NewServeMux()
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}

	// Resolve the OAuth or trusted-gateway identity once at the read boundary,
	// then share the immutable access scope with trace, evidence and log handlers.
	// The evidence WRITE route keeps its independent ingest-token guard.
	readAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return evidenceHandler.RequireTrustedQueryIdentity(h)
	}

	mux.HandleFunc(APIBasePath+"/traces/_search", readAuth(traceHandler.SearchTraces))
	mux.HandleFunc(APIBasePath+"/traces/by-conversation", readAuth(traceHandler.SearchTracesByConversationID))
	mux.HandleFunc(APIBasePath+"/traces/by-request/business-graph", readAuth(evidenceHandler.GetBusinessGraphByRequestID))
	mux.HandleFunc(APIBasePath+"/traces/by-request/snapshot-preview", readAuth(evidenceHandler.GetSnapshotPreviewByRequestID))
	mux.HandleFunc(APIBasePath+"/traces/by-request", readAuth(evidenceHandler.GetEvidenceChainByRequestID))
	mux.HandleFunc(APIBasePath+"/traces/", readAuth(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/trace-graph") && !evidenceHandler.AuthorizeTechnicalTraceQuery(w, r) {
			return
		}
		if traceHandler.GetTraceSubresource(w, r) {
			return
		}
		evidenceHandler.GetTraceSubresource(w, r)
	}))
	mux.HandleFunc(APIBasePath+"/evidence-nodes/", readAuth(evidenceHandler.GetEvidenceNode))
	mux.HandleFunc(APIBasePath+"/evidence/artifacts/", readAuth(evidenceHandler.GetEvidenceArtifact))
	mux.HandleFunc(APIBasePath+"/evidence/by-trace", readAuth(evidenceHandler.SearchEvidenceByTrace))
	httphandler.RegisterBusinessProvenanceRoutes(mux, APIBasePath, evidenceHandler, readAuth)
	mux.HandleFunc(APIBasePath+"/trace-executions", readAuth(evidenceHandler.ListTraceExecutions))
	mux.HandleFunc(APIBasePath+"/access-profile", readAuth(evidenceHandler.GetAccessProfile))
	mux.HandleFunc(ObservabilityAPIBasePath+"/logs", readAuth(logHandler.ListLogs))
	mux.HandleFunc(ObservabilityAPIBasePath+"/logs/", readAuth(logHandler.GetLog))
	mux.HandleFunc(ObservabilityAPIBasePath+"/log-facets", readAuth(logHandler.GetLogFacets))
	mux.HandleFunc(ObservabilityAPIBasePath+"/log-sources", readAuth(logHandler.ListLogSources))
	mux.HandleFunc(ObservabilityAPIBasePath+"/log-policies", readAuth(logHandler.ListLogPolicies))
	mux.Handle(APIBasePath+"/swagger/", httpSwagger.Handler(
		httpSwagger.URL(APIBasePath+"/swagger/doc.json"),
	))

	internalMux := http.NewServeMux()
	internal := evidenceHandler.InternalLifecycle
	lifecycle := func(next http.HandlerFunc) http.HandlerFunc {
		return internal(evidenceHandler.RequireTrustedLifecycleIdentity(next))
	}
	internalMux.HandleFunc(APIBasePath+"/evidence/events", lifecycle(ledgerHandler.Ingest))
	internalMux.HandleFunc(APIBasePath+"/evidence/artifacts", internal(evidenceHandler.IngestEvidenceArtifact))
	httphandler.RegisterSessionRoutes(internalMux, APIBasePath, sessionHandler, lifecycle)

	return &App{
		server:         httpserver.New(httpServerConfig.Address, mux),
		internalServer: httpserver.New(httpServerConfig.InternalAddress, internalMux),
	}
}

func (a *App) Start() error {
	if a.internalServer == nil {
		return a.server.Start()
	}
	internalResult, err := a.internalServer.StartAsync()
	if err != nil {
		return fmt.Errorf("start BKN Trace internal listener: %w", err)
	}
	publicResult, err := a.server.StartAsync()
	if err != nil {
		_ = a.internalServer.Shutdown(context.Background())
		return fmt.Errorf("start BKN Trace public listener: %w", err)
	}
	select {
	case err := <-internalResult:
		if err != nil {
			return fmt.Errorf("BKN Trace internal listener stopped: %w", err)
		}
		return nil
	case err := <-publicResult:
		return err
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	var shutdownErr error
	if a.server != nil {
		shutdownErr = a.server.Shutdown(ctx)
	}
	if a.internalServer != nil {
		shutdownErr = errors.Join(shutdownErr, a.internalServer.Shutdown(ctx))
	}
	shutdownErr = errors.Join(
		shutdownErr,
		stopAndDrainProjectionWorker(ctx, a.stopWorkers, &a.workers, a.projection),
	)
	if a.closeDatabase != nil {
		shutdownErr = errors.Join(shutdownErr, a.closeDatabase())
	}
	return shutdownErr
}

func stopAndDrainProjectionWorker(
	ctx context.Context,
	stopWorkers context.CancelFunc,
	workers *sync.WaitGroup,
	projection *projectorsvc.Worker,
) error {
	if stopWorkers != nil {
		stopWorkers()
	}
	if workers != nil {
		workers.Wait()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if projection == nil {
		return nil
	}
	_, err := projection.Drain(ctx)
	return err
}
