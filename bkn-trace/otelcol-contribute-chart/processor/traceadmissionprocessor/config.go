// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full
// license text.

package traceadmissionprocessor

import (
	"encoding/base64"
	"errors"
	"time"
)

// Config describes the existing BKN Safe OAuth2 and agent-observability
// endpoints. ClientSecret is supplied from a mounted Secret through the
// Collector environment/config substitution; it is never generated here.
type Config struct {
	PolicyURL         string        `mapstructure:"policy_url"`
	ConfigurationURL  string        `mapstructure:"configuration_url"`
	HeartbeatURL      string        `mapstructure:"heartbeat_url"`
	AckURLBase        string        `mapstructure:"ack_url_base"`
	TokenURL          string        `mapstructure:"token_url"`
	ClientID          string        `mapstructure:"client_id"`
	ClientSecret      string        `mapstructure:"client_secret"`
	Scope             string        `mapstructure:"scope"`
	Audience          string        `mapstructure:"audience_cluster_id"`
	CurrentKeyID      string        `mapstructure:"current_key_id"`
	CurrentPublicKey  string        `mapstructure:"current_public_key"`
	PreviousKeyID     string        `mapstructure:"previous_key_id"`
	PreviousPublicKey string        `mapstructure:"previous_public_key"`
	WorkloadIdentity  string        `mapstructure:"workload_identity"`
	ProcessBootID     string        `mapstructure:"process_boot_id"`
	PollInterval      time.Duration `mapstructure:"poll_interval"`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
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
		"token_url": c.TokenURL, "client_id": c.ClientID, "client_secret": c.ClientSecret,
		"audience_cluster_id": c.Audience, "current_key_id": c.CurrentKeyID,
		"current_public_key": c.CurrentPublicKey, "workload_identity": c.WorkloadIdentity,
	} {
		if value == "" {
			return errors.New(name + " is required")
		}
	}
	if _, err := decodePublicKey(c.CurrentPublicKey); err != nil {
		return err
	}
	if c.PreviousPublicKey != "" {
		if _, err := decodePublicKey(c.PreviousPublicKey); err != nil {
			return err
		}
		if c.PreviousKeyID == "" {
			return errors.New("previous_key_id is required when previous_public_key is configured")
		}
	}
	return nil
}

func decodePublicKey(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("capture policy public key must be a base64 encoded Ed25519 public key")
	}
	return decoded, nil
}
