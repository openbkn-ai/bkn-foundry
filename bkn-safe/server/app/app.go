// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package app is bkn-safe's bootstrap, split out of package main so that a
// second entry point can reuse it. The community binary (bkn-safe/server) and
// the enterprise binary (openbkn-ee bkn-safe/cmd/bkn-safe-ee) run
// byte-identical startup logic; they differ only in what happens between Boot
// and Run.
//
//	// community
//	a, err := app.Boot(app.Options{})
//	a.Run()
//
//	// enterprise
//	a, err := app.Boot(app.Options{})
//	eepermobject.Setup(a.DB())   // registers only if the license says so
//	a.Run()
//
// Boot installs the license gate; Run freezes the extension registry and
// serves. Everything an extension needs to assemble itself therefore exists
// between the two calls, and nothing can register once requests start flowing.
//
// Design: license-server docs/design/open-core-gating.md §2.5.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authzgate"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/decisionlog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httpapi"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/seed"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// Options configures Boot. The zero value is what the community entry point
// uses.
type Options struct {
	// ConfigPath is an explicit YAML config file. Empty means the usual
	// resolution order (defaults, then SAFE_CONFIG).
	ConfigPath string
}

// App is a booted, not-yet-serving bkn-safe.
type App struct {
	cfg      *config.Config
	db       *gorm.DB
	enforcer *authz.Enforcer
	deps     httpapi.Deps
	licSvc   *license.Service
	// decisions is the authorization decision log; its retention purge runs
	// alongside the listener.
	decisions *decisionlog.Store
	// freshAuthorizationStore is captured before AutoMigrate creates the
	// Casbin/marker schema. An old but empty store must still run the explicit
	// offline migration and therefore is never inferred as fresh later.
	freshAuthorizationStore bool
}

