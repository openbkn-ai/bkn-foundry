package conf

import (
	"testing"
	"time"
)

func TestCoreConfigRequiresExplicitMariaDBDSN(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_STORE", "mariadb")
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "trace:secret@tcp(mariadb:3306)/trace?parseTime=true")
	t.Setenv("BKN_TRACE_CORE_AUTO_MIGRATE", "true")
	t.Setenv("BKN_TRACE_CORE_ABANDON_INTERVAL", "45s")

	config, err := NewCoreConfig()
	if err != nil {
		t.Fatalf("new core config: %v", err)
	}
	if config.Store != "mariadb" || config.MariaDBDSN == "" || !config.AutoMigrate {
		t.Fatalf("unexpected core config: %#v", config)
	}
	if config.AbandonInterval != 45*time.Second {
		t.Fatalf("unexpected abandon interval: %s", config.AbandonInterval)
	}
}

func TestProjectionDoesNotRebuildByDefault(t *testing.T) {
	t.Setenv("BKN_TRACE_PROJECTION_ENABLED", "true")
	t.Setenv("BKN_TRACE_PROJECTION_INDEX", "bkn-trace-core")
	t.Setenv("BKN_TRACE_PROJECTION_REBUILD_VERSION", "")

	config, err := NewCoreConfig()
	if err != nil {
		t.Fatalf("new core config: %v", err)
	}
	if config.ProjectionRebuildVersion != "" {
		t.Fatalf("projection rebuild must require an explicit version: %#v", config)
	}
	if config.ProjectionBootstrapVersion != "bkn-trace-core-v014-r1" {
		t.Fatalf("projection bootstrap must retain a versioned index: %#v", config)
	}
}

func TestCoreConfigParsesOneShotIdleTTL(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_ONE_SHOT_IDLE_TTL", "20m")

	config, err := NewCoreConfig()
	if err != nil {
		t.Fatalf("new core config: %v", err)
	}
	if config.OneShotIdleTTL != 20*time.Minute {
		t.Fatalf("unexpected one-shot idle TTL: %s", config.OneShotIdleTTL)
	}
}

func TestCoreConfigDefaultsMariaDBAutoMigrateToTrue(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_STORE", "mariadb")
	t.Setenv("BKN_TRACE_CORE_AUTO_MIGRATE", "")

	config, err := NewCoreConfig()
	if err != nil {
		t.Fatalf("new core config: %v", err)
	}
	if !config.AutoMigrate {
		t.Fatal("MariaDB auto migration must default to true")
	}
}

func TestCoreConfigRejectsInvalidAutoMigrate(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_AUTO_MIGRATE", "ture")

	if _, err := NewCoreConfig(); err == nil {
		t.Fatal("expected invalid auto-migrate setting to be rejected")
	}
}
