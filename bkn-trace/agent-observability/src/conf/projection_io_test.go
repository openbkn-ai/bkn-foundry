// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"strings"
	"testing"
	"time"
)

func clearProjectionIOSettings(t *testing.T) {
	t.Helper()
	for _, name := range []string{"BKN_TRACE_PROJECTION_LEASE_IO_TIMEOUT", "BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", "BKN_TRACE_ANSWER_ACK_IO_TIMEOUT", "BKN_TRACE_PROJECTION_LOCK_DURATION"} {
		t.Setenv(name, "")
	}
}
func TestProjectionIOConfigurationDefaultsAndOverrides(t *testing.T) {
	clearProjectionIOSettings(t)
	cfg, err := NewCoreConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectionLeaseIOTimeout != 2*time.Second || cfg.AnswerTaskIOTimeout != 10*time.Second || cfg.AnswerAckIOTimeout != 2*time.Second || cfg.ProjectionLockDuration != 30*time.Second {
		t.Fatal("worker budgets were not wired into CoreConfig")
	}
	t.Setenv("BKN_TRACE_PROJECTION_LEASE_IO_TIMEOUT", "1s")
	t.Setenv("BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", "20s")
	t.Setenv("BKN_TRACE_ANSWER_ACK_IO_TIMEOUT", "3s")
	t.Setenv("BKN_TRACE_PROJECTION_LOCK_DURATION", "40s")
	cfg, err = NewCoreConfig()
	if err != nil || cfg.ProjectionLeaseIOTimeout != time.Second || cfg.AnswerTaskIOTimeout != 20*time.Second || cfg.AnswerAckIOTimeout != 3*time.Second || cfg.ProjectionLockDuration != 40*time.Second {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
}
func TestProjectionIOConfigurationRejectsInvalidAndExhaustedBudgets(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"BKN_TRACE_PROJECTION_LEASE_IO_TIMEOUT", "0s"},
		{"BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", "-1s"},
		{"BKN_TRACE_ANSWER_ACK_IO_TIMEOUT", "invalid"},
		{"BKN_TRACE_PROJECTION_LOCK_DURATION", "0s"},
		{"BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", "25s"}, // 2+25+2+1 equals the lease: reject.
		{"BKN_TRACE_PROJECTION_LOCK_DURATION", "500ms"},
		{"BKN_TRACE_ANSWER_TASK_IO_TIMEOUT", "2562047h47m16.854775807s"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			clearProjectionIOSettings(t)
			t.Setenv(tc.name, tc.value)
			if _, err := NewCoreConfig(); err == nil {
				t.Fatal("invalid lease budget accepted")
			}
		})
	}
}

func TestDurableAnswerRejectsInterpolationWithoutChangingLegacyDSN(t *testing.T) {
	clearProjectionIOSettings(t)
	t.Setenv("BKN_TRACE_CORE_STORE", "mariadb")
	t.Setenv("BKN_TRACE_EVIDENCE_STORE", "opensearch")
	t.Setenv("BKN_TRACE_PROJECTION_ENABLED", "true")
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "true")
	dsn := "fixture:private-dsn-value@tcp(localhost:3306)/unused?interpolateParams=true"
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", dsn)
	if _, err := NewCoreConfig(); err == nil {
		t.Fatal("durable answer accepted interpolated SQL")
	} else if strings.Contains(err.Error(), "private-dsn-value") || strings.Contains(err.Error(), "localhost") {
		t.Fatal("config error exposed the DSN")
	}
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "false")
	if cfg, err := NewCoreConfig(); err != nil || cfg.MariaDBDSN != dsn {
		t.Fatal("legacy interpolation configuration changed")
	}
	t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", "true")
	t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "fixture@tcp(localhost:3306)/unused")
	if _, err := NewCoreConfig(); err != nil {
		t.Fatalf("default prepared statements rejected: %v", err)
	}
}
