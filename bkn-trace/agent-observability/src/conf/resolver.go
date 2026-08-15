package conf

import (
	"os"
	"strings"
	"time"
)

type BusinessResolverConfig struct {
	Enabled             bool
	BKNBaseURL          string
	VegaBaseURL         string
	ExecutionFactoryURL string
	ModelManagerURL     string
	Timeout             time.Duration
}

func NewBusinessResolverConfig() BusinessResolverConfig {
	bknURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_TRACE_BKN_RESOLVER_URL")), "/")
	vegaURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_TRACE_VEGA_RESOLVER_URL")), "/")
	executionFactoryURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_TRACE_EXECUTION_FACTORY_URL")), "/")
	modelManagerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_TRACE_MODEL_MANAGER_URL")), "/")
	timeout := 3 * time.Second
	if value := strings.TrimSpace(os.Getenv("BKN_TRACE_RESOLVER_TIMEOUT")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return BusinessResolverConfig{
		Enabled: bknURL != "" || vegaURL != "", BKNBaseURL: bknURL, VegaBaseURL: vegaURL, ExecutionFactoryURL: executionFactoryURL, ModelManagerURL: modelManagerURL, Timeout: timeout,
	}
}
