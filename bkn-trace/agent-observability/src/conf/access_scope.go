package conf

import (
	"os"
	"strings"
	"time"
)

type AccessScopeConfig struct {
	BKNBaseURL string
	Timeout    time.Duration
}

func NewAccessScopeConfig() AccessScopeConfig {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "http://bkn-safe:3000"
	}
	timeout := 3 * time.Second
	if configured, err := time.ParseDuration(strings.TrimSpace(os.Getenv("BKN_SAFE_ACCESS_TIMEOUT"))); err == nil && configured > 0 {
		timeout = configured
	}
	return AccessScopeConfig{BKNBaseURL: baseURL, Timeout: timeout}
}
