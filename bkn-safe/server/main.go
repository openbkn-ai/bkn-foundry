// Command bkn-safe is the ISF replacement auth service: authentication
// (hydra login/consent/device provider), authorization (Casbin), and user
// management (directory + LDAP). hydra issues the tokens.
package main

import (
	"log/slog"
	"os"

	"bkn-safe/config"
	"bkn-safe/internal/audit"
	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/database"
	"bkn-safe/internal/directory"
	"bkn-safe/internal/httpapi"
	"bkn-safe/internal/seed"
)

func main() {
	cfg := config.Load()

	db, err := database.Open(cfg.DB)
	if err != nil {
		fatal("open database", err)
	}
	if err := database.Migrate(db); err != nil {
		fatal("migrate", err)
	}

	enforcer, err := authz.New(db)
	if err != nil {
		fatal("init authz", err)
	}

	if cfg.SeedOnStart {
		if err := seed.Apply(db, enforcer); err != nil {
			fatal("seed", err)
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

	r := httpapi.New(httpapi.Deps{
		Enforcer:  enforcer,
		DB:        db,
		Provider:  provider,
		Hydra:     hydraAdmin,
		Directory: dir,
		Users:     userStore,
		Audit:     audit.New(db),
	})
	slog.Info("bkn-safe listening", "addr", cfg.HTTPAddr)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		fatal("http serve", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
