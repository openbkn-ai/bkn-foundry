// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package v030

import (
	"strings"
	"testing"
)

func TestSchemaHasFrozenManifestLifecycleAndResultGuards(t *testing.T) {
	schema := SchemaSQL()
	for _, required := range []string{
		"bkn_trace_evidence_migration_manifests",
		"bkn_trace_evidence_migration_entries",
		"bkn_trace_evidence_migration_results",
		"bkn_trace_evidence_migration_result_conflicts",
		"bkn_trace_evidence_migration_manifest_audit",
		"active Evidence migration entries are immutable",
		"closed Evidence migration results are immutable",
		"active Evidence migration manifest core fields are immutable",
		"uq_evidence_manifest_source",
		"FOREIGN KEY (manifest_id, entry_id)",
	} {
		if !strings.Contains(schema, required) {
			t.Fatalf("missing Evidence v030 invariant %q", required)
		}
	}
	if strings.Contains(strings.ToUpper(schema), "DROP TABLE") {
		t.Fatal("v030 must not contain destructive migration SQL")
	}
}
