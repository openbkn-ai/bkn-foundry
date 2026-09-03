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
	"vega-backend/worker"
)

type mgrService struct {
	appSetting    *common.AppSetting
	otelProviders *otel.Providers
	restHandler   driveradapters.RestHandler
	workerManager *worker.WorkerManager
}

func (server *mgrService) start() {
	logger.Info("Server Starting")

	// Create gin.engine and register the API
	engine := gin.New()

	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

	// server observes process signals and starts graceful shutdown when one arrives.
	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
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

	<-runCtx.Done()

	server.restHandler.SetReady(false)

	// Set the last processing time of the system
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stop the http service
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(ctx); err != nil {
		logger.Errorf("Server Shutdown: %v", err)
	}

	server.workerManager.Stop()

	server.otelProviders.Shutdown(context.Background())

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
		permissionAccess, err := permission.MaybeShadow(permission.NewPermissionAccess(appSetting))
		if err != nil {
			logger.Fatalf("authorization provider is misconfigured: %v", err)
		}
		logics.SetPermissionAccess(permissionAccess)
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
	workerMgr := worker.NewWorkerManager(appSetting)
	if err := workerMgr.Start(context.Background()); err != nil {
		logger.Fatalf("Failed to start background workers: %v", err)
	}
	logger.Info("VEGA Manager Init Background Workers Success")

	// Create and start the service
	server := &mgrService{
		appSetting:    appSetting,
		otelProviders: otelProviders,
		restHandler:   driveradapters.NewRestHandler(appSetting),
		workerManager: workerMgr,
	}
	server.start()
}
