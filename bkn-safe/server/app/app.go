// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package app is bkn-safe's bootstrap, split out of package main so that a
// second entry point can reuse it. The community binary (bkn-safe/server) and
// the enterprise binary (bkn-foundry-ee cmd/bkn-safe-ee) run byte-identical
// startup logic; they differ only in what happens between Boot and Run.
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

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httpapi"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/seed"
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
	if err := database.Migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	enforcer, err := authz.New(db)
	if err != nil {
		return nil, fmt.Errorf("init authz: %w", err)
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

	// The gate has to be in place before any extension checks its own license.
	// With no license hub the registry keeps its deny-everything zero value.
	if licSvc != nil {
		extension.SetGate(extension.GateFunc(func(f extension.Feature) bool {
			return licSvc.FeatureEnabled(string(f))
		}))
	}

	return &App{
		cfg:      cfg,
		db:       db,
		enforcer: enforcer,
		licSvc:   licSvc,
		deps: httpapi.Deps{
			Enforcer:  enforcer,
			DB:        db,
			Provider:  provider,
			Hydra:     hydraAdmin,
			Directory: dir,
			Users:     userStore,
			Audit:     auditStore,
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
	extension.Freeze()
	slog.Info("extensions assembled", "registered", extension.Registered(), "assembly", extension.Assembly())

	r := httpapi.New(a.deps)
	slog.Info("bkn-safe listening", "addr", a.cfg.HTTPAddr)
	return r.Run(a.cfg.HTTPAddr)
}
