// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestMonthlyTemplateV032MatchesCanonicalDigestAndCoordinates(t *testing.T) {
	template := TemplateSQL()
	sum := sha256.Sum256([]byte(template))
	if got := hex.EncodeToString(sum[:]); got != "52acd735cb9c9bd3442e9280c50de0f26676faadc829453ef99d0e53546979d7" {
		t.Fatalf("monthly v032 template digest = %s", got)
	}
	for _, fragment := range []string{
		"topic VARCHAR(249) NOT NULL",
		"partition_id INT NOT NULL",
		"offset_id BIGINT NOT NULL",
		"UNIQUE KEY uq_audit_kafka_coordinate (topic, partition_id, offset_id)",
	} {
		if !strings.Contains(template, fragment) {
			t.Errorf("monthly v032 template missing %q", fragment)
		}
	}
}
