// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full
// license text.

package traceadmissionprocessor

import (
	"errors"
	"time"
)

// Config describes the internal policy and control endpoints.
type Config struct {
	PolicyURL        string        `mapstructure:"policy_url"`
	ConfigurationURL string        `mapstructure:"configuration_url"`
	HeartbeatURL     string        `mapstructure:"heartbeat_url"`
	AckURLBase       string        `mapstructure:"ack_url_base"`
	WorkloadIdentity string        `mapstructure:"workload_identity"`
	ProcessBootID    string        `mapstructure:"process_boot_id"`
	PollInterval     time.Duration `mapstructure:"poll_interval"`
	HTTPTimeout      time.Duration `mapstructure:"http_timeout"`
}

func (c *Config) setDefaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = 10 * time.Second
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 3 * time.Second
	}
}

func (c Config) validate() error {
	for name, value := range map[string]string{
		"policy_url": c.PolicyURL, "configuration_url": c.ConfigurationURL,
		"workload_identity": c.WorkloadIdentity,
	} {
		if value == "" {
			return errors.New(name + " is required")
		}
	}
	return nil
}
