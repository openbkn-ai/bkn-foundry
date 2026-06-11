package config

import (
	"os"
	"strconv"
)

type Config struct {
	Host                   string
	Port                   int
	OperatorIntegrationURL string
	DefaultBusinessDomain  string
	DefaultUserID          string
	Features               FeatureFlags
	MetricsEnabled         bool
	ServiceVersion         string
}

func Load() Config {
	port := 9002
	if raw := os.Getenv("PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			port = parsed
		}
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	baseURL := os.Getenv("OPERATOR_INTEGRATION_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9000"
	}

	bd := os.Getenv("DEFAULT_BUSINESS_DOMAIN")
	if bd == "" {
		bd = "bd_public"
	}

	userID := os.Getenv("DEFAULT_USER_ID")
	if userID == "" {
		userID = "capabilities-lab"
	}

	return Config{
		Host:                   host,
		Port:                   port,
		OperatorIntegrationURL: baseURL,
		DefaultBusinessDomain:  bd,
		DefaultUserID:          userID,
		Features:               LoadFeatureFlags(),
		MetricsEnabled:         envBool("LAB_METRICS_ENABLED", true),
		ServiceVersion:         "1.0.0",
	}
}
