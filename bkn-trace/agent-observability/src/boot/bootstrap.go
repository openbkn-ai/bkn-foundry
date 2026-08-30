// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

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
	observabilitylocale "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/locale"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sourcecoveragesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/tracesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/archivestore"
	mariadbsessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknbackendaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknsafeaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknsafeaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/bknsafeuseraccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/businessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/executionfactoryaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/modelmanageraudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchconversationaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchlogaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchruntimeaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchtraceaccess"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/ossgatewayarchive"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/otelcolmetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/vegaaudit"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	memorysessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isourcecoveragestore"
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

func localizedHTTPClient(timeout time.Duration) *http.Client {
	return observabilitylocale.WrapHTTPClient(&http.Client{Timeout: timeout})
}

func NewApp() (*App, error) {
	observabilitylocale.Register()
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
	if err := openSearchClient.EnsureTraceTimestampPipeline(
		context.Background(), openSearchConfig.TraceTimestampPipeline,
		openSearchConfig.TraceIndex,
	); err != nil {
		return nil, fmt.Errorf("initialize trace timestamp pipeline: %w", err)
	}
	traceDetailClient := opensearchtraceaccess.New(openSearchClient, openSearchConfig.TraceIndex)
	traceQueryService := tracesvc.New(traceDetailClient)
	var evidenceStore ievidencestore.EvidenceStorePort = evidencestore.New()
	if strings.EqualFold(evidenceConfig.Store, "opensearch") {
		evidenceStore = opensearchevidencestore.New(openSearchClient, openSearchConfig.EvidenceIndex)
	}
	var resolver ibusinessresolver.BusinessResolverPort
	if resolverConfig.Enabled {
		resolver = businessresolver.New(resolverConfig.BKNBaseURL, resolverConfig.VegaBaseURL, localizedHTTPClient(resolverConfig.Timeout))
	}
	coreConfig, err := conf.NewCoreConfig()
	if err != nil {
		return nil, err
	}
	metrics := coremetrics.New()
	sessionStore, ledgerStore, closeDatabase, err := newCoreStores(coreConfig)
	if err != nil {
		return nil, err
	}
	coverageStore, coverageStoreSupported := sessionStore.(isourcecoveragestore.Store)
	coverageMonitorEnabled := observabilityConfig.SourceCoverageMetricsEndpoint != ""
	if coverageMonitorEnabled && (!coverageStoreSupported || observabilityConfig.SourceCoverageSourceID == "" || observabilityConfig.SourceCoverageDeploymentID == "") {
		if closeDatabase != nil {
			_ = closeDatabase()
		}
		return nil, errors.New("configured source coverage monitor requires MariaDB Core store, source ID, and deployment ID")
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
		localizedHTTPClient(accessScopeConfig.Timeout),
	)
	evidenceHandler := httphandler.NewEvidenceHandlerWithAuthorizationScopeResolver(evidenceService, accessScopeResolver)
	logOptions := logsvc.Options{
		CursorKey: observabilityConfig.CursorSigningKey, SourceTimeout: observabilityConfig.SourceTimeout,
		MaxConcurrentSources: observabilityConfig.MaxConcurrentSources,
		OperationAuditOnly:   true,
	}
	if coverageStoreSupported && observabilityConfig.SourceCoverageDeploymentID != "" {
		logOptions.CoverageStore = coverageStore
		logOptions.CoverageDeploymentID = observabilityConfig.SourceCoverageDeploymentID
	}
	logSources := []logsvc.Source{
		opensearchlogaccess.New(openSearchClient, openSearchConfig.LogIndex),
		bknsafeaudit.New(accessScopeConfig.BKNBaseURL, localizedHTTPClient(accessScopeConfig.Timeout)),
		bknsafeuseraccess.New(accessScopeConfig.BKNBaseURL, localizedHTTPClient(accessScopeConfig.Timeout)),
		logsvc.NewNotIntegratedSource("bkn-safe-security", []string{
			observabilityvo.CategoryAuditSecurity,
		}, []string{"BKN Safe Authorization"}),
		bknbackendaudit.New(resolverConfig.BKNBaseURL, localizedHTTPClient(resolverConfig.Timeout)),
		vegaaudit.New(resolverConfig.VegaBaseURL, localizedHTTPClient(resolverConfig.Timeout)),
		executionfactoryaudit.New(resolverConfig.ExecutionFactoryURL, localizedHTTPClient(resolverConfig.Timeout)),
		modelmanageraudit.New(resolverConfig.ModelManagerURL, localizedHTTPClient(resolverConfig.Timeout)),
	}
	if coreConfig.ProjectionEnabled {
		logSources = append(logSources, opensearchconversationaudit.New(openSearchClient, coreConfig.ProjectionIndex))
		logSources = append(logSources, opensearchruntimeaudit.New(openSearchClient, coreConfig.ProjectionIndex))
	}
	logHandler := httphandler.NewLogHandler(logsvc.NewWithOptions(logSources, logOptions), evidenceHandler)
	provenanceHandler := enterpriseroute.HistoricalProvenanceHandler()
	sessionService := sessionsvc.New(sessionStore, sessionsvc.Options{
		EnableHistoricalProvenance: provenanceHandler != nil && coreConfig.ProjectionEnabled,
		Capacity: sessionsvc.CapacityLimits{
			MaxOperationsPerInteraction:   coreConfig.MaxOperationsPerInteraction,
			MaxClaimsPerInteraction:       coreConfig.MaxClaimsPerInteraction,
			MaxEvidenceRefsPerInteraction: coreConfig.MaxEvidenceRefsPerInteraction,
		},
		EvidenceCollectionState: func() string {
			if coreConfig.EvidenceCollectionState == "" {
				return "enabled"
			}
			return coreConfig.EvidenceCollectionState
		},
		Metrics: metrics,
	})
	archiveStore := archivesvc.NewMemoryStore()
	if databaseStore, ok := sessionStore.(interface{ Database() *sql.DB }); ok {
		archiveStore = archivestore.New(databaseStore.Database())
	}
	archiveObjectStore := ossgatewayarchive.New(ossgatewayarchive.Config{
		BaseURL: observabilityConfig.ArchiveObjectStoreURL, StorageID: observabilityConfig.ArchiveObjectStorageID,
		Prefix: observabilityConfig.ArchiveObjectPrefix,
	})
	archiveSource := archivesvc.Router{}
	if databaseStore, ok := sessionStore.(*mariadbsessionstore.Store); ok {
		archiveSource.Trace = archivesvc.TraceBundleSource{
			Core:      mariadbsessionstore.NewTraceArchiveSource(databaseStore),
			Technical: opensearchtraceaccess.NewArchiveStore(openSearchClient, openSearchConfig.TraceIndex),
		}
	}
	if coreConfig.ProjectionEnabled {
		archiveSource.Log = opensearchconversationaudit.NewArchiveSource(openSearchClient, coreConfig.ProjectionIndex)
	}
	archiveHandler := httphandler.NewArchiveHandler(archivesvc.New(archiveStore, archiveSource, archiveObjectStore, archivesvc.Options{}), evidenceHandler)
	traceHandler := httphandler.NewTraceHandlerWithTechnicalSources(
		traceQueryService, evidenceService, sessionService,
	)
	sessionHandler := httphandler.NewSessionHandlerWithAssembly(
		sessionService,
		assemblysvc.NewQueryServiceWithBusinessResolver(sessionStore, ledgerStore, resolver),
	)
	ledgerHandler := httphandler.NewConfiguredLedgerHandler(ledgersvc.NewWithMetrics(ledgerStore, metrics))

	enterpriseReader := httphandler.NewEnterpriseInteractionFactsReader(evidenceService, sessionService)
	app := newAppWithArchive(
		httpServerConfig, traceHandler, evidenceHandler, logHandler, archiveHandler,
		sessionHandler, ledgerHandler, metrics, enterpriseReader,
	)
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
	if coverageMonitorEnabled {
		coverageMonitor := sourcecoveragesvc.New(
			coverageStore,
			otelcolmetrics.New(observabilityConfig.SourceCoverageMetricsEndpoint, &http.Client{Timeout: 3 * time.Second}),
			sourcecoveragesvc.Options{
				SourceID: observabilityConfig.SourceCoverageSourceID, DeploymentID: observabilityConfig.SourceCoverageDeploymentID,
			},
		)
		app.workers.Add(1)
		go func() {
			defer app.workers.Done()
			runSourceCoverageMonitor(workerContext, observabilityConfig.SourceCoverageInterval, coverageMonitor)
		}()
	}
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
		} else if err := sink.EnsureBootstrap(workerContext, coreConfig.ProjectionBootstrapVersion); err != nil {
			stopWorkers()
			app.workers.Wait()
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, fmt.Errorf("initialize projection alias: %w", err)
		}
		worker := projectorsvc.NewWorker(outboxStore, sink, projectorsvc.WorkerOptions{
			Metrics: metrics, HistoricalProvenanceHandler: provenanceHandler,
		})
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
	if err := store.EnsureSchema(context.Background(), config.AutoMigrate); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
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

