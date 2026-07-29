// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package app is context-loader's bootstrap, split out of package main so that
// a second entry point can reuse it. The community binary (server/main.go) and
// the enterprise binary in openbkn-ee run the same startup code; they differ
// only in what happens between Boot and Run.
//
//	// community
//	a, _ := app.Boot(app.Options{})
//	a.Run()
//
//	// enterprise
//	a, _ := app.Boot(app.Options{})
//	ctxprobe.Setup()   // checks its own license, registers only if it passes
//	a.Run()
//
// Boot installs the license gate; Run freezes the extension registry and then
// serves. Everything an extension needs in order to assemble itself therefore
// exists between the two calls, and nothing can register once requests start
// flowing.
//
// The split is not cosmetic. Before it, mcp.NewMCPHandler() ran inside the
// struct literal in NewRestPublicHandler, which main called directly — the tool
// surface was fixed before any second entry point could get a word in.
//
// Design: bkn-docs shared/licensing/context-loader-ee-socket.md §7.
package app

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/bootstrap"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Options configures Boot. The zero value is what the community entry point
// uses; it exists so that adding an option later does not break either caller's
// call site.
type Options struct{}

// App is a booted, not-yet-serving context-loader.
type App struct {
	config *config.Config
}

// Server Service
type Server struct {
	// Health check
	httpHealthHandler  interfaces.HTTPRouterInterface
	restPublicHandler  interfaces.HTTPRouterInterface
	restPrivateHandler interfaces.HTTPRouterInterface
	config             *config.Config
}

// Boot brings up configuration and installs the license gate. It builds no
// handlers and listens on nothing — that is Run's job, and the gap between the
// two is where extensions register.
func Boot(opts Options) (*App, error) {
	cfg := config.NewConfigLoader()
	// Set error code language
	common.SetLang(cfg.Project.Language)
	// The gate has to be in place before any extension checks its own license.
	// A release build gets a gate that denies everything paid until the license
	// client lands; -tags ee_dev gets the environment stub.
	extension.SetGate(extension.DefaultGate())
	return &App{config: cfg}, nil
}

// Run freezes the extension registry, builds the handlers and serves. It does
// not return under normal operation.
//
// Freezing has to come before the handlers are built, because that is when the
// MCP tool surface is fixed: a tool registered afterwards would be invisible to
// tools/list while still being callable, and two callers would disagree about
// what this server can do.
//
// Freezing lives here rather than in a socket package on purpose. It closes the
// whole extension registry, so a second socket calling its own Freeze would
// panic on the second call — the assembler freezes, once.
func (a *App) Run() error {
	extension.Freeze()
	if assembled := extension.Assembly(); len(assembled) > 0 {
		a.config.Logger.Infof("extensions assembled: %v", assembled)
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
