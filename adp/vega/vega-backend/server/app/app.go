// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package app is vega-backend's startup, split so that a build can insert
// capability assembly between "the service is wired" and "the service is
// serving".
//
// Two entry points share this package: the community command in server/main.go,
// and the enterprise command in the openbkn-ee repository. They run the same
// code and differ only in what happens between Boot and Run:
//
//	a, err := app.Boot(app.Options{})   // 1. wire the service, install the gate
//	vegaconn.Setup()                    // 2. enterprise only: register paid connectors
//	err = a.Run()                       // 3. freeze the registry, then serve
//
// The order is a contract, not a convention. Boot has to install the licence
// gate before anything registers, because a socket entry declares a minimum
// tier and the registry rejects one it cannot rank. Run has to freeze before it
// builds anything from the registry, because the connector catalogue is
// materialised once and a late registration would make the catalogue depend on
// timing.
//
// Assembly is explicit rather than init() magic on purpose: what a build
// contains should be readable in one file, and this ordering is a thing init()
// cannot state.
package app

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-comm-go/db"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/asynq"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/bkn_agent"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/build_task"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/catalog"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/catalog_health_check_schedule"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/connector_type"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/discover_schedule"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/discover_task"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/kafka"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/model_factory"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/permission"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/resource"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/semantic_understanding_task"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/drivenadapters/user_mgmt"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/connector/factory"
	logicsDiscoverSchedule "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/discover_schedule"
	logicsDiscoverTask "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/logics/discover_task"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/worker"
)

// Options is reserved for the differences an entry point may need to express.
// It is empty today; the parameter exists so adding one later is not a
// signature change in two repositories at once.
type Options struct{}

// App is a booted, not yet serving, vega-backend.
type App struct {
	appSetting    *common.AppSetting
	otelProviders *otel.Providers

	// gateStop ends the licence refresher. Closed during shutdown; nil in a
	// deployment with no licence hub configured, which is what a community
	// install looks like.
	gateStop chan struct{}
}

// Boot wires the service and installs the licence gate. It does not start
// workers, does not build the connector catalogue and does not listen.
//
// The licence gate goes in here, before any capability registers, and it is
// deliberately the only licence-related thing an entry point has to think
// about: configuration only ever says where to fetch the certificate, never
// which capabilities are on. The moment a deployment can switch a capability on
// by itself, every gate in the product is decorative.
//
// A deployment with no hub configured is not an error. It is what a community
// install is, and the process runs with community behaviour.
func Boot(_ Options) (*App, error) {
	logger.Info("Server Initializing")

	appSetting := common.NewSetting()
	logger.Info("Server Init Setting Success")

	rest.SetLang(appSetting.ServerSetting.Language)
	logger.Info("Server Set Language Success")

	gin.SetMode(appSetting.ServerSetting.RunMode)
	logger.Infof("Server RunMode: %s", appSetting.ServerSetting.RunMode)

	otelProviders, err := otel.InitOTel(context.Background(), &appSetting.OtelSetting)
	if err != nil {
		return nil, fmt.Errorf("initialize OpenTelemetry provider: %w", err)
	}

	db := libdb.NewDB(&appSetting.DBSetting)
	logics.SetDB(db)

	// Set 顺序按字母升序排序
	if common.GetAuthEnabled() {
		logics.SetAuthAccess(auth.NewHydraAuthAccess(appSetting))
		logics.SetPermissionAccess(permission.MaybeShadow(permission.NewPermissionAccess(appSetting)))
		logics.SetUserMgmtAccess(user_mgmt.NewUserMgmtAccess(appSetting))
	}

	logics.SetAsynqAccess(asynq.NewAsynqAccess(appSetting))
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

	a := &App{appSetting: appSetting, otelProviders: otelProviders}
	a.installLicenceGate()

	return a, nil
}

