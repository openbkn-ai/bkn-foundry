// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package app exposes Vega startup as an explicit assembly lifecycle.
package app

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "unicode/utf8"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/bkn_agent"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/build_task"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/catalog"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/catalog_health_check_schedule"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/connector_type"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/discover_schedule"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/discover_task"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/kafka"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/model_factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/permission"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/resource"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/semantic_understanding_task"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/drivenadapters/user_mgmt"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/driveradapters"
	extensionconnector "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/extension/connector"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/worker"
)

// Options configures Boot. The zero value is the community entry point.
type Options struct{}

// App is booted but not yet serving. Enterprise code registers connectors
// between Boot and Run.
type App struct {
	appSetting    *common.AppSetting
	otelProviders *otel.Providers
	restHandler   driveradapters.RestHandler
	workerManager *worker.WorkerManager
	refresh       func(stop <-chan struct{})
	stop          chan struct{}
}

func (server *App) start() error {
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
		ReadTimeout:    time.Duration(server.appSetting.ServerSetting.ReadTimeOut) * time.Second,
		WriteTimeout:   time.Duration(server.appSetting.ServerSetting.WriteTimeout) * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Start the http service
	go func() {
		err := s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logger.Errorf("s.ListenAndServe err:%v", err)
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
	close(server.stop)

	server.otelProviders.Shutdown(context.Background())

	logger.Info("Server Exited")
	return nil
}

// Boot initializes all core dependencies but does not materialize the
// connector factory, start workers, or listen for HTTP requests.
func Boot(_ Options) (*App, error) {
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
		return nil, fmt.Errorf("initialize OpenTelemetry provider: %w", err)
	}

	// Initialize the database connection
	db := libdb.NewDB(&appSetting.DBSetting)
	logics.SetDB(db)

	// The Set order is sorted in ascending alphabetical order
	logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
	logics.SetPermissionAccess(permission.NewPermissionAccess(appSetting))
	logics.SetUserMgmtAccess(user_mgmt.NewUserMgmtAccess(appSetting))
	logics.SetProxyAuthorizationAccess(permission.NewProxyAuthorizationAccess())

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

	gate, refresh := entitlement.GateWithRunner()
	entitlement.SetGate(gate)

	return &App{
		appSetting:    appSetting,
		otelProviders: otelProviders,
		refresh:       refresh,
		stop:          make(chan struct{}),
	}, nil
}

// Run freezes extension assembly, materializes the connector factory, then
// starts workers and the HTTP service.
func (server *App) Run() error {
	extensionconnector.Freeze()
	if server.refresh != nil {
		go server.refresh(server.stop)
	}

	factory.GetFactory(server.appSetting)
	logger.Info("VEGA Manager Init Connector Factory Success")

	server.workerManager = worker.NewWorkerManager(server.appSetting)
	if err := server.workerManager.Start(context.Background()); err != nil {
		return fmt.Errorf("start background workers: %w", err)
	}
	logger.Info("VEGA Manager Init Background Workers Success")

	server.restHandler = driveradapters.NewRestHandler(server.appSetting)
	return server.start()
}
