// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/bootstrap"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"

	"github.com/gin-gonic/gin"
)

// Server Service
type Server struct {
	// Health check
	httpHealthHandler  interfaces.HTTPRouterInterface
	restPublicHandler  interfaces.HTTPRouterInterface
	restPrivateHandler interfaces.HTTPRouterInterface
	config             *config.Config
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

func main() {
	// Initialize global configuration
	config := config.NewConfigLoader()
	// Set error code language
	common.SetLang(config.Project.Language)
	s := &Server{
		config:             config,
		httpHealthHandler:  driveradapters.NewHTTPHealthHandler(),
		restPublicHandler:  driveradapters.NewRestPublicHandler(config.Logger),
		restPrivateHandler: driveradapters.NewRestPrivateHandler(config.Logger),
	}
	s.config.Logger.Info("start agent-retrieval server")
	if config.OTelProviders != nil {
		defer config.OTelProviders.Shutdown(context.Background())
	}
	defer s.config.Logger.Info("stop agent-retrieval server")
	s.Start()
	go bootstrap.NewToolDependencySync().Start(context.Background())
	select {}
}
