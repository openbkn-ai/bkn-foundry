// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	// _ "net/http/pprof"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	_ "go.uber.org/automaxprocs"

	"bkn-backend/common"
	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
	"bkn-backend/drivenadapters/action_execution"
	"bkn-backend/drivenadapters/action_schedule"
	"bkn-backend/drivenadapters/action_type"
	"bkn-backend/drivenadapters/agent_operator"
	"bkn-backend/drivenadapters/auth"
	"bkn-backend/drivenadapters/capability_binding"
	"bkn-backend/drivenadapters/concept_group"
	"bkn-backend/drivenadapters/kn_proxy"
	"bkn-backend/drivenadapters/knowledge_network"
	"bkn-backend/drivenadapters/metric"
	"bkn-backend/drivenadapters/model_factory"
	"bkn-backend/drivenadapters/object_type"
	"bkn-backend/drivenadapters/opensearch"
	"bkn-backend/drivenadapters/permission"
	"bkn-backend/drivenadapters/relation_type"
	"bkn-backend/drivenadapters/risk_type"
	"bkn-backend/drivenadapters/user_mgmt"
	"bkn-backend/drivenadapters/vega_backend"
	"bkn-backend/driveradapters"
	"bkn-backend/logics"
	vega_backend_service "bkn-backend/logics/vega_backend"
	"bkn-backend/worker"
)

type mgrService struct {
	appSetting        *common.AppSetting
	otelProviders     *otel.Providers
	restHandler       driveradapters.RestHandler
	conceptSyncer     *worker.ConceptSyncer
	scheduleWorker    *worker.ScheduleWorker
	evidencePublisher *evidencepublisher.Publisher
	evidenceProducer  interface{ Close() error }
}

func (server *mgrService) start() {
	logger.Info("Server Starting")

	vbs := vega_backend_service.NewVegaBackendService(server.appSetting, logics.VBA)
	err := logics.Init(context.Background(), server.appSetting, vbs)
	if err != nil {
		panic(err)
	}

	// Create the Gin engine and register APIs.
	engine := gin.New()

	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

	go server.conceptSyncer.Start()
	go server.scheduleWorker.Start()

	// Listen for interrupt signals (SIGINT and SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// Receiving a signal triggers ctx.Done. stop stops receiving registered signals and releases those resources.
	defer stop()
	flushDone := startEvidenceFlushLoop(ctx, server.evidencePublisher, time.Second)

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

	// Set the system's last processed time.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	<-flushDone

	// Stop the HTTP service.
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatalf("Server Shutdown:%v", err)
	}
	if server.evidencePublisher != nil {
		server.evidencePublisher.Flush(ctx)
		server.evidencePublisher.Close(ctx)
	}
	if server.evidenceProducer != nil {
		if err := bkntrace.CloseEvidenceProducer(server.evidenceProducer); err != nil {
			logger.Warnf("Evidence Kafka producer close failed: %v", err)
		}
	}

	server.otelProviders.Shutdown(ctx)

	logger.Info("Server Exited")
}

func startEvidenceFlushLoop(ctx context.Context, publisher *evidencepublisher.Publisher, interval time.Duration) <-chan struct{} {
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

func main() {
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
	var publisherRuntime *bkntrace.EvidencePublisherRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED")), "true") {
		publisherRuntime, err = bkntrace.NewEvidencePublisherRuntime()
		if err != nil {
			logger.Fatalf("Failed to configure Evidence Kafka publisher: %v", err)
		}
		bkntrace.SetEvidencePublisher(publisherRuntime.Publisher)
	} else {
		logger.Warn("BKN Trace Evidence Kafka publisher is disabled; workload is not 0.2-ready and evidence events will be dropped")
	}

	audit.Init(&appSetting.MQSetting)

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
	logics.SetCapabilityBindingAccess(capability_binding.NewCapabilityBindingAccess(appSetting))
	logics.SetMetricAccess(metric.NewMetricAccess(appSetting))
	logics.SetModelFactoryAccess(model_factory.NewModelFactoryAccess(appSetting))
	logics.SetOpenSearchAccess(opensearch.NewOpenSearchAccess(appSetting))
	logics.SetObjectTypeAccess(object_type.NewObjectTypeAccess(appSetting))
	logics.SetRelationTypeAccess(relation_type.NewRelationTypeAccess(appSetting))
	logics.SetRiskTypeAccess(risk_type.NewRiskTypeAccess(appSetting))
	logics.SetVegaBackendAccess(vega_backend.NewVegaBackendAccess(appSetting))

	// Create and start the service.
	server := &mgrService{
		appSetting:     appSetting,
		otelProviders:  otelProviders,
		restHandler:    driveradapters.NewRestHandler(appSetting, operationaudit.NewStore(db, "")),
		conceptSyncer:  worker.NewConceptSyncer(appSetting),
		scheduleWorker: worker.NewScheduleWorker(appSetting),
	}
	if publisherRuntime != nil {
		server.evidencePublisher = publisherRuntime.Publisher
		server.evidenceProducer = publisherRuntime.Producer
	}
	server.start()
}
