// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package app is context-loader's bootstrap, split out of package main so a
// second entry point can reuse it. The community binary (server/main.go) and
// the enterprise binary in openbkn-ee run the same startup code; they differ
// only in what happens between Boot and Run.
//
//	// community
//	a, _ := app.Boot(app.Options{})
//	a.Run()
//
//	// enterprise
//	a, _ := app.Boot(app.Options{})
//	eetools.Setup()   // registers everything it has; what may run is per call
//	a.Run()
//
// Boot installs the licence gate; Run freezes the assembly registry and then
// serves. Everything an extension needs in order to assemble itself exists
// between the two calls, and nothing can register once requests start flowing.
//
// The split is not cosmetic. Before it, mcp.NewMCPHandler() ran inside the
// struct literal in NewRestPublicHandler, which main called directly — the tool
// surface was fixed before any second entry point could get a word in.
//
// Design: bkn-docs docs/shared/licensing/ee-design.md §5.1.
package app

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/bootstrap"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Options configures Boot. The zero value is what the community entry point
// uses; it exists so adding an option later does not break either call site.
type Options struct{}

// App is a booted, not-yet-serving context-loader.
type App struct {
	config *config.Config
	// refresh keeps the licence snapshot current. Nil when this deployment has
	// no licence hub configured, which is every community deployment.
	refresh func(stop <-chan struct{})
	stop    chan struct{}
}

// Server Service
type Server struct {
	// Health check
	httpHealthHandler  interfaces.HTTPRouterInterface
	restPublicHandler  interfaces.HTTPRouterInterface
	restPrivateHandler interfaces.HTTPRouterInterface
	config             *config.Config
}

// Boot brings up configuration and installs the licence gate. It builds no
// handlers and listens on nothing — that is Run's job, and the gap between the
// two is where extensions register.
//
// A deployment with no licence hub gets the deny-everything gate and carries
// on: community deployments never configure one, so this is a normal steady
// state rather than a misconfiguration to fail on.
func Boot(opts Options) (*App, error) {
	cfg := config.NewConfigLoader()
	// Set error code language
	common.SetLang(cfg.Project.Language)

	gate, refresh := entitlement.GateWithRunner()
	entitlement.SetGate(gate)

	return &App{config: cfg, refresh: refresh, stop: make(chan struct{})}, nil
}

// Run freezes the assembly registry, builds the handlers and serves. It does
// not return under normal operation.
//
// Freezing comes first, before any handler is built, because that is when the
// MCP tool catalogue and the route tree are materialised. A tool registered
// afterwards would be invisible to tools/list while still being callable, and
// two callers would disagree about what this server can do.
//
// Freezing lives here rather than in a socket package on purpose: it closes the
// whole registry, so a second socket calling its own Freeze would be the second
// call — and the second call is a bug.
//
// What it does NOT freeze is what may run. That is re-read from the licence on
// every call, so a certificate installed after startup takes effect without a
// restart.
func (a *App) Run() error {
	entitlement.Freeze()
	if caps := entitlement.Assembled(); len(caps) > 0 {
		a.config.Logger.Infof("extensions assembled: %+v", caps)
	}
	if a.refresh != nil {
		go a.refresh(a.stop)
	}

	s := &Server{
		config:             a.config,
		httpHealthHandler:  driveradapters.NewHTTPHealthHandler(),
		restPublicHandler:  driveradapters.NewRestPublicHandler(a.config.Logger),
		restPrivateHandler: driveradapters.NewRestPrivateHandler(a.config.Logger),
	}
	s.config.Logger.Info("start agent-retrieval server")
	if a.config.OTelProviders != nil {
		defer a.config.OTelProviders.Shutdown(context.Background())
	}
	defer s.config.Logger.Info("stop agent-retrieval server")
	s.Start()
	go bootstrap.NewToolDependencySync().Start(context.Background())
	select {}
}

// Start starts the server
func (s *Server) Start() {
	gin.SetMode(gin.ReleaseMode)

	go func() {
		// Register router - health check
		engine := gin.New()
		engine.Use(gin.Recovery())
		engine.UseRawPath = true
		routerHealth := engine.Group("/health")
		s.httpHealthHandler.RegisterRouter(routerHealth)

		// Register internal interface router - operator related interfaces
		routerInternalGroup := engine.Group("/api/agent-retrieval/in/v1")
		routerInternalGroup.Use(gin.Recovery())
		s.restPrivateHandler.RegisterRouter(routerInternalGroup)

		// Register external router
		routerGroup := engine.Group("/api/agent-retrieval/v1")
		routerGroup.Use(gin.Recovery())
		s.restPublicHandler.RegisterRouter(routerGroup)

		url := fmt.Sprintf("%s:%d", s.config.Project.Host, s.config.Project.Port)
		err := engine.Run(url)
		if err != nil {
			s.config.Logger.Errorf("start server failed, error: %v", err)
		}
	}()
}
