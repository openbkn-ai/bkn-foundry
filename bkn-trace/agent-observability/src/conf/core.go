// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type CoreConfig struct {
	Store                         string
	MariaDBDSN                    string
	AutoMigrate                   bool
	AbandonInterval               time.Duration
	OneShotIdleTTL                time.Duration
	ProjectionEnabled             bool
	ProjectionIndex               string
	ProjectionInterval            time.Duration
	ProjectionBootstrapVersion    string
	ProjectionRebuildVersion      string
	ProjectionGrantIssuer         string
	ProjectionGrantKeyID          string
	ProjectionGrantAudience       string
	ProjectionGrantPrivateKey     ed25519.PrivateKey
	ProjectionGrantTTL            time.Duration
	EvidenceCollectionState       string
	MaxOperationsPerInteraction   int
	MaxClaimsPerInteraction       int
	MaxEvidenceRefsPerInteraction int
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
	projectionGrantIssuer := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_ISSUER"))
	projectionGrantKeyID := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_KEY_ID"))
	projectionGrantAudience := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_AUDIENCE"))
	projectionGrantTTL := 5 * time.Minute
	if configured := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_TTL")); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 {
			return CoreConfig{}, fmt.Errorf("BKN_TRACE_PROJECTION_GRANT_TTL must be a positive duration")
		}
		projectionGrantTTL = parsed
	}
	projectionGrantPrivateKey, err := projectionGrantPrivateKeyFromEnv()
	if err != nil {
		return CoreConfig{}, err
	}
	if len(projectionGrantPrivateKey) > 0 && (projectionGrantIssuer == "" || projectionGrantKeyID == "" || projectionGrantAudience == "") {
		return CoreConfig{}, fmt.Errorf("projection grant issuer, key ID, and audience are required when a private key is configured")
	}
	projectionBootstrapVersion := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_BOOTSTRAP_VERSION"))
	if projectionBootstrapVersion == "" {
		projectionBootstrapVersion = projectionIndex + "-v015-r1"
	}
	maxOperationsPerInteraction, err := positiveIntEnv("BKN_TRACE_CORE_MAX_OPERATIONS_PER_INTERACTION", 256)
	if err != nil {
		return CoreConfig{}, err
	}
	maxClaimsPerInteraction, err := positiveIntEnv("BKN_TRACE_CORE_MAX_CLAIMS_PER_INTERACTION", 32)
	if err != nil {
		return CoreConfig{}, err
	}
	maxEvidenceRefsPerInteraction, err := positiveIntEnv("BKN_TRACE_CORE_MAX_EVIDENCE_REFS_PER_INTERACTION", 4096)
	if err != nil {
		return CoreConfig{}, err
	}
	if maxClaimsPerInteraction > maxOperationsPerInteraction {
		return CoreConfig{}, fmt.Errorf("BKN_TRACE_CORE_MAX_CLAIMS_PER_INTERACTION must not exceed BKN_TRACE_CORE_MAX_OPERATIONS_PER_INTERACTION")
	}
	if maxEvidenceRefsPerInteraction < maxOperationsPerInteraction {
		return CoreConfig{}, fmt.Errorf("BKN_TRACE_CORE_MAX_EVIDENCE_REFS_PER_INTERACTION must be at least BKN_TRACE_CORE_MAX_OPERATIONS_PER_INTERACTION")
	}
	return CoreConfig{
		Store: store, MariaDBDSN: strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")),
		AutoMigrate: autoMigrate, AbandonInterval: interval, OneShotIdleTTL: oneShotIdleTTL,
		ProjectionEnabled: projectionEnabled, ProjectionIndex: projectionIndex,
		ProjectionInterval:            projectionInterval,
		ProjectionBootstrapVersion:    projectionBootstrapVersion,
		ProjectionRebuildVersion:      projectionRebuildVersion,
		ProjectionGrantIssuer:         projectionGrantIssuer,
		ProjectionGrantKeyID:          projectionGrantKeyID,
		ProjectionGrantAudience:       projectionGrantAudience,
		ProjectionGrantPrivateKey:     projectionGrantPrivateKey,
		ProjectionGrantTTL:            projectionGrantTTL,
		EvidenceCollectionState:       strings.TrimSpace(os.Getenv("BKN_TRACE_EVIDENCE_COLLECTION_STATE")),
		MaxOperationsPerInteraction:   maxOperationsPerInteraction,
		MaxClaimsPerInteraction:       maxClaimsPerInteraction,
		MaxEvidenceRefsPerInteraction: maxEvidenceRefsPerInteraction,
	}, nil
}

func projectionGrantPrivateKeyFromEnv() (ed25519.PrivateKey, error) {
	configured := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_PRIVATE_KEY"))
	if configured == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(configured)
	if err != nil {
		return nil, fmt.Errorf("decode BKN_TRACE_PROJECTION_GRANT_PRIVATE_KEY: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("BKN_TRACE_PROJECTION_GRANT_PRIVATE_KEY must be an Ed25519 private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	configured := strings.TrimSpace(os.Getenv(name))
	if configured == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(configured)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}
