package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/db"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/outboundtls"

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

// Server service.
type Server struct {
	// health check.
	httpHealthHandler     interfaces.HTTPRouterInterface
	restPublicHandler     interfaces.HTTPRouterInterface
	restPrivateHandler    interfaces.HTTPRouterInterface
	MQHandler             interfaces.MQHandler
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

// Start Start the service.
func (s *Server) Start() {
	gin.SetMode(gin.ReleaseMode)
	var err error
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
	// Initialize the unified capability index. A failure here is logged and retried in the
	// background: the dataset not existing yet must not stop the service from serving.
	if err = s.capabilityIndexSync.EnsureInitialized(context.Background()); err != nil {
		s.config.Logger.Errorf("init capability index sync service failed, error: %v", err)
	}
	s.startCapabilityReconciler()

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

// startCapabilityReconciler starts the periodic reconcile of Function tools and MCP tools.
func (s *Server) startCapabilityReconciler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.capabilityCancel = cancel
	if s.config.CapabilityIndexConfig.DisableReconciler || s.capabilityReconciler == nil {
		// Disabling periodic scans does not disable indexing committed Skill writes.
		skill.StartCapabilityReconciler(ctx, 0)
		s.config.Logger.Info("capability index reconciler is disabled")
		return
	}
	interval, err := time.ParseDuration(s.config.CapabilityIndexConfig.ReconcileInterval)
	if err != nil || interval <= 0 {
		s.config.Logger.Warnf("invalid capability index reconcile interval %q, falling back to 30m: %v",
			s.config.CapabilityIndexConfig.ReconcileInterval, err)
		interval = 30 * time.Minute
	}
	s.capabilityReconciler.Start(ctx, interval)
	// The other two halves run from their own packages: MCP next to the client that can list a
	// remote server, Skills next to the rule that decides which snapshot of a Skill counts.
	mcp.StartCapabilityReconciler(ctx, interval)
	skill.StartCapabilityReconciler(ctx, interval)
	s.config.Logger.Infof("capability index reconciler started, interval=%s", interval)
}

// Stop stop service.
func (s *Server) Stop(ctx context.Context) {
	s.config.Logger.Info("stop agent-operator-integration server")
	// sandbox.Close() // Close and destroy the sandbox session pool.
	if err := bkntrace.CloseEvidencePublisher(ctx); err != nil {
		s.config.Logger.Errorf("BKN Trace Evidence publisher shutdown acknowledgement failed: %v", err)
	}
	bknaudit.ClosePublisher(ctx)
	if s.capabilityCancel != nil {
		s.capabilityCancel()
	}
	if s.skillIndexBuildWorker != nil {
		s.skillIndexBuildWorker.Stop(ctx)
	}
	mcpinstance.Close() // Shut down the instance pool.
}

func main() {
	// Initialize global configuration.
	config := config.NewConfigLoader()
	// Complete upgrade-time enrollment before constructing reconcilers/clients.
	trustDB := db.NewDBPool()
	trust, err := outboundtls.New(config.OutboundTLS, outboundtls.SQLStore{DB: trustDB})
	if err != nil {
		log.Fatalf("outbound TLS configuration invalid: %v", err)
		return
	}
	if err = trust.BootstrapQuery(context.Background(), trustDB, "SELECT CONCAT('mcp:', f_mcp_id), COALESCE(f_url, '') FROM t_mcp_server_config UNION SELECT CONCAT('mcp:', f_mcp_id), COALESCE(f_url, '') FROM t_mcp_server_release UNION SELECT CONCAT('toolbox:', f_box_id), COALESCE(f_svc_url, '') FROM t_toolbox UNION SELECT CONCAT('toolbox:', t.f_box_id), COALESCE(m.f_svc_url, '') FROM t_tool t JOIN t_metadata_api m ON t.f_source_id = m.f_version JOIN t_toolbox b ON b.f_box_id = t.f_box_id WHERE COALESCE(b.f_svc_url, '') = ''"); err != nil {
		log.Fatalf("outbound TLS upgrade failed: %v", err)
		return
	}

	outboundtls.SetDefault(trust)
	// Set error code language.
	common.SetLang(config.Project.Language)
	s := &Server{
		config:                config,
		httpHealthHandler:     driveradapters.NewHTTPHealthHandler(),
		restPublicHandler:     driveradapters.NewRestPublicHandler(),
		restPrivateHandler:    driveradapters.NewRestPrivateHandler(),
		MQHandler:             driveradapters.NewMQHandler(),
		skillIndexSyncService: skill.NewSkillIndexSyncService(),
		skillIndexBuildWorker: skill.NewSkillIndexBuildWorker(),
		capabilityIndexSync:   capability.NewCapabilityIndexSyncService(),
		capabilityReconciler:  capabilityindex.NewReconciler(),
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
