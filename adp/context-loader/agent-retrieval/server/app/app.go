// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
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
	refresh  func(stop <-chan struct{})
	stop     chan struct{}
	evidence *bkntrace.EvidencePublisherRuntime
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
	var evidence *bkntrace.EvidencePublisherRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED")), "true") {
		runtime, err := bkntrace.NewEvidencePublisherRuntime()
		if err != nil {
			return nil, err
		}
		bkntrace.SetEvidencePublisher(runtime.Publisher)
		evidence = runtime
	}
	// Set error code language
	common.SetLang(cfg.Project.Language)

	gate, refresh := entitlement.GateWithRunner()
	entitlement.SetGate(gate)

	return &App{config: cfg, refresh: refresh, stop: make(chan struct{}), evidence: evidence}, nil
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
	if err := validatePorts(a.config.Project); err != nil {
		return err
	}
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
		restPublicHandler:  driveradapters.NewRestPublicHandler(a.config.Logger, a.config.Project.SandboxPort),
		restPrivateHandler: driveradapters.NewRestPrivateHandler(a.config.Logger),
	}
	s.config.Logger.Info("start agent-retrieval server")
	if a.config.OTelProviders != nil {
		defer a.config.OTelProviders.Shutdown(context.Background())
	}
	defer s.config.Logger.Info("stop agent-retrieval server")
	s.Start()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var flushStop chan struct{}
	var flushDone chan struct{}
	var periodicFlushCancel context.CancelFunc
	if a.evidence != nil {
		flushStop = make(chan struct{})
		flushDone = make(chan struct{})
		periodicFlushCtx, cancel := context.WithCancel(context.Background())
		periodicFlushCancel = cancel
		go func() {
			defer close(flushDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					flushCtx, flushCancel := context.WithTimeout(periodicFlushCtx, 5*time.Second)
					result := bkntrace.FlushEvidencePublisher(flushCtx)
					flushCancel()
					if result.Dropped > 0 {
						a.config.Logger.Warnf("BKN Trace Kafka evidence flush dropped %d records", result.Dropped)
					}
				case <-flushStop:
					return
				}
			}
		}()
	}
	<-ctx.Done()
	close(a.stop)
	if flushStop != nil {
		periodicFlushCancel()
		close(flushStop)
		periodicFlushStopped := false
		select {
		case <-flushDone:
			periodicFlushStopped = true
		case <-time.After(time.Second):
			a.config.Logger.Warnf("BKN Trace Kafka evidence periodic flush did not stop before shutdown deadline")
		}
		if periodicFlushStopped {
			flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if result := bkntrace.FlushEvidencePublisher(flushCtx); result.Dropped > 0 {
				a.config.Logger.Warnf("BKN Trace Kafka evidence shutdown flush dropped %d records", result.Dropped)
			}
			if result := bkntrace.CloseEvidencePublisher(flushCtx); result.Dropped > 0 {
				a.config.Logger.Warnf("BKN Trace Kafka evidence shutdown close dropped %d records", result.Dropped)
			}
			flushCancel()
			_ = a.evidence.Producer.Close()
		}
	}
	return nil
}

func validatePorts(project config.Project) error {
	if project.SandboxPort < 1 || project.SandboxPort > 65535 {
		return fmt.Errorf("project.sandbox_port must be between 1 and 65535")
	}
	if project.SandboxPort == project.Port {
		return fmt.Errorf("project.sandbox_port must differ from project.port")
	}
	return nil
}

// Start starts the server
func (s *Server) Start() {
	gin.SetMode(gin.ReleaseMode)

	go s.serve(s.platformEngine(), s.config.Project.Port, "platform")
	go s.serve(s.sandboxEngine(), s.config.Project.SandboxPort, "sandbox")
}

// platformEngine serves both the public authenticated API and the trusted
// in-cluster API. Sandbox workloads must never be allowed to reach its port.
func (s *Server) platformEngine() *gin.Engine {
	engine := s.baseEngine()

	routerInternalGroup := engine.Group("/api/agent-retrieval/in/v1")
	routerInternalGroup.Use(gin.Recovery())
	s.restPrivateHandler.RegisterRouter(routerInternalGroup)

	s.registerPublicRoutes(engine)
	return engine
}

// sandboxEngine is a separate L4 boundary for untrusted sandbox workloads.
// It deliberately omits the /in router so a NetworkPolicy can allow public
// BKN/MCP calls without also exposing trusted internal APIs on the same port.
func (s *Server) sandboxEngine() *gin.Engine {
	engine := s.baseEngine()
	s.registerPublicRoutes(engine)
	return engine
}

func (s *Server) baseEngine() *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.UseRawPath = true
	routerHealth := engine.Group("/health")
	s.httpHealthHandler.RegisterRouter(routerHealth)
	return engine
}

func (s *Server) registerPublicRoutes(engine *gin.Engine) {
	routerGroup := engine.Group("/api/agent-retrieval/v1")
	routerGroup.Use(gin.Recovery())
	s.restPublicHandler.RegisterRouter(routerGroup)
}

func (s *Server) serve(engine *gin.Engine, port int, surface string) {
	url := fmt.Sprintf("%s:%d", s.config.Project.Host, port)
	if err := engine.Run(url); err != nil {
		s.config.Logger.Errorf("start %s server failed, error: %v", surface, err)
	}
}
