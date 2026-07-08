// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config

import (
	"fmt"
	"os"
)

// LoadOptions controls config resolution order: defaults → file → env overrides.
type LoadOptions struct {
	// ConfigPath is a YAML file path. When empty, SAFE_CONFIG env is checked.
	ConfigPath string
}

// LoadWithOptions resolves configuration. File path from ConfigPath or SAFE_CONFIG.
// Non-empty environment variables override file values.
func LoadWithOptions(opts LoadOptions) (*Config, error) {
	path := opts.ConfigPath
	if path == "" {
		path = os.Getenv("SAFE_CONFIG")
	}

	var cfg *Config
	if path != "" {
		loaded, err := LoadFromFile(path)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	} else {
		cfg = defaultConfig()
	}

	applyEnv(cfg)
	return cfg, nil
}

// Load reads configuration (defaults, optional SAFE_CONFIG file, env overrides).
func Load() *Config {
	cfg, err := LoadWithOptions(LoadOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bkn-safe: load config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("SAFE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v, ok := envBool("SAFE_SEED_ON_START"); ok {
		cfg.SeedOnStart = v
	}
	if v := os.Getenv("SAFE_DB_TYPE"); v != "" {
		cfg.DB.Type = v
	}
	if v := os.Getenv("SAFE_DB_HOST"); v != "" {
		cfg.DB.Host = v
	}
	if v, ok := envInt("SAFE_DB_PORT"); ok {
		cfg.DB.Port = v
	}
	if v := os.Getenv("SAFE_DB_USER"); v != "" {
		cfg.DB.User = v
	}
	if v := os.Getenv("SAFE_DB_PASSWORD"); v != "" {
		cfg.DB.Password = v
	}
	if v := os.Getenv("SAFE_DB_NAME"); v != "" {
		cfg.DB.Name = v
	}
	if v := os.Getenv("SAFE_DB_PARAMS"); v != "" {
		cfg.DB.Params = v
	}
	if v := os.Getenv("SAFE_HYDRA_ADMIN_URL"); v != "" {
		cfg.Hydra.AdminURL = v
	}
	if v := os.Getenv("SAFE_HYDRA_PUBLIC_URL"); v != "" {
		cfg.Hydra.PublicURL = v
	}
	if v := os.Getenv("SAFE_LDAP_URL"); v != "" {
		cfg.LDAP.URL = v
	}
	if v := os.Getenv("SAFE_LDAP_BIND_DN"); v != "" {
		cfg.LDAP.BindDN = v
	}
	if v := os.Getenv("SAFE_LDAP_BIND_PASSWORD"); v != "" {
		cfg.LDAP.BindPassword = v
	}
	if v := os.Getenv("SAFE_LDAP_BASE_DN"); v != "" {
		cfg.LDAP.BaseDN = v
	}
	if v := os.Getenv("SAFE_LDAP_USER_FILTER"); v != "" {
		cfg.LDAP.UserFilter = v
	}
}

func envInt(k string) (int, bool) {
	v := os.Getenv(k)
	if v == "" {
		return 0, false
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return 0, false
	}
	return n, true
}

func envBool(k string) (bool, bool) {
	v := os.Getenv(k)
	if v == "" {
		return false, false
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true, true
	case "0", "false", "FALSE", "False", "no", "NO", "No":
		return false, true
	default:
		return false, false
	}
}
