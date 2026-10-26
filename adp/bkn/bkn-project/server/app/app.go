// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package app owns the bkn-project process lifecycle: Boot wires shared
// infrastructure, Run serves HTTP until a shutdown signal arrives.
package app

import (
	"context"
	"database/sql"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/bknbackend"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/executionfactory"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/gitstore"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/release"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// Application is a booted bkn-project process that has not started serving yet.
type Application struct {
	appSetting    *common.AppSetting
	db            *sql.DB
	otelProviders *otel.Providers
	restHandler   driveradapters.RestHandler
}

// Boot initializes shared infrastructure without opening the HTTP listener.
func Boot() *Application {
	logger.Info("Server Initializing")

	// Initialize service configuration.
	appSetting := common.NewSetting()
	logger.Info("Server Init Setting Success")

	// Configure error-code locales.
	i18n.SetLang(appSetting.ServerSetting.Language)
	logger.Info("Server Set Language Success")

	// Configure Gin run mode.
	gin.SetMode(appSetting.ServerSetting.RunMode)
	logger.Infof("Server RunMode: %s", appSetting.ServerSetting.RunMode)

	otelProviders, err := otel.InitOTel(context.Background(), &appSetting.OtelSetting)
	if err != nil {
		logger.Fatalf("Failed to initialize OpenTelemetry provider: %v", err)
	}

	// Initialize the database connection.
	db := libdb.NewDB(&appSetting.DBSetting)
	logics.SetDB(db)
	logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
	logics.SetReleaseAccess(release.NewReleaseAccess(appSetting))
	logics.SetGitStore(gitstore.NewGitStore(appSetting))
	logics.SetBknBackendAccess(bknbackend.NewBknBackendAccess(appSetting))
	logics.SetExecutionFactoryAccess(executionfactory.NewExecutionFactoryAccess(appSetting))
	logger.Infof("Git store root: %s", appSetting.GitStoreSetting.RootDir)

	return &Application{
		appSetting:    appSetting,
		db:            db,
		otelProviders: otelProviders,
		restHandler:   driveradapters.NewRestHandler(appSetting, db),
	}
}

// Run serves until shutdown.
func (server *Application) Run() {
	logger.Info("Server Starting")

	// Create the Gin engine and register APIs.
	engine := gin.New()
	server.restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

	// Listen for interrupt signals (SIGINT and SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stop the HTTP service.
	logger.Info("Server Start Shutdown")
	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Fatalf("Server Shutdown:%v", err)
	}
	if err := server.db.Close(); err != nil {
		logger.Warnf("Database close failed: %v", err)
	}
	server.otelProviders.Shutdown(shutdownCtx)

	logger.Info("Server Exited")
}
