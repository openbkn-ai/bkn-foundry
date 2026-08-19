package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	logicscommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/mcpinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/skill"
)

// Server service.
type Server struct {
	// health check.
	httpHealthHandler     interfaces.HTTPRouterInterface
	restPublicHandler     interfaces.HTTPRouterInterface
	restPrivateHandler    interfaces.HTTPRouterInterface
	MQHandler             interfaces.MQHandler
	outboxMessageEvent    interfaces.App
	config                *config.Config
	skillIndexSyncService interfaces.SkillIndexSyncService
	skillIndexBuildWorker interfaces.App
}

// Start Start the service.
func (s *Server) Start() {
	gin.SetMode(gin.ReleaseMode)
	err := s.outboxMessageEvent.Start()
	if err != nil {
		s.config.Logger.Errorf("start outbox message event failed, error: %v", err)
		panic(err)
	}
	// Initialize skill index synchronization.
	err = s.skillIndexSyncService.EnsureInitialized(context.Background())
	if err != nil {
		s.config.Logger.Errorf("init skill index sync service failed, error: %v", err)
	}
	if s.skillIndexBuildWorker != nil {
		go func() {
			if workerErr := s.skillIndexBuildWorker.Start(); workerErr != nil {
				s.config.Logger.Errorf("start skill index build worker failed, error: %v", workerErr)
			}
		}()
	}

	// Register Route - Health Check.
	go func() {
		engine := gin.New()
		engine.Use(gin.Recovery())
		engine.UseRawPath = true
		routerHealth := engine.Group("/health")
		s.httpHealthHandler.RegisterRouter(routerHealth)

		// Register internal interface routing - operator-related interfaces.
		routerInternalGroup := engine.Group("/api/agent-operator-integration/internal-v1")
		routerInternalGroup.Use(gin.Recovery())
		s.restPrivateHandler.RegisterRouter(routerInternalGroup)

		// Register external routes - operator related interfaces.
		routerGroup := engine.Group("/api/agent-operator-integration/v1")
		routerGroup.Use(gin.Recovery())
		s.restPublicHandler.RegisterRouter(routerGroup)

		// Register capability plane routing - the original capabilities-lab independent service, after the merger, it became one of this service.
		// routing group. The path remains unchanged, and the consumer only needs to change the host of the base URL.
		routerLabGroup := engine.Group("/api/capabilities-lab/v1")
		routerLabGroup.Use(gin.Recovery())
		capabilitieslab.RegisterRouter(routerLabGroup)

		url := fmt.Sprintf("%s:%d", s.config.Project.Host, s.config.Project.Port)
		err := engine.Run(url)
		if err != nil {
			s.config.Logger.Errorf("start server failed, error: %v", err)
		}
	}()
	// Start MQ processing.
	go s.MQHandler.Subscribe()
}

// Stop stop service.
func (s *Server) Stop(ctx context.Context) {
	s.config.Logger.Info("stop agent-operator-integration server")
	// sandbox.Close() // Close and destroy the sandbox session pool.
	s.outboxMessageEvent.Stop(ctx)
	if s.skillIndexBuildWorker != nil {
		s.skillIndexBuildWorker.Stop(ctx)
	}
	mcpinstance.Close() // Shut down the instance pool.
}

func main() {
	// Initialize global configuration.
	config := config.NewConfigLoader()
	// Set error code language.
	common.SetLang(config.Project.Language)
	s := &Server{
		config:                config,
		httpHealthHandler:     driveradapters.NewHTTPHealthHandler(),
		restPublicHandler:     driveradapters.NewRestPublicHandler(),
		restPrivateHandler:    driveradapters.NewRestPrivateHandler(),
		outboxMessageEvent:    logicscommon.NewOutboxMessageEvent(),
		MQHandler:             driveradapters.NewMQHandler(),
		skillIndexSyncService: skill.NewSkillIndexSyncService(),
		skillIndexBuildWorker: skill.NewSkillIndexBuildWorker(),
	}
	s.config.Logger.Info("start agent-operator-integration server")
	if config.OTelProviders != nil {
		defer config.OTelProviders.Shutdown(context.Background())
	}
	s.Start()
	defer s.Stop(context.Background())
	// Wait for semaphore.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT) //nolint
	<-c
}
