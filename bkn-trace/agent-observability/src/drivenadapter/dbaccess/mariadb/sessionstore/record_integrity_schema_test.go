// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"strings"
	"testing"
)

func TestRecordIntegrityMigrationHasIndependentNullableMetadata(t *testing.T) {
	for _, migration := range Migrations() {
		if migration.Version == "035" {
			if !strings.Contains(migration.SQL, "record_integrity_version BIGINT UNSIGNED NOT NULL DEFAULT 0") || !strings.Contains(migration.SQL, "record_integrity_json LONGTEXT NULL") || !strings.Contains(migration.SQL, "record_integrity_pending") || !strings.Contains(migration.SQL, "idx_record_integrity_pending_interaction") || !strings.Contains(migration.SQL, "ADD COLUMN IF NOT EXISTS") {
				t.Fatalf("unsafe integrity migration: %s", migration.SQL)
			}
			return
		}
	}
	t.Fatal("v035 integrity metadata migration missing")
}
