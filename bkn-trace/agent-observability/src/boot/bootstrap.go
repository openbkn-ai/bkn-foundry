// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturecontrollersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysnapshot"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditvalidator"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/capturepolicystore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	memorysessionstore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/extension/enterpriseroute"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/coremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/server/httpserver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionrebuild"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isourcecoveragestore"
	"github.com/openbkn-ai/bkn-foundry/comm-go/projectiongrant"
	kafka "github.com/segmentio/kafka-go"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type App struct {
	server         *httpserver.Server
	internalServer *httpserver.Server
	closeDatabase  func() error
	stopWorkers    context.CancelFunc
	workers        sync.WaitGroup
	projection     *projectorsvc.Worker
	kafkaRuntimes  []*kafkaruntime.Runtime
	kafkaHealth    *kafkaHealth
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
	kafkaConfig, err := conf.NewKafkaConsumerConfig()
	if err != nil {
		return nil, err
	}
	metrics := coremetrics.New()
	sessionStore, ledgerStore, closeDatabase, err := newCoreStores(coreConfig)
	if err != nil {
		return nil, err
	}
	var capturePolicyReader capturepolicysvc.Reader
	var capturePolicyCommander capturepolicysvc.Commander
	var captureController *capturecontrollersvc.Controller
	if durable, ok := sessionStore.(interface {
		ReadCapturePolicySnapshot(context.Context) (capturepolicysvc.Snapshot, error)
	}); ok {
		capturePolicyReader = capturepolicysvc.ReaderFunc(durable.ReadCapturePolicySnapshot)
		if maria, ok := sessionStore.(*mariadbsessionstore.Store); ok {
			captureController, err = capturecontrollersvc.New(capturecontrollersvc.Options{
				Store: maria, WorkerID: "agent-observability-control-controller",
				Lease: 30 * time.Second, Convergence: 10 * time.Minute,
			})
			if err != nil {
				if closeDatabase != nil {
					_ = closeDatabase()
				}
				return nil, fmt.Errorf("initialize capture policy controller: %w", err)
			}
			capturePolicyCommander = captureController
		}
	} else {
		memoryCapturePolicyStore := capturepolicystore.New(capturepolicysvc.Snapshot{
			Revision: 1, DesiredState: capturepolicysvc.StateEnabled,
			EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1,
			Operation: capturepolicysvc.Operation{ID: "bootstrap", Phase: capturepolicysvc.PhaseSucceeded, RequestedState: capturepolicysvc.StateEnabled, ExpectedRevision: 1},
		})
		capturePolicyReader = memoryCapturePolicyStore
		capturePolicyCommander = memoryCapturePolicyStore
	}
	var capturePolicyHandler *httphandler.CapturePolicyHandler
	var capturePolicyWriter interface {
		UpsertEndpointLease(context.Context, icapturepolicy.EndpointLease) error
		RecordAcknowledgement(context.Context, icapturepolicy.ExpectedAcknowledgement) error
	}
	if maria, ok := sessionStore.(*mariadbsessionstore.Store); ok {
		capturePolicyWriter = maria
	}
	capturePolicyHandler = httphandler.NewCapturePolicyHandlerWithInternal(
		capturePolicyReader, capturePolicyCommander, captureController,
		capturepolicysnapshot.Signer{
			PrivateKey: coreConfig.CapturePolicySigningKey,
			KeyID:      coreConfig.CapturePolicySigningKeyID,
			Audience:   coreConfig.CapturePolicyAudience,
			TTL:        coreConfig.CapturePolicySnapshotTTL,
		}, capturePolicyWriter,
	)
	var admissionBudgetSources []capturepolicysvc.AdmissionMeasurementSource
	admissionBudgetSources = append(admissionBudgetSources,
		capturepolicysvc.AdmissionMeasurementSourceFunc(func(ctx context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			metrics, err := openSearchClient.ReadAdmissionMetrics(ctx)
			if err != nil {
				return capturepolicysvc.AdmissionMeasurement{}, err
			}
			return capturepolicysvc.AdmissionMeasurement{Metric: "trace_opensearch_capacity", Source: "opensearch-cluster-stats", SampleTime: metrics.SampledAt, Value: metrics.Capacity, Fresh: true}, nil
		}),
		capturepolicysvc.AdmissionMeasurementSourceFunc(func(ctx context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			metrics, err := openSearchClient.ReadAdmissionMetrics(ctx)
			if err != nil {
				return capturepolicysvc.AdmissionMeasurement{}, err
			}
			return capturepolicysvc.AdmissionMeasurement{Metric: "trace_opensearch_heap", Source: "opensearch-cluster-stats", SampleTime: metrics.SampledAt, Value: metrics.Heap, Fresh: true}, nil
		}),
	)
	collectorMetricsEndpoint := strings.TrimSpace(observabilityConfig.AdmissionBudgetMetricsEndpoint)
	if collectorMetricsEndpoint == "" {
		collectorMetricsEndpoint = strings.TrimSpace(observabilityConfig.SourceCoverageMetricsEndpoint)
	}
	if endpoint := collectorMetricsEndpoint; endpoint != "" {
		collectorMetrics := otelcolmetrics.New(endpoint, &http.Client{Timeout: 3 * time.Second})
		admissionBudgetSources = append(admissionBudgetSources, capturepolicysvc.AdmissionMeasurementSourceFunc(func(ctx context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			sample, err := collectorMetrics.ReadQueueSample(ctx)
			if err != nil {
				return capturepolicysvc.AdmissionMeasurement{}, err
			}
			return capturepolicysvc.AdmissionMeasurement{Metric: "trace_collector_queue", Source: endpoint, SampleTime: sample.SampledAt, Value: sample.Utilization, Fresh: true}, nil
		}))
	} else {
		admissionBudgetSources = append(admissionBudgetSources, capturepolicysvc.AdmissionMeasurementSourceFunc(func(context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			return capturepolicysvc.AdmissionMeasurement{}, errors.New("collector metrics endpoint is not configured")
		}))
	}
	if databaseStore, ok := sessionStore.(interface{ Database() *sql.DB }); ok && databaseStore.Database() != nil {
		database := databaseStore.Database()
		admissionBudgetSources = append(admissionBudgetSources, capturepolicysvc.AdmissionMeasurementSourceFunc(func(context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			stats := database.Stats()
			if stats.MaxOpenConnections <= 0 {
				return capturepolicysvc.AdmissionMeasurement{}, errors.New("Trace storage pool max open connections is not configured")
			}
			value := float64(stats.InUse) / float64(stats.MaxOpenConnections)
			if value > 1 {
				value = 1
			}
			return capturepolicysvc.AdmissionMeasurement{Metric: "trace_storage_connection_pool", Source: "bkn-trace-mariadb", SampleTime: time.Now().UTC(), Value: value, Fresh: true}, nil
		}))
	} else {
		admissionBudgetSources = append(admissionBudgetSources, capturepolicysvc.AdmissionMeasurementSourceFunc(func(context.Context) (capturepolicysvc.AdmissionMeasurement, error) {
			return capturepolicysvc.AdmissionMeasurement{}, errors.New("Trace storage pool is not configured")
		}))
	}
	budgetConfig := observabilityConfig.AdmissionBudgetThresholds
	budgetProvider, budgetErr := capturepolicysvc.NewAdmissionBudgetProvider(
		observabilityConfig.AdmissionBudgetProfile,
		capturepolicysvc.AdmissionBudgetThresholds{
			OpenSearchCapacity: budgetConfig.OpenSearchCapacity,
			OpenSearchHeap:     budgetConfig.OpenSearchHeap,
			CollectorQueue:     budgetConfig.CollectorQueue,
			StoragePool:        budgetConfig.StoragePool,
		}, admissionBudgetSources...,
	)
	if budgetErr != nil {
		log.Printf("Trace admission budget provider unavailable: %v", budgetErr)
	} else {
		capturePolicyHandler.SetAdmissionBudgetReader(budgetProvider)
	}
	var kafkaRuntimes []*kafkaruntime.Runtime
	if kafkaConfig.Evidence.Enabled {
		if closeDatabase != nil {
			_ = closeDatabase()
		}
		return nil, errors.New("evidence Kafka consumer is blocked until the C1 control-plane writer for policy, producer registration, and closure history is integrated")
	}
	if kafkaConfig.Audit.Enabled {
		if !strings.EqualFold(coreConfig.Store, "mariadb") || !coreConfig.AutoMigrate {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, errors.New("enabled Audit Kafka consumer requires Core MariaDB with AutoMigrate enabled")
		}
		databaseStore, ok := sessionStore.(interface{ Database() *sql.DB })
		if !ok || databaseStore.Database() == nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, errors.New("enabled Audit Kafka consumer requires the shared MariaDB ledger connection")
		}
		auditLedger, err := auditstore.New(databaseStore.Database())
		if err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, err
		}
		if err := auditLedger.EnsureMonthlyWindow(context.Background(), time.Now().UTC()); err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, fmt.Errorf("ensure Audit monthly schema window: %w", err)
		}
		validator, err := auditvalidator.New()
		if err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, err
		}
		protocol, err := auditconsumer.New(validator, auditLedger, kafkaNoopCommitter{})
		if err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, err
		}
		runtime, err := kafkaruntime.New(kafkaConfig, kafkaConfig.Audit, func(ctx context.Context, message kafka.Message) error {
			headers := make([]auditconsumer.Header, 0, len(message.Headers))
			for _, header := range message.Headers {
				headers = append(headers, auditconsumer.Header{Key: header.Key, Value: append([]byte(nil), header.Value...)})
			}
			record := auditconsumer.Record{Topic: message.Topic, Key: message.Key, Value: message.Value, Headers: headers, Partition: message.Partition, Offset: message.Offset, BrokerTime: message.Time.UTC()}
			return protocol.Process(ctx, record)
		})
		if err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, err
		}
		kafkaRuntimes = append(kafkaRuntimes, runtime)
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
	runtimeLogSources := []logsvc.Source{
		opensearchlogaccess.New(openSearchClient, openSearchConfig.LogIndex),
		bknsafeuseraccess.New(accessScopeConfig.BKNBaseURL, localizedHTTPClient(accessScopeConfig.Timeout)),
	}
	legacyAuditSources := []logsvc.Source{
		bknsafeaudit.New(accessScopeConfig.BKNBaseURL, localizedHTTPClient(accessScopeConfig.Timeout)),
		logsvc.NewNotIntegratedSource("bkn-safe-security", []string{
			observabilityvo.CategoryAuditSecurity,
		}, []string{"BKN Safe Authorization"}),
		bknbackendaudit.New(resolverConfig.BKNBaseURL, localizedHTTPClient(resolverConfig.Timeout)),
		vegaaudit.New(resolverConfig.VegaBaseURL, localizedHTTPClient(resolverConfig.Timeout)),
		executionfactoryaudit.New(resolverConfig.ExecutionFactoryURL, localizedHTTPClient(resolverConfig.Timeout)),
		modelmanageraudit.New(resolverConfig.ModelManagerURL, localizedHTTPClient(resolverConfig.Timeout)),
	}
	if coreConfig.ProjectionEnabled {
		runtimeLogSources = append(runtimeLogSources, opensearchconversationaudit.New(openSearchClient, coreConfig.ProjectionIndex))
		runtimeLogSources = append(runtimeLogSources, opensearchruntimeaudit.New(openSearchClient, coreConfig.ProjectionIndex))
	}
	var auditSource logsvc.Source
	if kafkaConfig.Audit.Enabled {
		auditQueryDB, err := openMariaDBPool(coreConfig.MariaDBDSN, 8, 2)
		if err != nil {
			if closeDatabase != nil {
				_ = closeDatabase()
			}
			return nil, fmt.Errorf("open isolated Audit query pool: %w", err)
		}
		primaryClose := closeDatabase
		closeDatabase = func() error {
			if primaryClose == nil {
				return auditQueryDB.Close()
			}
			return errors.Join(auditQueryDB.Close(), primaryClose())
		}
		reader, err := auditstore.NewReader(auditQueryDB)
		if err != nil {
			_ = closeDatabase()
			return nil, err
		}
		if kafkaConfig.Audit.Enabled {
			auditSource = httphandler.NewAuditLedgerSource(reader)
		}
	}
	if kafkaConfig.Audit.Enabled && auditSource == nil {
		if closeDatabase != nil {
			_ = closeDatabase()
		}
		return nil, errors.New("audit Kafka query source requires the MariaDB ledger")
	}
	logSources := assembleLogSources(runtimeLogSources, legacyAuditSources, auditSource, kafkaConfig.Audit.Enabled)
	logHandler := httphandler.NewLogHandler(logsvc.NewWithOptions(logSources, logOptions), evidenceHandler)
	provenanceHandler := enterpriseroute.HistoricalProvenanceHandler()
	if coreConfig.HistoricalProvenanceEnabled && provenanceHandler == nil {
		return nil, errors.New("historical provenance projection requires a registered enterprise handler")
	}
	historicalProvenanceEnabled := coreConfig.HistoricalProvenanceEnabled
	sessionOptions := sessionsvc.Options{
		EnableHistoricalProvenance: historicalProvenanceEnabled,
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
	}
	if historicalProvenanceEnabled {
		if len(coreConfig.ProjectionGrantPrivateKey) == 0 {
			return nil, errors.New("historical provenance projection requires BKN_TRACE_PROJECTION_GRANT_PRIVATE_KEY")
		}
		sessionOptions.ProjectionGrantIssuer = coreConfig.ProjectionGrantIssuer
		sessionOptions.ProjectionGrantKeyID = coreConfig.ProjectionGrantKeyID
		sessionOptions.ProjectionGrantAudience = coreConfig.ProjectionGrantAudience
		sessionOptions.ProjectionGrantTTL = coreConfig.ProjectionGrantTTL
		sessionOptions.ProjectionGrantSigner = func(claims projectiongrant.Claims) (string, error) {
			return projectiongrant.Sign(claims, coreConfig.ProjectionGrantPrivateKey)
		}
	}
	sessionService := sessionsvc.New(sessionStore, sessionOptions)
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

	var captureInput func(context.Context, string, evidencevo.QueryScope) (json.RawMessage, string, bool, error)
	if snapshots, ok := sessionStore.(isessionstore.EvidenceSnapshotReader); ok {
		if artifacts, ok := evidenceStore.(iartifactstore.CaptureReader); ok {
			captureService := evidencesvc.NewCaptureService(snapshots, artifacts)
			captureInput = func(ctx context.Context, id string, scope evidencevo.QueryScope) (json.RawMessage, string, bool, error) {
				capture, found, err := captureService.Capture(ctx, id, scope, evidencesvc.CaptureLimits{MaxReads: 1000, MaxResponseBytes: 8 << 20, MaxReadBytes: 64 << 20, MaxContentBytes: 8 << 20, MaxCapturedBytes: 64 << 20})
				if err != nil || !found {
					return nil, "", found, err
				}
				raw, hash, err := evidencesvc.EncodeCapture(capture, 64<<20)
				return raw, hash, true, err
			}
		}
	}
	enterpriseReader := httphandler.NewEnterpriseInteractionFactsReader(evidenceService, sessionService, captureInput)
	app := newAppWithCapturePolicy(
		httpServerConfig, traceHandler, evidenceHandler, logHandler, archiveHandler,
		sessionHandler, ledgerHandler, metrics, capturePolicyHandler, enterpriseReader,
	)
	app.closeDatabase = closeDatabase
	app.kafkaRuntimes = kafkaRuntimes
	for _, runtime := range kafkaRuntimes {
		app.kafkaHealth.set("audit", runtime)
	}
	workerContext, stopWorkers := context.WithCancel(context.Background())
	app.stopWorkers = stopWorkers
	if captureController != nil {
		app.workers.Add(1)
		go func() {
			defer app.workers.Done()
			runCapturePolicyController(workerContext, captureController)
		}()
	}
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

