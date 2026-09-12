// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm/logger"
)

func TestLoadDatabaseConfigUsesFileThenEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "safe.yaml")
	content := []byte("db:\n  host: file-host\n  port: 3307\n  user: file-user\n  password: file-secret\n  name: file-safe\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAFE_DB_HOST", "environment-host")
	t.Setenv("SAFE_DB_PORT", "4406")

	cfg, err := loadDatabaseConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "environment-host" || cfg.Port != 4406 ||
		cfg.User != "file-user" || cfg.Name != "file-safe" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestMigrationGORMConfigKeepsStandardOutputMachineReadable(t *testing.T) {
	cfg := migrationGORMConfig()
	if cfg.Logger != logger.Discard {
		t.Fatalf("migration logger = %T, want logger.Discard", cfg.Logger)
	}
}
