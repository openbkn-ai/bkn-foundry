// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"log/slog"
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
	if value := strings.TrimSpace(os.Getenv("BKN_SAFE_ACCESS_TIMEOUT")); value != "" {
		configured, err := time.ParseDuration(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid BKN Safe access timeout; using default", "value", value, "default", timeout)
		} else {
			timeout = configured
		}
	}
	return AccessScopeConfig{BKNBaseURL: baseURL, Timeout: timeout}
}