// installLicenceGate resolves the certificate source and starts the refresher.
//
// The refresher matters as much as the first fetch: without it the process
// never learns about a certificate installed later, nor about one that expired,
// and the answer to "may this run" would be frozen at boot. Deciding per call
// is what makes both directions — a certificate arriving, a certificate lapsing
// — take effect without a restart.
func (a *App) installLicenceGate() {
	g, run := entitlement.GateWithRunner()
	entitlement.SetGate(g)
	if run == nil {
		// No hub configured. Community deployments never configure one, so this
		// is a normal steady state and comm-go has already logged whatever is
		// worth logging about half-configured ones.
		return
	}
	a.gateStop = make(chan struct{})
	go run(a.gateStop)
	logger.Info("Server Licence Gate Started")
}

// AppSetting exposes the loaded configuration to an entry point that assembles
// paid capabilities between Boot and Run.
func (a *App) AppSetting() *common.AppSetting { return a.appSetting }

// Run freezes the extension registry, builds everything that reads it, and
// serves until the process is signalled.
//
// Freeze comes first and is not optional. The connector catalogue is
// materialised from the registry during startup; a registration that landed
// afterwards would make the catalogue depend on goroutine timing — the
// "connector list has four entries on one boot and five on the next" class of
// bug, which is miserable to chase and trivial to prevent.
func (a *App) Run() error {
	entitlement.Freeze()

	// Init installs the built-in connectors, merges in whatever the extension
	// socket holds, and reconciles both against the catalogue rows in the
	// database. It must run after Freeze so that set is final.
	factory.Init(a.appSetting)
	logger.Info("VEGA Manager Init Connector Factory Success")

	taskWorkerMgr := worker.NewTaskWorkerManager(a.appSetting)
	taskWorkerMgr.Start()
	logger.Info("VEGA Manager Init Task Worker Success")

	dts := logicsDiscoverTask.NewDiscoverTaskService(a.appSetting)
	dss := logicsDiscoverSchedule.NewDiscoverScheduleService(a.appSetting, dts)
	sw := worker.NewScheduleWorker(a.appSetting, dss)
	if err := sw.Start(); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	logger.Info("VEGA Manager Init Scheduler Success")
	defer sw.Stop()

	chcw := worker.NewCatalogHealthCheckWorker(a.appSetting)
	if err := chcw.Start(); err != nil {
		return fmt.Errorf("start catalog health check worker: %w", err)
	}
	logger.Info("VEGA Manager Init Catalog Health Check Worker Success")

	return a.serve(driveradapters.NewRestHandler(a.appSetting, sw))
}

// serve runs the HTTP server until SIGINT or SIGTERM, then shuts everything
// down within the grace period.
func (a *App) serve(restHandler driveradapters.RestHandler) error {
	logger.Info("Server Starting")

	engine := gin.New()
	restHandler.RegisterPublic(engine)
	logger.Info("Server Register API Success")

	// 监听中断信号（SIGINT、SIGTERM）
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// 在收到信号的时候，会自动触发 ctx 的 Done，这个 stop 是不再捕获注册的信号的意思，算是一种释放资源。
	defer stop()

	s := &http.Server{
		Addr:           ":" + strconv.Itoa(a.appSetting.ServerSetting.HttpPort),
		Handler:        engine,
		ReadTimeout:    a.appSetting.ServerSetting.ReadTimeOut * time.Second,
		WriteTimeout:   a.appSetting.ServerSetting.WriteTimeout * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	logger.Infof("Server Started on Port:%d", a.appSetting.ServerSetting.HttpPort)

	select {
	case err := <-serveErr:
		// Listen failed, typically a port already in use. Returning it lets the
		// entry point exit non-zero; the previous code called logger.Fatalf
		// from inside the goroutine, which skipped every deferred shutdown.
		if err != nil {
			return fmt.Errorf("http serve: %w", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger.Info("Server Start Shutdown")
	if a.gateStop != nil {
		close(a.gateStop)
	}
	if err := s.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	a.otelProviders.Shutdown(shutdownCtx)

	logger.Info("Server Exited")
	return nil
}
