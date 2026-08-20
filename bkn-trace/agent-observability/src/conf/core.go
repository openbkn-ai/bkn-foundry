package conf

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type CoreConfig struct {
	Store                      string
	MariaDBDSN                 string
	AutoMigrate                bool
	AbandonInterval            time.Duration
	OneShotIdleTTL             time.Duration
	ProjectionEnabled          bool
	ProjectionIndex            string
	ProjectionInterval         time.Duration
	ProjectionBootstrapVersion string
	ProjectionRebuildVersion   string
	EvidenceCollectionState    string
}

func NewCoreConfig() (CoreConfig, error) {
	store := strings.ToLower(strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_STORE")))
	if store == "" {
		store = "memory"
	}
	interval := 30 * time.Second
	if configured := strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_ABANDON_INTERVAL")); configured != "" {
		if parsed, err := time.ParseDuration(configured); err == nil && parsed > 0 {
			interval = parsed
		}
	}
	oneShotIdleTTL := 15 * time.Minute
	if configured := strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_ONE_SHOT_IDLE_TTL")); configured != "" {
		if parsed, err := time.ParseDuration(configured); err == nil && parsed > 0 {
			oneShotIdleTTL = parsed
		}
	}
	autoMigrate := strings.EqualFold(store, "mariadb")
	if configured := strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_AUTO_MIGRATE")); configured != "" {
		parsed, err := strconv.ParseBool(configured)
		if err != nil {
			return CoreConfig{}, fmt.Errorf("parse BKN_TRACE_CORE_AUTO_MIGRATE: %w", err)
		}
		autoMigrate = parsed
	}
	projectionEnabled, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_ENABLED")))
	projectionInterval := time.Second
	if configured := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_INTERVAL")); configured != "" {
		if parsed, err := time.ParseDuration(configured); err == nil && parsed > 0 {
			projectionInterval = parsed
		}
	}
	projectionIndex := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_INDEX"))
	if projectionIndex == "" {
		projectionIndex = "bkn-trace-core"
	}
	projectionRebuildVersion := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_REBUILD_VERSION"))
	projectionBootstrapVersion := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_BOOTSTRAP_VERSION"))
	if projectionBootstrapVersion == "" {
		projectionBootstrapVersion = projectionIndex + "-v014-r1"
	}
	return CoreConfig{
		Store: store, MariaDBDSN: strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")),
		AutoMigrate: autoMigrate, AbandonInterval: interval, OneShotIdleTTL: oneShotIdleTTL,
		ProjectionEnabled: projectionEnabled, ProjectionIndex: projectionIndex,
		ProjectionInterval:         projectionInterval,
		ProjectionBootstrapVersion: projectionBootstrapVersion,
		ProjectionRebuildVersion:   projectionRebuildVersion,
		EvidenceCollectionState:    strings.TrimSpace(os.Getenv("BKN_TRACE_EVIDENCE_COLLECTION_STATE")),
	}, nil
}
