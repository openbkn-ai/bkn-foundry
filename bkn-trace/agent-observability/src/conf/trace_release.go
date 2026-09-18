package conf

import (
	"os"
	"strings"
	"time"
)

type TraceReleaseConfig struct {
	StatePath    string
	ManifestPath string
	Namespace    string
	PollInterval time.Duration
}

func NewTraceReleaseConfig() TraceReleaseConfig {
	statePath := valueOrDefault("BKN_TRACE_RELEASE_STATE_PATH", "/var/lib/openbkn/trace-evidence/state.json")
	manifestPath := valueOrDefault("BKN_TRACE_RELEASE_MANIFEST_PATH", "/etc/openbkn/trace-evidence/services.yaml")
	namespace := valueOrDefault("BKN_TRACE_RELEASE_NAMESPACE", "openbkn")
	pollInterval := time.Second
	if raw := strings.TrimSpace(os.Getenv("BKN_TRACE_RELEASE_POLL_INTERVAL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			pollInterval = parsed
		}
	}
	return TraceReleaseConfig{StatePath: statePath, ManifestPath: manifestPath, Namespace: namespace, PollInterval: pollInterval}
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
