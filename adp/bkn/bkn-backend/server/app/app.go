// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package app owns the bkn-backend process lifecycle. Paid distributions can
// boot the open-core application, assemble private extensions, and only then
// start serving requests.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	// _ "net/http/pprof"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_execution"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_schedule"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/agent_operator"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/capability_binding"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/concept_group"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/kn_proxy"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/model_factory"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/opensearch"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/permission"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/relation_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/risk_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/user_mgmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/vega_backend"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	vega_backend_service "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/worker"
)

// Options controls application bootstrapping. It is intentionally empty today
// so callers do not need to change when injectable boot dependencies are added.
type Options struct{}

// Application is a booted bkn-backend process that has not started serving yet.
// Extension assembly belongs between Boot and Run.
type Application struct {
	appSetting       *common.AppSetting
	db               *sql.DB
	otelProviders    *otel.Providers
	restHandler      driveradapters.RestHandler
	conceptSyncer    *worker.ConceptSyncer
	scheduleWorker   *worker.ScheduleWorker
	proxySyncWorker  *worker.ProxySyncWorker
	evidenceRuntime  *evidencepublisher.PublisherRuntime
	evidenceProducer interface{ Close() error }
	auditRuntime     *operationaudit.KafkaRuntime
	auditTelemetry   *operationaudit.PublishTelemetry
	refresh          func(stop <-chan struct{})
	stop             chan struct{}
	extensionMu      sync.Mutex
	extensionsFrozen bool
	routeExtensions  map[string]func(*gin.Engine)
}

// Setting exposes immutable boot configuration to explicitly assembled extensions.
func (server *Application) Setting() *common.AppSetting {
	return server.appSetting
}

// DB exposes the shared database connection to persistence adapters assembled by extensions.
func (server *Application) DB() *sql.DB {
	return server.db
}

// RegisterRoutes adds one explicitly named route extension. Registration is
// only valid after Boot and before Run; duplicate names are rejected so a paid
// binary cannot silently replace another capability's surface.
func (server *Application) RegisterRoutes(name string, install func(*gin.Engine)) error {
	server.extensionMu.Lock()
	defer server.extensionMu.Unlock()
	if server.extensionsFrozen {
		return fmt.Errorf("bkn-backend app: register routes %q after Run", name)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("bkn-backend app: route extension name is required")
	}
	if install == nil {
		return fmt.Errorf("bkn-backend app: route extension %q has no installer", name)
	}
	if _, exists := server.routeExtensions[name]; exists {
		return fmt.Errorf("bkn-backend app: route extension %q already registered", name)
	}
	server.routeExtensions[name] = install
	return nil
}

func (server *Application) freezeRouteExtensions() []func(*gin.Engine) {
	server.extensionMu.Lock()
	defer server.extensionMu.Unlock()
	if server.extensionsFrozen {
		panic("bkn-backend app: Run called more than once")
	}
	server.extensionsFrozen = true
	names := make([]string, 0, len(server.routeExtensions))
	for name := range server.routeExtensions {
		names = append(names, name)
	}
	sort.Strings(names)
	installers := make([]func(*gin.Engine), 0, len(names))
	for _, name := range names {
		installers = append(installers, server.routeExtensions[name])
	}
	return installers
}

