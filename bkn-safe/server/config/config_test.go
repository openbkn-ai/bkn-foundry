// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"bkn-safe/config"
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
