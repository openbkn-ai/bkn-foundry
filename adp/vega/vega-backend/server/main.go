// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"context"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "unicode/utf8"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	_ "go.uber.org/automaxprocs"

	"vega-backend/common"
	"vega-backend/drivenadapters/auth"
	"vega-backend/drivenadapters/bkn_agent"
	"vega-backend/drivenadapters/build_task"
	"vega-backend/drivenadapters/catalog"
	"vega-backend/drivenadapters/catalog_health_check_schedule"
	"vega-backend/drivenadapters/connector_type"
	"vega-backend/drivenadapters/discover_schedule"
	"vega-backend/drivenadapters/discover_task"
	"vega-backend/drivenadapters/kafka"
	"vega-backend/drivenadapters/model_factory"
	"vega-backend/drivenadapters/permission"
	"vega-backend/drivenadapters/resource"
	"vega-backend/drivenadapters/semantic_understanding_task"
	"vega-backend/drivenadapters/user_mgmt"
	"vega-backend/driveradapters"
	"vega-backend/logics"
	"vega-backend/logics/connector/factory"
	logicsDiscoverSchedule "vega-backend/logics/discover_schedule"
	logicsDiscoverTask "vega-backend/logics/discover_task"
	"vega-backend/worker"
)

type mgrService struct {
	appSetting    *common.AppSetting
	otelProviders *otel.Providers
	restHandler   driveradapters.RestHandler
}

func (server *mgrService) start() {
	logger.Info("Server Starting")

	// Create gin.engine and register the API
	engine := gin.New()

	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

	// Listen for interrupt signals (SIGINT, SIGTERM)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// When a signal is received, the "Done" of ctx will be automatically triggered. This "stop" means no longer capturing the registered signal, which can be regarded as a form of resource release.
	defer stop()

	// Initialize the http service
	s := &http.Server{
		Addr:           ":" + strconv.Itoa(server.appSetting.ServerSetting.HttpPort),
		Handler:        engine,
		ReadTimeout:    server.appSetting.ServerSetting.ReadTimeOut * time.Second,
		WriteTimeout:   server.appSetting.ServerSetting.WriteTimeout * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Start the http service
	go func() {
		err := s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("s.ListenAndServe err:%v", err)
		}
	}()

	logger.Infof("Server Started on Port:%d", server.appSetting.ServerSetting.HttpPort)

	<-ctx.Done()

	// Set the last processing time of the system
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stop the http service
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatalf("Server Shutdown:%v", err)
	}

	server.otelProviders.Shutdown(ctx)

	logger.Info("Server Exited")
}

func main() {
	logger.Info("Server Initializing")

	// Initialize the service configuration
	appSetting := common.NewSetting()
	logger.Info("Server Init Setting Success")

	// Set the error code language
	rest.SetLang(appSetting.ServerSetting.Language)
	logger.Info("Server Set Language Success")

	// Set the running mode of gin
	gin.SetMode(appSetting.ServerSetting.RunMode)
	logger.Infof("Server RunMode: %s", appSetting.ServerSetting.RunMode)

	logger.Infof("Server Start By Port:%d", appSetting.ServerSetting.HttpPort)

	otelProviders, err := otel.InitOTel(context.Background(), &appSetting.OtelSetting)
	if err != nil {
		logger.Fatalf("Failed to initialize OpenTelemetry provider: %v", err)
	}

	// Initialize the database connection
	db := libdb.NewDB(&appSetting.DBSetting)
	logics.SetDB(db)

	// The Set order is sorted in ascending alphabetical order
	if common.GetAuthEnabled() {
		logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
		logics.SetPermissionAccess(permission.MaybeShadow(permission.NewPermissionAccess(appSetting)))
		logics.SetUserMgmtAccess(user_mgmt.NewUserMgmtAccess(appSetting))
	}

	logics.SetBuildTaskAccess(build_task.NewBuildTaskAccess(appSetting))
	logics.SetCatalogAccess(catalog.NewCatalogAccess(appSetting))
	logics.SetCatalogHealthCheckScheduleAccess(catalog_health_check_schedule.NewCatalogHealthCheckScheduleAccess(appSetting))
	logics.SetConnectorTypeAccess(connector_type.NewConnectorTypeAccess(appSetting))
	logics.SetDiscoverScheduleAccess(discover_schedule.NewDiscoverScheduleAccess(appSetting))
	logics.SetDiscoverTaskAccess(discover_task.NewDiscoverTaskAccess(appSetting))
	logics.SetKafkaAccess(kafka.NewKafkaAccess(appSetting))
	logics.SetModelFactoryAccess(model_factory.NewModelFactoryAccess(appSetting))
	logics.SetResourceAccess(resource.NewResourceAccess(appSetting))
	logics.SetBknAgentAccess(bkn_agent.NewBknAgentAccess(appSetting))
	logics.SetSemanticUnderstandingTaskAccess(semantic_understanding_task.NewSemanticUnderstandingTaskAccess(appSetting))

	// Initialize the Connector Factory and register the built-in Local Connector Builder
	factory.GetFactory(appSetting)
	logger.Info("VEGA Manager Init Connector Factory Success")

	// Initialize and start a unified TaskWorkermanager to handle all types of tasks
	taskWorkerMgr := worker.NewTaskWorkerManager(appSetting)
	if err := taskWorkerMgr.Start(context.Background()); err != nil {
		logger.Fatalf("Failed to start task workers: %v", err)
	}
	logger.Info("VEGA Manager Init Task Worker Success")

	// Initialize and start the scheduler
	dts := logicsDiscoverTask.NewDiscoverTaskService(appSetting)
	dss := logicsDiscoverSchedule.NewDiscoverScheduleService(appSetting, dts)
	dsw := worker.NewDiscoverScheduleWorker(appSetting, dss)
	if err := dsw.Start(); err != nil {
		logger.Fatalf("Failed to start scheduler: %v", err)
	}
	logger.Info("VEGA Manager Init Scheduler Success")

	chcw := worker.NewCatalogHealthCheckWorker(appSetting)
	if err := chcw.Start(); err != nil {
		logger.Fatalf("Failed to start catalog health check worker: %v", err)
	}
	logger.Info("VEGA Manager Init Catalog Health Check Worker Success")

	// Create and start the service
	server := &mgrService{
		appSetting:    appSetting,
		otelProviders: otelProviders,
		restHandler:   driveradapters.NewRestHandler(appSetting),
	}
	server.start()
}