// Run freezes the assembled process shape and serves until shutdown.
func (server *Application) Run() {
	logger.Info("Server Starting")
	routeExtensions := server.freezeRouteExtensions()
	entitlement.Freeze()
	if caps := entitlement.Assembled(); len(caps) > 0 {
		logger.Infof("Extensions assembled: %+v", caps)
	}
	if server.refresh != nil {
		go server.refresh(server.stop)
	}

	vbs := vega_backend_service.NewVegaBackendService(server.appSetting, logics.VBA)
	err := logics.Init(context.Background(), server.appSetting, vbs)
	if err != nil {
		panic(err)
	}

	// Create the Gin engine and register APIs.
	engine := gin.New()
	engine.GET("/metrics", gin.WrapH(server.auditTelemetry))

	server.restHandler.RegisterPublic(engine)
	for _, install := range routeExtensions {
		install(engine)
	}
	logger.Info("Server Register API Success")

	go server.conceptSyncer.Start()
	go server.scheduleWorker.Start()
	server.proxySyncWorker.Start()

	// Listen for interrupt signals (SIGINT and SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// Receiving a signal triggers ctx.Done. stop stops receiving registered signals and releases those resources.
	defer stop()
	var runtimeDone <-chan error
	var flusher evidenceFlusher
	if server.evidenceRuntime != nil {
		flusher = server.evidenceRuntime
		done := make(chan error, 1)
		runtimeDone = done
		go func() { done <- server.evidenceRuntime.Run(ctx) }()
	}
	flushDone := startEvidenceFlushLoop(ctx, flusher, time.Second)

	// Initialize the HTTP service.
	s := &http.Server{
		Addr:           ":" + strconv.Itoa(server.appSetting.ServerSetting.HttpPort),
		Handler:        engine,
		ReadTimeout:    server.appSetting.ServerSetting.ReadTimeOut * time.Second,
		WriteTimeout:   server.appSetting.ServerSetting.WriteTimeout * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Start the HTTP service.
	go func() {
		err := s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("s.ListenAndServe err:%v", err)
		}
	}()

	logger.Infof("Server Started on Port:%d", server.appSetting.ServerSetting.HttpPort)

	<-ctx.Done()
	close(server.stop)

	// Set the system's last processed time.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	<-flushDone
	if runtimeDone != nil {
		<-runtimeDone
	}

	// Stop the HTTP service.
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatalf("Server Shutdown:%v", err)
	}
	// Stop accepting mutations and let in-flight handlers finish before the
	// outbox workers are canceled. Otherwise a request can enqueue work after
	// every local worker has already exited.
	server.proxySyncWorker.Stop()
	if server.evidenceRuntime != nil {
		if _, err := server.evidenceRuntime.Close(ctx); err != nil {
			logger.Warnf("Evidence publisher runtime close failed: %v", err)
		}
	}
	if server.evidenceProducer != nil {
		if err := bkntrace.CloseEvidenceProducer(server.evidenceProducer); err != nil {
			logger.Warnf("Evidence Kafka producer close failed: %v", err)
		}
	}
	if err := server.auditRuntime.Close(); err != nil {
		logger.Warnf("Audit Kafka producer close failed: %v", err)
	}

	server.otelProviders.Shutdown(ctx)

	logger.Info("Server Exited")
}

type evidenceFlusher interface {
	Flush(context.Context) evidencepublisher.DrainResult
}

func startEvidenceFlushLoop(ctx context.Context, publisher evidenceFlusher, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if publisher == nil {
		close(done)
		return done
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ticker.C:
				publisher.Flush(context.Background())
			case <-ctx.Done():
				return
			}
		}
	}()
	return done
}