func assembleLogSources(runtimeSources, legacyAuditSources []logsvc.Source, centerAuditSource logsvc.Source, kafkaAuditEnabled bool) []logsvc.Source {
	capacity := len(runtimeSources) + len(legacyAuditSources)
	if kafkaAuditEnabled {
		capacity = len(runtimeSources) + 1
	}
	sources := make([]logsvc.Source, 0, capacity)
	if kafkaAuditEnabled {
		if centerAuditSource != nil {
			sources = append(sources, centerAuditSource)
		}
		sources = append(sources, runtimeSources...)
		return sources
	}
	sources = append(sources, runtimeSources...)
	sources = append(sources, legacyAuditSources...)
	return sources
}

func newCoreStores(config conf.CoreConfig) (isessionstore.Store, ievidenceledger.Store, func() error, error) {
	if !strings.EqualFold(config.Store, "mariadb") {
		return memorysessionstore.New(), ledgerstore.New(), nil, nil
	}
	if config.MariaDBDSN == "" {
		return nil, nil, nil, errors.New("BKN_TRACE_CORE_MARIADB_DSN is required when BKN_TRACE_CORE_STORE=mariadb")
	}
	db, err := openMariaDBPool(config.MariaDBDSN, 16, 4)
	if err != nil {
		return nil, nil, nil, err
	}
	store := mariadbsessionstore.New(db)
	if err := store.EnsureSchema(context.Background(), config.AutoMigrate); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	if err := store.EnsureControlState(context.Background(), config.CapturePolicyInitialState == "enabled", time.Now().UTC()); err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("initialize Trace/Evidence control state: %w", err)
	}
	return store, store, db.Close, nil
}

