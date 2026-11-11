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
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/licverify"
	_ "go.uber.org/automaxprocs"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/bknagent"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/bknbackend"
	checkaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/check"
	draftaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/draft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/executionfactory"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/gitstore"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/idregistry"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/indexbuild"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/project"
	regressionaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/regression"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/release"
	requirementaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/requirement"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/vega"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
	releaselogic "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/release"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

// Application is a booted bkn-project process that has not started serving yet.
type Application struct {
	appSetting          *common.AppSetting
	db                  *sql.DB
	otelProviders       *otel.Providers
	restHandler         driveradapters.RestHandler
	refreshLicense      func(stop <-chan struct{})
	knowledgeBaseRoutes func(*gin.Engine)
	workers             []func(context.Context)
}

// Boot initializes shared infrastructure without opening the HTTP listener.
func Boot() *Application {
	logger.Info("Server Initializing")
	gate, refreshLicense := entitlement.GateWithRunner()
	entitlement.SetGate(gate)

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
	logics.SetProjectAccess(project.NewProjectAccess(appSetting))
	logics.SetDraftAccess(draftaccess.NewDraftAccess(appSetting))
	logics.SetCheckAccess(checkaccess.NewCheckAccess(appSetting))
	logics.SetIDRegistryAccess(idregistry.NewIDRegistryAccess(appSetting))
	logics.SetVegaAccess(vega.NewVegaAccess(appSetting))
	logics.SetIndexBuildAccess(indexbuild.NewIndexBuildAccess(appSetting))
	logics.SetGitStore(gitstore.NewGitStore(appSetting))
	logics.SetBknBackendAccess(bknbackend.NewBknBackendAccess(appSetting))
	logics.SetKNVersionAccess(bknbackend.NewKNVersionAccess(appSetting))
	logics.SetExecutionFactoryAccess(executionfactory.NewExecutionFactoryAccess(appSetting))
	logics.SetBknAgentAccess(bknagent.NewBknAgentAccess(appSetting))
	logics.SetRequirementAccess(requirementaccess.NewRequirementAccess(appSetting))
	logics.SetRegressionAccess(regressionaccess.NewRegressionAccess(appSetting))
	logger.Infof("Git store root: %s", appSetting.GitStoreSetting.RootDir)

	// Drafting and checks run in this process; those a previous run left going will never finish.
	if err := draft.NewDraftService(appSetting).FailInterrupted(context.Background()); err != nil {
		logger.Errorf("Failed to fail interrupted drafts: %v", err)
	}
	if err := requirement.NewRequirementService().FailInterrupted(context.Background()); err != nil {
		logger.Errorf("Failed to fail interrupted requirement drafting: %v", err)
	}
	// So do the regressions of releases.
	if err := releaselogic.NewRegressionService().FailInterrupted(context.Background()); err != nil {
		logger.Errorf("Failed to fail interrupted regressions: %v", err)
	}

	return &Application{
		appSetting:     appSetting,
		db:             db,
		otelProviders:  otelProviders,
		restHandler:    driveradapters.NewRestHandler(appSetting, db),
		refreshLicense: refreshLicense,
	}
}

// DB is the database of the process, for an edition to open its own stores on.
func (server *Application) DB() *sql.DB { return server.db }

// AddWorker runs work beside serving until shutdown, which waits for it to return. Call during
// assembly.
func (server *Application) AddWorker(work func(ctx context.Context)) {
	entitlement.MustBeAssembling("bkn-project worker")
	if work == nil {
		panic("bkn-project: a worker must not be nil")
	}
	server.workers = append(server.workers, work)
}

// RegisterKnowledgeBaseRoutes installs the knowledge-base API of an edition
// build. It does not replace any community route. Call once during assembly.
func (server *Application) RegisterKnowledgeBaseRoutes(install func(*gin.Engine)) {
	entitlement.MustBeAssembling("bkn-project knowledge base routes")
	if install == nil || server.knowledgeBaseRoutes != nil {
		panic("bkn-project: knowledge base routes require one non-nil installer")
	}
	server.knowledgeBaseRoutes = install
}

func (server *Application) httpHandler() http.Handler {
	community := gin.New()
	server.restHandler.RegisterPublic(community)
	if server.knowledgeBaseRoutes == nil {
		return community
	}
	extended := gin.New()
	server.restHandler.RegisterPublic(extended)
	server.knowledgeBaseRoutes(extended)
	return knowledgeBaseGate(extended, community)
}

// knowledgeBaseGate selects between complete routers before route matching, including method
// handling and canonical-path redirects. The extended router must contain the community routes
// too, and is installed only by an edition that brings the knowledge base. Unlicensed, it leaks no
// extension route through a redirect, Allow header or a different not-found response; downgrades
// apply without restart. Authentication and project or source authorization stay in handlers.
func knowledgeBaseGate(extended, community http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entitlement.AtLeast(licverify.EditionProfessional) {
			extended.ServeHTTP(w, r)
			return
		}
		community.ServeHTTP(w, r)
	})
}

// Run serves until shutdown.
func (server *Application) Run() {
	logger.Info("Server Starting")
	entitlement.Freeze()

	// Create the Gin engine and register APIs.
	handler := server.httpHandler()
	logger.Info("Server Register API Success")

	// Listen for interrupt signals (SIGINT and SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var workers sync.WaitGroup
	for _, work := range server.workers {
		workers.Go(func() { work(ctx) })
	}
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	if server.refreshLicense != nil {
		go server.refreshLicense(ctx.Done())
	}

	// Initialize the HTTP service.
	s := &http.Server{
		Addr:           ":" + strconv.Itoa(server.appSetting.ServerSetting.HttpPort),
		Handler:        handler,
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
	select {
	case <-workersDone:
	case <-shutdownCtx.Done():
	}
	if err := server.db.Close(); err != nil {
		logger.Warnf("Database close failed: %v", err)
	}
	server.otelProviders.Shutdown(shutdownCtx)

	logger.Info("Server Exited")
}
