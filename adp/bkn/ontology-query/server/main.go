// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"context"
	"database/sql"
	"net/http"

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

	"ontology-query/common"
	"ontology-query/common/bkntrace"
	"ontology-query/common/bkntrace/outbox"
	"ontology-query/drivenadapters/agent_operator"
	"ontology-query/drivenadapters/auth"
	"ontology-query/drivenadapters/model_factory"
	"ontology-query/drivenadapters/ontology_manager"
	"ontology-query/drivenadapters/opensearch"
	"ontology-query/drivenadapters/vega_backend"
	"ontology-query/driveradapters"
	"ontology-query/logics"
)

type mgrService struct {
	appSetting    *common.AppSetting
	otelProviders *otel.Providers
	restHandler   driveradapters.RestHandler
	traceOutbox   *outbox.Worker
	outboxDB      *sql.DB
}

func (server *mgrService) start() {
	logger.Info("Server Starting")

	// Create the Gin engine and register APIs.
	engine := gin.New()

	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")
	if server.traceOutbox != nil {
		server.traceOutbox.Start()
	}

	// Listen for interrupt signals (SIGINT and SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// Receiving a signal triggers ctx.Done. stop stops receiving registered signals and releases those resources.
	defer stop()

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

	// Stop the HTTP service.
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatalf("Server Shutdown:%v", err)
	}
	if server.traceOutbox != nil {
		if err := server.traceOutbox.Stop(ctx); err != nil {
			logger.Warnf("BKN Trace outbox shutdown: %v", err)
		}
	}
	if server.outboxDB != nil {
		_ = server.outboxDB.Close()
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
	var traceOutbox *outbox.Worker
	var outboxDB *sql.DB
	bkntrace.WarnIfLegacyEvidenceMisconfigured()
	if bkntrace.ProducerOutboxEnabled() {
		outboxDB, err = bkntrace.OpenProducerOutboxDB(appSetting.DBSetting)
		if err != nil {
			logger.Fatalf("Failed to open BKN Trace producer outbox database: %v", err)
		}
		traceOutbox, err = bkntrace.ConfigureProducerOutbox(outboxDB)
		if err != nil {
			logger.Fatalf("Failed to configure BKN Trace producer outbox: %v", err)
		}
	}
	if bkntrace.ProducerOutboxCleanupOnly() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := bkntrace.CleanupProducerOutbox(ctx)
		if err != nil {
			logger.Fatalf("Failed to clean BKN Trace producer outbox: %v", err)
		}
		logger.Infof("BKN Trace producer outbox cleanup complete: delivered=%d abandoned=%d audits=%d", result.Delivered, result.Abandoned, result.Audits)
		if outboxDB != nil {
			_ = outboxDB.Close()
		}
		otelProviders.Shutdown(ctx)
		return
	}

	// Sort Set entries in ascending alphabetical order.
	if common.GetAuthEnabled() {
		logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
	}
	logics.SetAgentOperatorAccess(agent_operator.NewAgentOperatorAccess(appSetting))
	logics.SetModelFactoryAccess(model_factory.NewModelFactoryAccess(appSetting))
	logics.SetOntologyManagerAccess(ontology_manager.NewOntologyManagerAccess(appSetting))
	logics.SetOpenSearchAccess(opensearch.NewOpenSearchAccess(appSetting))
	logics.SetVegaBackendAccess(vega_backend.NewVegaBackendAccess(appSetting))

	server := &mgrService{
		appSetting:    appSetting,
		otelProviders: otelProviders,
		restHandler:   driveradapters.NewRestHandler(appSetting),
		traceOutbox:   traceOutbox,
		outboxDB:      outboxDB,
	}
	server.start()
}
