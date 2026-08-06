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

	config := NewCoreConfig()
	if config.Store != "mariadb" || config.MariaDBDSN == "" || !config.AutoMigrate {
		t.Fatalf("unexpected core config: %#v", config)
	}
	if config.AbandonInterval != 45*time.Second {
		t.Fatalf("unexpected abandon interval: %s", config.AbandonInterval)
	}
}

func TestProjectionUsesVersionedIndexBehindAliasByDefault(t *testing.T) {
	t.Setenv("BKN_TRACE_PROJECTION_ENABLED", "true")
	t.Setenv("BKN_TRACE_PROJECTION_INDEX", "bkn-trace-core")
	t.Setenv("BKN_TRACE_PROJECTION_REBUILD_VERSION", "")

	config := NewCoreConfig()
	if config.ProjectionRebuildVersion != "bkn-trace-core-v014" {
		t.Fatalf("projection could create a concrete alias index: %#v", config)
	}
}

func TestCoreConfigParsesOneShotIdleTTL(t *testing.T) {
	t.Setenv("BKN_TRACE_CORE_ONE_SHOT_IDLE_TTL", "20m")

	config := NewCoreConfig()
	if config.OneShotIdleTTL != 20*time.Minute {
		t.Fatalf("unexpected one-shot idle TTL: %s", config.OneShotIdleTTL)
	}
}
