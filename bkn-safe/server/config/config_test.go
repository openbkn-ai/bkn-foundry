// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.yaml")
	if err := os.WriteFile(path, []byte(`
http_addr: ":3001"
seed_on_start: false
db:
  host: db.example
  port: 3307
  user: u
  password: p
  name: safe
hydra:
  admin_url: http://hydra-admin:4445
  public_url: http://hydra-public:4444
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":3001" {
		t.Fatalf("http_addr = %q", cfg.HTTPAddr)
	}
	if cfg.SeedOnStart {
		t.Fatal("seed_on_start should be false")
	}
	if cfg.DB.Host != "db.example" || cfg.DB.Port != 3307 {
		t.Fatalf("db = %+v", cfg.DB)
	}
	if cfg.License.ServerURL != "https://license.openbkn.ai" {
		t.Fatalf("license server_url = %q", cfg.License.ServerURL)
	}
	if cfg.Upstreams.ExecutionFactory.BaseURL != "http://agent-operator-integration:9000" {
		t.Fatalf("execution factory base_url = %q", cfg.Upstreams.ExecutionFactory.BaseURL)
	}
	if cfg.Upstreams.VegaBackend.BaseURL != "http://vega-backend-svc:13014" {
		t.Fatalf("vega backend base_url = %q", cfg.Upstreams.VegaBackend.BaseURL)
	}
	if cfg.Upstreams.OntologyQuery.BaseURL != "http://ontology-query-svc:13018" {
		t.Fatalf("ontology query base_url = %q", cfg.Upstreams.OntologyQuery.BaseURL)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.yaml")
	if err := os.WriteFile(path, []byte(`
db:
  host: from-file
  port: 3306
  user: safe
  password: secret
  name: safe
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SAFE_DB_HOST", "from-env")
	cfg, err := config.LoadWithOptions(config.LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Host != "from-env" {
		t.Fatalf("host = %q, want from-env", cfg.DB.Host)
	}
}

func TestLicenseServerURLCanBeDisabledByEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.yaml")
	if err := os.WriteFile(path, []byte(`
license:
  server_url: https://license.example.test
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SAFE_LICENSE_SERVER_URL", "")
	cfg, err := config.LoadWithOptions(config.LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.License.ServerURL != "" {
		t.Fatalf("license server_url = %q, want empty offline deployment", cfg.License.ServerURL)
	}
}

// The pool and refresh defaults (#1511) must survive a config file that does
// not mention them; the file and env can still change or disable each one.
func TestPoolAndRefreshDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.yaml")
	if err := os.WriteFile(path, []byte(`
db:
  host: from-file
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithOptions(config.LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.MaxOpenConns != 50 || cfg.DB.ConnMaxIdleTime != 2*time.Minute || cfg.DB.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("pool defaults = %d/%v/%v", cfg.DB.MaxOpenConns, cfg.DB.ConnMaxIdleTime, cfg.DB.ConnMaxLifetime)
	}
	if cfg.Authz.PolicyRefreshInterval != 10*time.Minute || cfg.Authz.RowFilterMaxDepartmentIDs != 1000 {
		t.Fatalf("authz defaults = refresh %v, row-filter department bound %d", cfg.Authz.PolicyRefreshInterval, cfg.Authz.RowFilterMaxDepartmentIDs)
	}

	if err := os.WriteFile(path, []byte(`
db:
  max_open_conns: 20
  conn_max_idle_time: 1m
authz:
  policy_refresh_interval: 5m
  row_filter_max_department_ids: 750
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAFE_DB_CONN_MAX_LIFETIME", "10m")
	cfg, err = config.LoadWithOptions(config.LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.MaxOpenConns != 20 || cfg.DB.ConnMaxIdleTime != time.Minute || cfg.DB.ConnMaxLifetime != 10*time.Minute {
		t.Fatalf("pool from file/env = %d/%v/%v", cfg.DB.MaxOpenConns, cfg.DB.ConnMaxIdleTime, cfg.DB.ConnMaxLifetime)
	}
	if cfg.Authz.PolicyRefreshInterval != 5*time.Minute || cfg.Authz.RowFilterMaxDepartmentIDs != 750 {
		t.Fatalf("authz values from file = refresh %v, row-filter department bound %d", cfg.Authz.PolicyRefreshInterval, cfg.Authz.RowFilterMaxDepartmentIDs)
	}

	// 0 is a documented value for both: no cap, and no periodic reload.
	t.Setenv("SAFE_DB_MAX_OPEN_CONNS", "0")
	t.Setenv("SAFE_AUTHZ_POLICY_REFRESH_INTERVAL", "0s")
	t.Setenv("SAFE_AUTHZ_ROW_FILTER_MAX_DEPARTMENT_IDS", "500")
	cfg, err = config.LoadWithOptions(config.LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.MaxOpenConns != 0 || cfg.Authz.PolicyRefreshInterval != 0 || cfg.Authz.RowFilterMaxDepartmentIDs != 500 {
		t.Fatalf("overrides = %d / %v / %d", cfg.DB.MaxOpenConns, cfg.Authz.PolicyRefreshInterval, cfg.Authz.RowFilterMaxDepartmentIDs)
	}
}
