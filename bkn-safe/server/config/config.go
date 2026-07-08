// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package config loads bkn-safe configuration from defaults, an optional YAML
// file (SAFE_CONFIG or -config), and environment variable overrides.
package config

import "fmt"

// Config is the full bkn-safe configuration.
type Config struct {
	HTTPAddr string `yaml:"http_addr"` // listen address (login/consent/device + APIs)
	DB       DBConfig
	Hydra    HydraConfig
	LDAP     LDAPConfig
	// SeedOnStart controls whether roles/resource-types/operations/grants are
	// seeded into the DB at startup (idempotent). Default true.
	SeedOnStart bool `yaml:"seed_on_start"`
}

// DBConfig points bkn-safe at its database. The proton-rds driver fakes
// Dameng(DM8)/Kingbase(KDB9) as MySQL wire, so Type only selects the GORM
// dialect tuning; the connection is always opened via the "proton-rds" driver.
type DBConfig struct {
	Type     string `yaml:"type"` // MySQL | DM8 | KDB9
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	Params   string `yaml:"params"` // extra DSN params
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
	AdminURL  string `yaml:"admin_url"`  // e.g. http://hydra-admin:4445
	PublicURL string `yaml:"public_url"` // e.g. http://hydra-public:4444
}

// LDAPConfig enables the light external-directory federation (Phase 5). When
// URL is empty, LDAP federation is disabled and only the local user store is used.
type LDAPConfig struct {
	URL          string `yaml:"url"` // ldap://host:389 (empty = disabled)
	BindDN       string `yaml:"bind_dn"`
	BindPassword string `yaml:"bind_password"`
	BaseDN       string `yaml:"base_dn"`
	UserFilter   string `yaml:"user_filter"` // e.g. (uid=%s)
}

// Enabled reports whether LDAP federation is configured.
func (l LDAPConfig) Enabled() bool { return l.URL != "" }