func runSourceCoverageMonitor(ctx context.Context, interval time.Duration, service *sourcecoveragesvc.Service) {
	observe := func() {
		if err := service.Observe(ctx); err != nil && ctx.Err() == nil {
			log.Printf("BKN Trace source coverage monitor failed: %v", err)
		}
	}
	observe()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			observe()
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

// newApp preserves the community route test seam. Archive routes are mounted
// only by the fully assembled production application below.
func newApp(
	httpServerConfig conf.HTTPServerConfig,
	traceHandler *httphandler.TraceHandler,
	evidenceHandler *httphandler.EvidenceHandler,
	logHandler *httphandler.LogHandler,
	sessionHandler *httphandler.SessionHandler,
	ledgerHandler *httphandler.LedgerHandler,
	metrics http.Handler,
	enterpriseReaders ...enterpriseroute.Reader,
) *App {
	return newAppWithArchive(httpServerConfig, traceHandler, evidenceHandler, logHandler, nil, sessionHandler, ledgerHandler, metrics, enterpriseReaders...)
}

func newAppWithArchive(
	httpServerConfig conf.HTTPServerConfig,
	traceHandler *httphandler.TraceHandler,
	evidenceHandler *httphandler.EvidenceHandler,
	logHandler *httphandler.LogHandler,
	archiveHandler *httphandler.ArchiveHandler,
	sessionHandler *httphandler.SessionHandler,
	ledgerHandler *httphandler.LedgerHandler,
	metrics http.Handler,
	enterpriseReaders ...enterpriseroute.Reader,
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

	// These pre-0.1.4 raw query contracts are intentionally unavailable. Keep
	// exact tombstones ahead of the typed /traces/{trace_id} dispatch so callers
	// receive 404 instead of treating a removed route name as a Trace ID.
	mux.HandleFunc(APIBasePath+"/traces/_search", http.NotFound)
	mux.HandleFunc(APIBasePath+"/traces/by-conversation", http.NotFound)
	mux.HandleFunc(APIBasePath+"/traces/by-request", http.NotFound)
	mux.HandleFunc(APIBasePath+"/traces/by-request/business-graph", http.NotFound)
	mux.HandleFunc(APIBasePath+"/traces/by-request/snapshot-preview", http.NotFound)
	mux.HandleFunc(APIBasePath+"/traces", readAuth(evidenceHandler.ListTraceExecutions))
	typedTraceDetail := readAuth(func(w http.ResponseWriter, r *http.Request) {
		if traceHandler.GetTraceSubresource(w, r) {
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc(APIBasePath+"/traces/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSuffix(r.URL.Path, "/") == APIBasePath+"/traces" {
			readAuth(evidenceHandler.ListTraceExecutions)(w, r)
			return
		}
		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/trace-graph") {
			http.NotFound(w, r)
			return
		}
		typedTraceDetail(w, r)
	})
	mux.HandleFunc(APIBasePath+"/evidence/events", ledgerHandler.Ingest)
	mux.HandleFunc(APIBasePath+"/evidence/artifacts", evidenceHandler.IngestEvidenceArtifact)
	mux.HandleFunc(APIBasePath+"/evidence/artifacts/", readAuth(evidenceHandler.GetEvidenceArtifact))
	mux.HandleFunc(APIBasePath+"/access-profile", readAuth(evidenceHandler.GetAccessProfile))
	mux.HandleFunc(ObservabilityAPIBasePath+"/logs", readAuth(logHandler.ListLogs))
	mux.HandleFunc(ObservabilityAPIBasePath+"/logs/", readAuth(logHandler.GetLog))
	mux.HandleFunc(ObservabilityAPIBasePath+"/log-sources", readAuth(logHandler.ListLogSources))
	mux.HandleFunc(ObservabilityAPIBasePath+"/log-policies", readAuth(logHandler.ListLogPolicies))
	if archiveHandler != nil {
		mux.HandleFunc(ObservabilityAPIBasePath+"/log-archive-overview", readAuth(archiveHandler.Overview(observabilityvo.ArchiveKindLog)))
		mux.HandleFunc(ObservabilityAPIBasePath+"/trace-archive-overview", readAuth(archiveHandler.Overview(observabilityvo.ArchiveKindTrace)))
		mux.HandleFunc(ObservabilityAPIBasePath+"/log-archive-jobs", readAuth(archiveHandler.ListOrCreate(observabilityvo.ArchiveKindLog)))
		mux.HandleFunc(ObservabilityAPIBasePath+"/trace-archive-jobs", readAuth(archiveHandler.ListOrCreate(observabilityvo.ArchiveKindTrace)))
		mux.HandleFunc(ObservabilityAPIBasePath+"/archive-jobs/", readAuth(archiveHandler.GetOrRetry))
	}
	var enterpriseReader enterpriseroute.Reader
	if len(enterpriseReaders) > 0 {
		enterpriseReader = enterpriseReaders[0]
	}
	enterpriseroute.Mount(mux, enterpriseReader, func(next http.Handler) http.Handler {
		return http.HandlerFunc(readAuth(next.ServeHTTP))
	})
	httphandler.RegisterSessionRoutes(mux, APIBasePath, sessionHandler, evidenceHandler.RequirePublicLifecycleIdentity)
	mux.Handle(APIBasePath+"/swagger/", httpSwagger.Handler(
		httpSwagger.URL(APIBasePath+"/swagger/doc.json"),
	))

	internalMux := http.NewServeMux()
	internal := evidenceHandler.InternalLifecycle
	lifecycle := func(next http.HandlerFunc) http.HandlerFunc {
		return internal(evidenceHandler.RequireTrustedLifecycleIdentity(next))
	}
	httphandler.RegisterSessionRoutes(internalMux, APIBasePath, sessionHandler, lifecycle)

	publicHandler := observabilitylocale.PrivateNoCacheForPrefixes(
		observabilitylocale.LanguageMiddleware(mux),
		APIBasePath,
		ObservabilityAPIBasePath,
	)
	internalHandler := observabilitylocale.PrivateNoCacheForPrefixes(
		observabilitylocale.LanguageMiddleware(internalMux),
		APIBasePath,
	)

	return &App{
		server:         httpserver.New(httpServerConfig.Address, publicHandler),
		internalServer: httpserver.New(httpServerConfig.InternalAddress, internalHandler),
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