func openMariaDBPool(dsn string, maxOpen, maxIdle int) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open BKN Trace MariaDB: %w", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect BKN Trace MariaDB: %w", err)
	}
	return db, nil
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

func newAppWithCapturePolicy(
	httpServerConfig conf.HTTPServerConfig,
	traceHandler *httphandler.TraceHandler,
	evidenceHandler *httphandler.EvidenceHandler,
	logHandler *httphandler.LogHandler,
	archiveHandler *httphandler.ArchiveHandler,
	sessionHandler *httphandler.SessionHandler,
	ledgerHandler *httphandler.LedgerHandler,
	metrics http.Handler,
	capturePolicyHandler *httphandler.CapturePolicyHandler,
	enterpriseReaders ...enterpriseroute.Reader,
) *App {
	return newAppWithArchiveAndCapture(httpServerConfig, traceHandler, evidenceHandler, logHandler, archiveHandler, sessionHandler, ledgerHandler, metrics, capturePolicyHandler, enterpriseReaders...)
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
	return newAppWithArchiveAndCapture(httpServerConfig, traceHandler, evidenceHandler, logHandler, archiveHandler, sessionHandler, ledgerHandler, metrics, nil, enterpriseReaders...)
}

func newAppWithArchiveAndCapture(
	httpServerConfig conf.HTTPServerConfig,
	traceHandler *httphandler.TraceHandler,
	evidenceHandler *httphandler.EvidenceHandler,
	logHandler *httphandler.LogHandler,
	archiveHandler *httphandler.ArchiveHandler,
	sessionHandler *httphandler.SessionHandler,
	ledgerHandler *httphandler.LedgerHandler,
	metrics http.Handler,
	capturePolicyHandler *httphandler.CapturePolicyHandler,
	enterpriseReaders ...enterpriseroute.Reader,
) *App {
	health := newKafkaHealth()
	disabledEvidence, _ := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{}, nil, nil)
	disabledAudit, _ := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{}, nil, nil)
	health.set("evidence", disabledEvidence)
	health.set("audit", disabledAudit)
	mux := http.NewServeMux()
	mux.HandleFunc("/health/ready", health.serveHTTP)
	mux.HandleFunc("/health/live", health.serveLiveHTTP)
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
	if capturePolicyHandler != nil {
		capturePolicyRoute := evidenceHandler.RequireTraceEvidenceConfigurationPermission(capturePolicyHandler.HandleTraceEvidenceConfiguration)
		mux.HandleFunc(APIBasePath+"/trace-evidence-configuration", readAuth(capturePolicyRoute))
		captureOperationRoute := evidenceHandler.RequireTraceEvidenceConfigurationPermission(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				capturePolicyHandler.ReconcileTraceEvidenceOperation(w, r)
				return
			}
			capturePolicyHandler.GetTraceEvidenceOperation(w, r)
		})
		mux.HandleFunc(APIBasePath+"/trace-evidence-operations/", readAuth(captureOperationRoute))
	}
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
	internalMux.HandleFunc("/health/ready", health.serveHTTP)
	internal := evidenceHandler.InternalLifecycle
	lifecycle := func(next http.HandlerFunc) http.HandlerFunc {
		return internal(evidenceHandler.RequireTrustedLifecycleIdentity(next))
	}
	httphandler.RegisterSessionRoutes(internalMux, APIBasePath, sessionHandler, lifecycle)
	if capturePolicyHandler != nil {
		workload := func(next http.HandlerFunc) http.HandlerFunc {
			return internal(evidenceHandler.RequireTrustedServicePrincipal(next))
		}
		internalMux.HandleFunc(APIBasePath+"/internal/trace-evidence/policy", workload(capturePolicyHandler.GetInternalTraceEvidencePolicy))
		internalMux.HandleFunc(APIBasePath+"/internal/trace-evidence/endpoints:heartbeat", workload(capturePolicyHandler.HeartbeatInternalTraceEvidenceEndpoint))
		internalMux.HandleFunc(APIBasePath+"/internal/trace-evidence/operations/", workload(capturePolicyHandler.AcknowledgeInternalTraceEvidenceOperation))
	}

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
		kafkaHealth:    health,
	}
}

