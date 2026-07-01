// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package config loads bkn-safe configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is the full bkn-safe configuration.
type Config struct {
	HTTPAddr string // listen address for bkn-safe's own HTTP (login/consent/device + APIs)
	DB       DBConfig
	Hydra    HydraConfig
	LDAP     LDAPConfig
	// SeedOnStart controls whether roles/resource-types/operations/grants are
	// seeded into the DB at startup (idempotent). Default true.
	SeedOnStart bool
}

// DBConfig points bkn-safe at its database. The proton-rds driver fakes
// Dameng(DM8)/Kingbase(KDB9) as MySQL wire, so Type only selects the GORM
// dialect tuning; the connection is always opened via the "proton-rds" driver.
type DBConfig struct {
	Type     string // MySQL | DM8 | KDB9
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	Params   string // extra DSN params
}

// DSN returns a go-sql-driver/mysql style DSN (proton-rds speaks MySQL wire).
func (d DBConfig) DSN() string {
	params := d.Params
	if params == "" {
		params = "charset=utf8mb4&parseTime=true&loc=Local"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s", d.User, d.Password, d.Host, d.Port, d.Name, params)
}

// HydraConfig is how bkn-safe reaches hydra's admin API (login/consent/device
// accept + client mgmt). Admin is internal-only.
type HydraConfig struct {
	AdminURL  string // e.g. http://hydra-admin:4445
	PublicURL string // e.g. http://hydra-public:4444 (for building verification URIs)
}

// LDAPConfig enables the light external-directory federation (Phase 5). When
// URL is empty, LDAP federation is disabled and only the local user store is used.
type LDAPConfig struct {
	URL          string // ldap://host:389 (empty = disabled)
	BindDN       string
	BindPassword string
	BaseDN       string
	UserFilter   string // e.g. (uid=%s)
}

// Enabled reports whether LDAP federation is configured.
func (l LDAPConfig) Enabled() bool { return l.URL != "" }

// Load reads configuration from the environment with sane dev defaults.
func Load() *Config {
	return &Config{
		HTTPAddr:    env("SAFE_HTTP_ADDR", ":3000"),
		SeedOnStart: envBool("SAFE_SEED_ON_START", true),
		DB: DBConfig{
			Type:     env("SAFE_DB_TYPE", "MySQL"),
			Host:     env("SAFE_DB_HOST", "127.0.0.1"),
			Port:     envInt("SAFE_DB_PORT", 3306),
			User:     env("SAFE_DB_USER", "safe"),
			Password: env("SAFE_DB_PASSWORD", "secret"),
			Name:     env("SAFE_DB_NAME", "safe"),
			Params:   env("SAFE_DB_PARAMS", ""),
		},
		Hydra: HydraConfig{
			AdminURL:  env("SAFE_HYDRA_ADMIN_URL", "http://127.0.0.1:4445"),
			PublicURL: env("SAFE_HYDRA_PUBLIC_URL", "http://127.0.0.1:4444"),
		},
		LDAP: LDAPConfig{
			URL:          env("SAFE_LDAP_URL", ""),
			BindDN:       env("SAFE_LDAP_BIND_DN", ""),
			BindPassword: env("SAFE_LDAP_BIND_PASSWORD", ""),
			BaseDN:       env("SAFE_LDAP_BASE_DN", ""),
			UserFilter:   env("SAFE_LDAP_USER_FILTER", "(uid=%s)"),
		},
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
