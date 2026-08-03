package conf

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type ObservabilityConfig struct {
	CursorSigningKey     []byte
	SourceTimeout        time.Duration
	MaxConcurrentSources int
}

func NewObservabilityConfig() ObservabilityConfig {
	sourceTimeout := 3 * time.Second
	if value := strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_TIMEOUT")); value != "" {
		configured, err := time.ParseDuration(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid observability source timeout; using default", "value", value, "default", sourceTimeout)
		} else {
			sourceTimeout = configured
		}
	}
	maxConcurrentSources := 4
	if value := strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES")); value != "" {
		configured, err := strconv.Atoi(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid observability source concurrency; using default", "value", value, "default", maxConcurrentSources)
		} else {
			maxConcurrentSources = configured
		}
	}
	return ObservabilityConfig{
		CursorSigningKey:     []byte(os.Getenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY")),
		SourceTimeout:        sourceTimeout,
		MaxConcurrentSources: maxConcurrentSources,
	}
}