func (a *App) Start() error {
	startedKafka := make([]*kafkaruntime.Runtime, 0, len(a.kafkaRuntimes))
	for _, runtime := range a.kafkaRuntimes {
		if err := runtime.Start(context.Background()); err != nil {
			for _, started := range startedKafka {
				_ = started.Shutdown(context.Background())
			}
			return fmt.Errorf("start Kafka consumer runtime: %w", err)
		}
		startedKafka = append(startedKafka, runtime)
	}
	if a.internalServer == nil {
		err := a.server.Start()
		if err != nil {
			stopStartedKafka(startedKafka)
		}
		return err
	}
	internalResult, err := a.internalServer.StartAsync()
	if err != nil {
		stopStartedKafka(startedKafka)
		return fmt.Errorf("start BKN Trace internal listener: %w", err)
	}
	publicResult, err := a.server.StartAsync()
	if err != nil {
		_ = a.internalServer.Shutdown(context.Background())
		stopStartedKafka(startedKafka)
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
	for _, runtime := range a.kafkaRuntimes {
		shutdownErr = errors.Join(shutdownErr, runtime.Shutdown(ctx))
	}
	if a.server != nil {
		shutdownErr = errors.Join(shutdownErr, a.server.Shutdown(ctx))
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

func stopStartedKafka(runtimes []*kafkaruntime.Runtime) {
	for i := len(runtimes) - 1; i >= 0; i-- {
		_ = runtimes[i].Shutdown(context.Background())
	}
}

type kafkaNoopCommitter struct{}

func (kafkaNoopCommitter) Commit(context.Context, int, int64) error { return nil }

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