// Boot brings up config, database, authz, the license hub, and the license
// gate — everything except the extension freeze and the listener.
//
// Licensing never blocks the auth service: if the license hub cannot start
// (typically no resolvable instance fingerprint), bkn-safe runs without the
// license surface and every paid feature stays off. Community capability is
// unaffected, because it is not gated.
func Boot(opts Options) (*App, error) {
	cfg, err := config.LoadWithOptions(config.LoadOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if path := opts.ConfigPath; path != "" {
		slog.Info("config loaded", "file", path)
	} else if path := os.Getenv("SAFE_CONFIG"); path != "" {
		slog.Info("config loaded", "file", path)
	}

	db, err := database.Open(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	freshAuthorizationStore := authzgate.IsFreshAuthorizationStore(db)
	if err := database.Migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	enforcer, err := authz.New(db)
	if err != nil {
		return nil, fmt.Errorf("init authz: %w", err)
	}
	// Run the normal_user withdrawal independently of seed_on_start. This is an
	// upgrade migration for bkn-safe-owned data, whereas seed_on_start only
	// controls whether the current catalog and role matrix are reconciled.
	if err := seed.ReconcileWithdrawnNormalUserRole(db, enforcer); err != nil {
		return nil, fmt.Errorf("reconcile withdrawn normal user role: %w", err)
	}

	if cfg.SeedOnStart {
		if err := seed.Apply(db, enforcer); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
		slog.Info("seed applied (roles + catalog + grants)")
	}

	userStore := auth.NewUserStore(db)
	hydraAdmin := auth.NewHydraAdmin(cfg.Hydra.AdminURL)

	// Authenticator: local bcrypt store, plus LDAP federation when configured
	// (local first, then LDAP).
	var authenticator auth.Authenticator = userStore
	if cfg.LDAP.Enabled() {
		authenticator = auth.NewChain(userStore, auth.NewLDAPAuthenticator(cfg.LDAP, db))
		slog.Info("LDAP federation enabled", "url", cfg.LDAP.URL)
	}
	provider := auth.NewProvider(authenticator, hydraAdmin, userStore)
	dir := directory.New(db)
	auditStore := audit.New(db)
	accessLogStore := accesslog.New(db)
	decisionStore := decisionlog.New(db, decisionlog.Options{
		Enabled:         cfg.Audit.DecisionLog.Enabled,
		AllowSampleRate: cfg.Audit.DecisionLog.AllowSampleRate,
		QueueSize:       cfg.Audit.DecisionLog.QueueSize,
	})
	if decisionStore.Enabled() {
		slog.Info("authz decision log enabled",
			"allow_sample_rate", cfg.Audit.DecisionLog.AllowSampleRate,
			"retention_days", cfg.Audit.DecisionLog.RetentionDays)
	}

	// Cluster license hub: hold the one .lic, be the only egress to the
	// license-server, distribute to modules.
	licSvc, err := license.New(db, cfg.License, auditStore)
	if err != nil {
		slog.Error("license hub disabled", "err", err)
		licSvc = nil
	} else {
		go licSvc.Run(context.Background())
		slog.Info("license hub enabled", "instance_fp", licSvc.Fingerprint(), "server_url", cfg.License.ServerURL)
	}

	// The gate has to be in place before any extension consults the licence.
	// With no license hub the package keeps its zero value: community, not
	// licensed — which is also the right answer, so there is nothing to install.
	//
	// bkn-safe is the cluster's licence holder, not a consumer: it reads its own
	// verified snapshot instead of fetching one from a hub (ee-design.md §4.1).
	// A consequence worth knowing before debugging: -tags ee_dev and
	// OPENBKN_EDITION do NOT affect this service. Its tier comes from a real
	// certificate or, in tests, from entitlement.SetGateForTest.
	if licSvc != nil {
		entitlement.SetGate(license.Gate(licSvc))
	}

	return &App{
		cfg:                     cfg,
		db:                      db,
		enforcer:                enforcer,
		licSvc:                  licSvc,
		freshAuthorizationStore: freshAuthorizationStore,
		decisions:               decisionStore,
		deps: httpapi.Deps{
			Enforcer:  enforcer,
			DB:        db,
			Provider:  provider,
			Hydra:     hydraAdmin,
			Directory: dir,
			Users:     userStore,
			Audit:     auditStore,
			AccessLog: accessLogStore,
			Decisions: decisionStore,
			License:   licSvc,
		},
	}, nil
}

// DB is the shared database handle. An enterprise extension stores its own
// tables here — subject to the migration discipline that keeps the upgrade
// path intact: ee migrations only add tables and columns, and never alter or
// drop anything core owns (open-core-gating §2.6 constraint 1, enforced by CI).
func (a *App) DB() *gorm.DB { return a.db }

// Addr is the configured listen address.
func (a *App) Addr() string { return a.cfg.HTTPAddr }

// Run closes the extension registry and serves until the listener fails.
// Registering an extension after this point panics, by design.
func (a *App) Run() error {
	// Enterprise Setup calls happen between Boot and Run. Checking here lets a
	// fresh Enterprise install record present_empty after its private table has
	// been created, while an upgraded store still needs the offline marker before
	// any listener can accept traffic.
	if err := a.ensureAuthorizationMigrationReady(context.Background()); err != nil {
		return fmt.Errorf("authorization migration gate: %w", err)
	}
	entitlement.Freeze()
	slog.Info("extensions assembled", "assembled", entitlement.Assembled())

	// Background audit work lives for as long as the listener: the chain head
	// anchor export and the decision-log retention purge.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.deps.Audit.LogHead(ctx, a.cfg.Audit.ChainHeadLogInterval)
	go a.decisions.RunRetention(ctx, a.cfg.Audit.DecisionLog.RetentionDays, 24*time.Hour)

	r := httpapi.New(a.deps)
	slog.Info("bkn-safe listening", "addr", a.cfg.HTTPAddr)
	err := r.Run(a.cfg.HTTPAddr)
	// The listener is gone; drain the queued decisions before reporting why.
	a.decisions.Close()
	return err
}

func (a *App) ensureAuthorizationMigrationReady(ctx context.Context) error {
	if err := authzgate.SeedFreshInstallMarker(ctx, a.db, a.freshAuthorizationStore); err != nil {
		return err
	}
	return authzgate.VerifyCurrentMarker(ctx, a.db)
}
