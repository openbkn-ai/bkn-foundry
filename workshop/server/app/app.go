// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package app owns the Workshop process lifecycle. The community binary boots
// and runs with probes only; the paid build (openbkn-ee workshop-ee) boots the
// same application, registers Workshop's business routes between Boot and Run,
// and only then serves.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/common"
	"github.com/openbkn-ai/bkn-foundry/workshop/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/workshop/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/workshop/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/workshop/server/locale"
)

// Application is a booted Workshop process that has not started serving yet.
// Extension assembly belongs between Boot and Run.
type Application struct {
	appSetting    *common.AppSetting
	db            *sql.DB
	otelProviders *otel.Providers
	auth          interfaces.AuthAccess
	refresh       func(stop <-chan struct{})
	stop          chan struct{}

	extensionMu      sync.Mutex
	extensionsFrozen bool
	routeExtensions  map[string]func(*gin.Engine)
	workers          map[string]func(context.Context)
}

// Boot loads configuration, connects the database and installs the licence
// gate without opening the HTTP listener.
func Boot() *Application {
	logger.Info("Server Initializing")

	appSetting := common.NewSetting()
	logger.Info("Server Init Setting Success")

	locale.Register()
	i18n.SetLang(appSetting.ServerSetting.Language)
	gin.SetMode(appSetting.ServerSetting.RunMode)
	logger.Infof("Server RunMode: %s", appSetting.ServerSetting.RunMode)

	otelProviders, err := otel.InitOTel(context.Background(), &appSetting.OtelSetting)
	if err != nil {
		logger.Fatalf("Failed to initialize OpenTelemetry provider: %v", err)
	}

	db := libdb.NewDB(&appSetting.DBSetting)
	gate, refresh := entitlement.GateWithRunner()
	entitlement.SetGate(gate)

	return &Application{
		appSetting:      appSetting,
		db:              db,
		otelProviders:   otelProviders,
		auth:            auth.NewHydraAuthAccess(appSetting.HydraAdminSetting),
		refresh:         refresh,
		stop:            make(chan struct{}),
		routeExtensions: make(map[string]func(*gin.Engine)),
		workers:         make(map[string]func(context.Context)),
	}
}

// Setting exposes the boot configuration to assembled extensions.
func (server *Application) Setting() *common.AppSetting {
	return server.appSetting
}

// DB exposes the shared database connection to assembled extensions.
func (server *Application) DB() *sql.DB {
	return server.db
}

// Auth exposes token verification, for driveradapters.Authenticate.
func (server *Application) Auth() interfaces.AuthAccess {
	return server.auth
}

// RegisterRoutes adds one named route extension. It is only valid after Boot
// and before Run; duplicate names are rejected so one capability cannot
// silently replace another's surface.
func (server *Application) RegisterRoutes(name string, install func(*gin.Engine)) error {
	server.extensionMu.Lock()
	defer server.extensionMu.Unlock()
	if server.extensionsFrozen {
		return fmt.Errorf("workshop app: register routes %q after Run", name)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workshop app: route extension name is required")
	}
	if install == nil {
		return fmt.Errorf("workshop app: route extension %q has no installer", name)
	}
	if _, exists := server.routeExtensions[name]; exists {
		return fmt.Errorf("workshop app: route extension %q already registered", name)
	}
	server.routeExtensions[name] = install
	return nil
}

// RegisterWorker adds one named background job. Run starts it once the
// listener is up and cancels its context at shutdown, then waits for it to
// return before closing the database. Like RegisterRoutes it is only valid
// between Boot and Run, and names are unique.
func (server *Application) RegisterWorker(name string, run func(ctx context.Context)) error {
	server.extensionMu.Lock()
	defer server.extensionMu.Unlock()
	if server.extensionsFrozen {
		return fmt.Errorf("workshop app: register worker %q after Run", name)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workshop app: worker name is required")
	}
	if run == nil {
		return fmt.Errorf("workshop app: worker %q has no function", name)
	}
	if _, exists := server.workers[name]; exists {
		return fmt.Errorf("workshop app: worker %q already registered", name)
	}
	server.workers[name] = run
	return nil
}

// startWorkers runs the registered jobs; the returned function cancels them
// and waits until every one has returned.
func (server *Application) startWorkers() func() {
	server.extensionMu.Lock()
	names := make([]string, 0, len(server.workers))
	for name := range server.workers {
		names = append(names, name)
	}
	sort.Strings(names)
	runs := make([]func(context.Context), 0, len(names))
	for _, name := range names {
		runs = append(runs, server.workers[name])
	}
	server.extensionMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for index, run := range runs {
		wg.Add(1)
		logger.Infof("Worker %s started", names[index])
		go func() {
			defer wg.Done()
			run(ctx)
		}()
	}
	return func() {
		cancel()
		wg.Wait()
	}
}

func (server *Application) freezeRouteExtensions() []func(*gin.Engine) {
	server.extensionMu.Lock()
	defer server.extensionMu.Unlock()
	if server.extensionsFrozen {
		panic("workshop app: Run called more than once")
	}
	server.extensionsFrozen = true
	names := make([]string, 0, len(server.routeExtensions))
	for name := range server.routeExtensions {
		names = append(names, name)
	}
	sort.Strings(names)
	installers := make([]func(*gin.Engine), 0, len(names))
	for _, name := range names {
		installers = append(installers, server.routeExtensions[name])
	}
	return installers
}

// handler builds the engine: base middleware and probes first, then the
// extensions in name order.
func (server *Application) handler() http.Handler {
	return server.handlerWith(server.db)
}

func (server *Application) handlerWith(db driveradapters.Pinger) http.Handler {
	routeExtensions := server.freezeRouteExtensions()
	engine := gin.New()
	driveradapters.RegisterBase(engine, db)
	for _, install := range routeExtensions {
		install(engine)
	}
	return engine
}

// Run freezes the assembled process shape and serves until shutdown.
func (server *Application) Run() {
	logger.Info("Server Starting")
	engine := server.handler()
	entitlement.Freeze()
	if caps := entitlement.Assembled(); len(caps) > 0 {
		logger.Infof("Extensions assembled: %+v", caps)
	}
	if server.refresh != nil {
		go server.refresh(server.stop)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s := &http.Server{
		Addr:           ":" + strconv.Itoa(server.appSetting.ServerSetting.HttpPort),
		Handler:        engine,
		ReadTimeout:    server.appSetting.ServerSetting.ReadTimeOut * time.Second,
		WriteTimeout:   server.appSetting.ServerSetting.WriteTimeout * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		err := s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("s.ListenAndServe err:%v", err)
		}
	}()
	logger.Infof("Server Started on Port:%d", server.appSetting.ServerSetting.HttpPort)
	stopWorkers := server.startWorkers()

	<-ctx.Done()
	close(server.stop)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger.Info("Server Start Shutdown")
	// A request still open past the deadline (a long AI conversation) must not
	// skip stopping the workers and closing the database.
	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("Server Shutdown:%v", err)
	}
	stopWorkers()
	if err := server.db.Close(); err != nil {
		logger.Warnf("Database close failed: %v", err)
	}
	server.otelProviders.Shutdown(shutdownCtx)

	logger.Info("Server Exited")
}