// Boot initializes shared infrastructure without opening the HTTP listener.
// Paid entry points may register extensions after Boot returns and before Run.
func Boot(_ Options) *Application {
	// Enable pprof.
	// go func() {
	// 	http.ListenAndServe("0.0.0.0:6060", nil)
	// }()

	logger.Info("Server Initializing")

	// Initialize service configuration.
	appSetting := common.NewSetting()
	logger.Info("Server Init Setting Success")

	// Configure error-code locales.
	rest.SetLang(appSetting.ServerSetting.Language)
	logger.Info("Server Set Language Success")

	// Configure Gin run mode.
	gin.SetMode(appSetting.ServerSetting.RunMode)
	logger.Infof("Server RunMode: %s", appSetting.ServerSetting.RunMode)

	logger.Infof("Server Start By Port:%d", appSetting.ServerSetting.HttpPort)

	otelProviders, err := otel.InitOTel(context.Background(), &appSetting.OtelSetting)
	if err != nil {
		logger.Fatalf("Failed to initialize OpenTelemetry provider: %v", err)
	}

	// Initialize the database connection.
	db := libdb.NewDB(&appSetting.DBSetting)
	logics.SetDB(db)
	gate, refresh := entitlement.GateWithRunner()
	entitlement.SetGate(gate)
	var publisherRuntime *bkntrace.EvidencePublisherRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED")), "true") {
		publisherRuntime, err = bkntrace.NewEvidencePublisherRuntime()
		if err != nil {
			logger.Warnf("Evidence Kafka publisher unavailable; evidence will be dropped: %v", err)
			publisherRuntime = nil
		} else {
			bkntrace.SetEvidencePublisher(publisherRuntime.Runtime)
		}
	} else {
		logger.Warn("BKN Trace Evidence Kafka publisher is disabled; workload is not 0.2-ready and evidence events will be dropped")
	}

	auditTelemetry := operationaudit.NewPublishTelemetry()
	var auditRuntime *operationaudit.KafkaRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_AUDIT_KAFKA_ENABLED")), "true") {
		auditRuntime, err = operationaudit.NewKafkaRuntimeFromEnv(auditTelemetry)
		if err != nil {
			if errors.Is(err, operationaudit.ErrInvalidKafkaConfiguration) {
				logger.Fatalf("Audit Kafka publisher enabled but invalid: %v", err)
			}
			logger.Warnf("Audit Kafka publisher unavailable; audit coverage_gap: %v", err)
		}
	} else {
		logger.Warn("Audit Kafka publisher is disabled; management Audit coverage_gap")
	}

	// Authentication, authorization, and managed-proxy enforcement are mandatory.
	bknSafeURL, err := common.NormalizeBknSafeURL(os.Getenv("BKN_SAFE_URL"))
	if err != nil {
		logger.Fatalf("Invalid bkn-safe configuration: %v", err)
	}
	logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
	logics.SetPermissionAccess(permission.NewPermissionAccess(bknSafeURL))
	logics.SetManagedProxyAccess(kn_proxy.NewManagedProxyAccess(bknSafeURL))
	logics.SetUserMgmtAccess(user_mgmt.NewUserMgmtAccess(bknSafeURL))
	logics.SetActionScheduleAccess(action_schedule.NewActionScheduleAccess(appSetting))
	logics.SetActionExecutionAccess(action_execution.NewActionExecutionAccess(appSetting))
	logics.SetAgentOperatorAccess(agent_operator.NewAgentOperatorAccess(appSetting))
	logics.SetActionTypeAccess(action_type.NewActionTypeAccess(appSetting))
	logics.SetConceptGroupAccess(concept_group.NewConceptGroupAccess(appSetting))
	logics.SetKNAccess(knowledge_network.NewKNAccess(appSetting))
	logics.SetKNProxyAccess(kn_proxy.NewAccess(db))
	proxyOutboxAccess := kn_proxy.NewOutboxAccess(db)
	if appSetting.ServerSetting.ProxySyncWorkerEnabled {
		logics.SetKNProxyOutboxAccess(proxyOutboxAccess)
	} else {
		// A disabled consumer must not leave producers accepting writes into an
		// undrained queue. A nil outbox keeps the existing synchronous publication
		// path active during a stopped upgrade or an operator kill switch.
		logics.SetKNProxyOutboxAccess(nil)
	}
	logics.SetCapabilityBindingAccess(capability_binding.NewCapabilityBindingAccess(appSetting))
	logics.SetMetricAccess(metric.NewMetricAccess(appSetting))
	logics.SetModelFactoryAccess(model_factory.NewModelFactoryAccess(appSetting))
	logics.SetOpenSearchAccess(opensearch.NewOpenSearchAccess(appSetting))
	logics.SetObjectTypeAccess(object_type.NewObjectTypeAccess(appSetting))
	logics.SetRelationTypeAccess(relation_type.NewRelationTypeAccess(appSetting))
	logics.SetRiskTypeAccess(risk_type.NewRiskTypeAccess(appSetting))
	logics.SetVegaBackendAccess(vega_backend.NewVegaBackendAccess(appSetting))

	// Create and start the service.
	var auditRecorder interface {
		Record(context.Context, operationaudit.Entry) error
	} = operationaudit.NewKafkaRecorder(nil, os.Getenv("BKN_AUDIT_ENVIRONMENT"), auditTelemetry)
	if auditRuntime != nil {
		auditRecorder = operationaudit.NewKafkaRecorder(auditRuntime.Publisher, os.Getenv("BKN_AUDIT_ENVIRONMENT"), auditTelemetry)
	}
	server := &Application{
		appSetting:      appSetting,
		db:              db,
		otelProviders:   otelProviders,
		restHandler:     driveradapters.NewRestHandler(appSetting, auditRecorder),
		auditRuntime:    auditRuntime,
		auditTelemetry:  auditTelemetry,
		refresh:         refresh,
		stop:            make(chan struct{}),
		routeExtensions: make(map[string]func(*gin.Engine)),
		conceptSyncer:   worker.NewConceptSyncer(appSetting),
		scheduleWorker:  worker.NewScheduleWorker(appSetting),
		proxySyncWorker: worker.NewProxySyncWorker(appSetting, proxyOutboxAccess, logics.MPA),
	}
	if publisherRuntime != nil {
		server.evidenceRuntime = publisherRuntime.Runtime
		server.evidenceProducer = publisherRuntime.Producer
	}
	return server
}
