// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package app is agent-operator-integration's bootstrap, split out of package
// main so that startup can be called from another entry point, the way
// bkn-backend, ontology-query, vega-backend and context-loader already expose
// theirs:
//
//	a, err := app.Boot(app.Options{})
//	if err != nil { ... }
//	if err := a.Run(); err != nil { ... }
//
// Boot loads configuration and builds the handlers and services; Run starts
// serving and blocks until the process receives a stop signal, then shuts the
// services down.
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bknaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/capabilityindex"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/mcpinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/skill"
)

// Options configures Boot. It is empty today and exists so later startup
// options do not change Boot's signature.
type Options struct{}

// App is a booted agent-operator-integration server.
type App struct {
	// health check.
	httpHealthHandler     interfaces.HTTPRouterInterface
	restPublicHandler     interfaces.HTTPRouterInterface
	restPrivateHandler    interfaces.HTTPRouterInterface
	config                *config.Config
	skillIndexSyncService interfaces.SkillIndexSyncService
	skillIndexBuildWorker interfaces.App
	// capabilityIndexSync owns the unified capability dataset. It is initialised here rather than
	// lazily on first write, because a write that arrives before the dataset exists is dropped
	// with a warning, and nothing retries it until the reconciler's next pass.
	capabilityIndexSync  interfaces.CapabilityIndexSyncService
	capabilityReconciler capabilityindex.Reconciler
	capabilityCancel     context.CancelFunc
}

// Boot loads the global configuration and builds the handlers and services.
// It does not open a listener.
func Boot(_ Options) (*App, error) {
	// Initialize global configuration.
	cfg := config.NewConfigLoader()
	// Set error code language.
	common.SetLang(cfg.Project.Language)
	return &App{
		config:                cfg,
		httpHealthHandler:     driveradapters.NewHTTPHealthHandler(),
		restPublicHandler:     driveradapters.NewRestPublicHandler(),
		restPrivateHandler:    driveradapters.NewRestPrivateHandler(),
		skillIndexSyncService: skill.NewSkillIndexSyncService(),
		skillIndexBuildWorker: skill.NewSkillIndexBuildWorker(),
		capabilityIndexSync:   capability.NewCapabilityIndexSyncService(),
		capabilityReconciler:  capabilityindex.NewReconciler(),
	}, nil
}

// Run starts serving and blocks until the process receives SIGHUP, SIGINT,
// SIGTERM or SIGQUIT, then stops the services.
func (a *App) Run() error {
	a.config.Logger.Info("start agent-operator-integration server")
	if a.config.OTelProviders != nil {
		defer a.config.OTelProviders.Shutdown(context.Background())
	}
	a.start()
	defer a.stop(context.Background())
	// Wait for semaphore.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT) //nolint
	<-c
	return nil
}

// start starts the service.
func (a *App) start() {
	gin.SetMode(gin.ReleaseMode)
	var err error
	// Initialize skill index synchronization.
	err = a.skillIndexSyncService.EnsureInitialized(context.Background())
	if err != nil {
		a.config.Logger.Errorf("init skill index sync service failed, error: %v", err)
	}
	if a.skillIndexBuildWorker != nil {
		go func() {
			if workerErr := a.skillIndexBuildWorker.Start(); workerErr != nil {
				a.config.Logger.Errorf("start skill index build worker failed, error: %v", workerErr)
			}
		}()
	}
	// Initialize the unified capability index. A failure here is logged and retried in the
	// background: the dataset not existing yet must not stop the service from serving.
	if err = a.capabilityIndexSync.EnsureInitialized(context.Background()); err != nil {
		a.config.Logger.Errorf("init capability index sync service failed, error: %v", err)
	}
	a.startCapabilityReconciler()

	// Register Route - Health Check.
	go func() {
		engine := gin.New()
		engine.Use(gin.Recovery())
		engine.UseRawPath = true
		routerHealth := engine.Group("/health")
		a.httpHealthHandler.RegisterRouter(routerHealth)

		// Register internal interface routing.
		routerInternalGroup := engine.Group("/api/agent-operator-integration/internal-v1")
		routerInternalGroup.Use(gin.Recovery())
		a.restPrivateHandler.RegisterRouter(routerInternalGroup)

		// Register external routes.
		routerGroup := engine.Group("/api/agent-operator-integration/v1")
		routerGroup.Use(gin.Recovery())
		a.restPublicHandler.RegisterRouter(routerGroup)

		// Register capability plane routing - the original capabilities-lab independent service, after the merger, it became one of this service.
		// routing group. The path remains unchanged, and the consumer only needs to change the host of the base URL.
		routerLabGroup := engine.Group("/api/capabilities-lab/v1")
		routerLabGroup.Use(gin.Recovery())
		capabilitieslab.RegisterRouter(routerLabGroup)

		url := fmt.Sprintf("%s:%d", a.config.Project.Host, a.config.Project.Port)
		err := engine.Run(url)
		if err != nil {
			a.config.Logger.Errorf("start server failed, error: %v", err)
		}
	}()
}

// startCapabilityReconciler starts the periodic reconcile of Function tools and MCP tools.
func (a *App) startCapabilityReconciler() {
	ctx, cancel := context.WithCancel(context.Background())
	a.capabilityCancel = cancel
	if a.config.CapabilityIndexConfig.DisableReconciler || a.capabilityReconciler == nil {
		// Disabling periodic scans does not disable indexing committed Skill writes.
		skill.StartCapabilityReconciler(ctx, 0)
		a.config.Logger.Info("capability index reconciler is disabled")
		return
	}
	interval, err := time.ParseDuration(a.config.CapabilityIndexConfig.ReconcileInterval)
	if err != nil || interval <= 0 {
		a.config.Logger.Warnf("invalid capability index reconcile interval %q, falling back to 30m: %v",
			a.config.CapabilityIndexConfig.ReconcileInterval, err)
		interval = 30 * time.Minute
	}
	a.capabilityReconciler.Start(ctx, interval)
	// The other two halves run from their own packages: MCP next to the client that can list a
	// remote server, Skills next to the rule that decides which snapshot of a Skill counts.
	mcp.StartCapabilityReconciler(ctx, interval)
	skill.StartCapabilityReconciler(ctx, interval)
	a.config.Logger.Infof("capability index reconciler started, interval=%s", interval)
}

// stop stops the service.
func (a *App) stop(ctx context.Context) {
	a.config.Logger.Info("stop agent-operator-integration server")
	// sandbox.Close() // Close and destroy the sandbox session pool.
	if err := bkntrace.CloseEvidencePublisher(ctx); err != nil {
		a.config.Logger.Errorf("BKN Trace Evidence publisher shutdown acknowledgement failed: %v", err)
	}
	bknaudit.ClosePublisher(ctx)
	if a.capabilityCancel != nil {
		a.capabilityCancel()
	}
	if a.skillIndexBuildWorker != nil {
		a.skillIndexBuildWorker.Stop(ctx)
	}
	mcpinstance.Close() // Shut down the instance pool.
}
