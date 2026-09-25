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
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"ontology-query/common"
	"ontology-query/common/bkntrace"
	"ontology-query/drivenadapters/agent_operator"
	"ontology-query/drivenadapters/auth"
	knproxy "ontology-query/drivenadapters/kn_proxy"
	"ontology-query/drivenadapters/model_factory"
	"ontology-query/drivenadapters/ontology_manager"
	"ontology-query/drivenadapters/opensearch"
	"ontology-query/drivenadapters/vega_backend"
	"ontology-query/driveradapters"
	"ontology-query/logics"
	"ontology-query/logics/action_logs"
	proxycontext "ontology-query/logics/proxy_context"
)

type mgrService struct {
	appSetting       *common.AppSetting
	otelProviders    *otel.Providers
	restHandler      driveradapters.RestHandler
	evidenceRuntime  *evidencepublisher.PublisherRuntime
	evidenceProducer interface{ Close() error }
}

func (server *mgrService) start() {
	logger.Info("Server Starting")

	// Create the Gin engine and register APIs.
	engine := gin.New()

	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

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
	if server.evidenceRuntime != nil {
		if _, err := server.evidenceRuntime.Close(ctx); err != nil {
			logger.Warnf("Evidence publisher runtime close failed: %v", err)
		}
	}
	if server.evidenceProducer != nil {
		if err := server.evidenceProducer.Close(); err != nil {
			logger.Warnf("Evidence Kafka producer close failed: %v", err)
		}
	}
	server.otelProviders.Shutdown(ctx)

	logger.Info("Server Exited")
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
	if action_logs.RetentionCleanupOnly() {
		runActionLogRetention(appSetting)
		otelProviders.Shutdown(context.Background())
		return
	}

	logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
	logics.SetAgentOperatorAccess(agent_operator.NewAgentOperatorAccess(appSetting))
	logics.SetModelFactoryAccess(model_factory.NewModelFactoryAccess(appSetting))
	logics.SetOntologyManagerAccess(ontology_manager.NewOntologyManagerAccess(appSetting))
	logics.SetOpenSearchAccess(opensearch.NewOpenSearchAccess(appSetting))
	logics.SetVegaBackendAccess(vega_backend.NewVegaBackendAccess(appSetting))
	logics.SetProxyContextResolver(proxycontext.NewProxyContextResolver(knproxy.NewKnowledgeNetworkProxyAccess(appSetting)))

	server := &mgrService{
		appSetting:    appSetting,
		otelProviders: otelProviders,
		restHandler:   driveradapters.NewRestHandler(appSetting),
	}
	if publisherRuntime != nil {
		server.evidenceRuntime = publisherRuntime.Runtime
		server.evidenceProducer = publisherRuntime.Producer
	}
	server.start()
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

// runActionLogRetention runs one retention cleanup of action execution logs, as the retention
// CronJob does, and exits the process on failure so the Job is retried.
func runActionLogRetention(appSetting *common.AppSetting) {
	cfg, err := action_logs.LoadRetentionConfig()
	if err != nil {
		logger.Fatalf("Invalid action execution log retention configuration: %v", err)
	}
	if cfg.RetentionDays == 0 {
		logger.Info("Action execution log retention is disabled: ACTION_EXECUTION_LOG_RETENTION_DAYS is 0")
		return
	}

	logics.SetOpenSearchAccess(opensearch.NewOpenSearchAccess(appSetting))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	started := time.Now()
	result, err := action_logs.CleanupExpiredExecutions(ctx, appSetting, cfg)
	if err != nil {
		deletedExecutions, deletedResults := int64(0), int64(0)
		if result != nil {
			deletedExecutions, deletedResults = result.Executions, result.Results
		}
		logger.Fatalf("Action execution log retention failed after executions=%d results=%d: %v",
			deletedExecutions, deletedResults, err)
	}
	logger.Infof("Action execution log retention complete: dry_run=%t retention_days=%d cutoff=%s executions=%d results=%d batches=%d more_remaining=%t took=%s",
		cfg.DryRun, cfg.RetentionDays, time.UnixMilli(result.Cutoff).UTC().Format(time.RFC3339),
		result.Executions, result.Results, result.Batches, result.Truncated, time.Since(started).Round(time.Millisecond))
	if result.Truncated {
		logger.Warnf("Action execution log retention stopped at its per-run limit of %d executions (batch size %d x %d batches) "+
			"with more expired executions left; if this repeats, raise the batch count or run the CronJob more often",
			cfg.BatchSize*cfg.MaxBatches, cfg.BatchSize, cfg.MaxBatches)
	}
}
