// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore_test

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestSchemaFreezesLifecycleAndDurableEvidenceConstraints(t *testing.T) {
	t.Parallel()

	schema := sessionstore.SchemaSQL()
	required := []string{
		"bkn_trace_conversations",
		"agent_name VARCHAR(128)",
		"actor_name_snapshot VARCHAR(255)",
		"creation_auth_method VARCHAR(32)",
		"uq_bkn_trace_conversation_generation",
		"uq_bkn_trace_conversation_current",
		"bkn_trace_interactions",
		"uq_bkn_trace_interaction_active",
		"bkn_trace_operations",
		"uq_bkn_trace_operation_key",
		"bkn_trace_receipts",
		"uq_bkn_trace_receipt_attempt",
		"bkn_trace_evidence_event_ledger",
		"uq_bkn_trace_event_stream_sequence",
		"started_at DATETIME(6) NOT NULL",
		"bkn_trace_projection_outbox",
		"bkn_trace_assembly_revisions",
		"bkn_trace_event_conflicts",
		"bkn_trace_projection_checkpoints",
		"bkn_trace_dlq",
		"bkn_trace_dlq_replay_audit",
		"bkn_trace_log_source_coverage",
		"PRIMARY KEY (source_id, deployment_id)",
		"dropped_records BIGINT UNSIGNED NOT NULL DEFAULT 0",
		"bkn_trace_ee_provenance_analyses",
		"idx_provenance_analysis_interaction",
		"ADD COLUMN locale VARCHAR(16) NOT NULL DEFAULT 'zh-CN'",
	}
	for _, fragment := range required {
		if !strings.Contains(schema, fragment) {
			t.Errorf("schema is missing %q", fragment)
		}
	}
}

func TestMigrationsAreOrderedAndChecksumProtected(t *testing.T) {
	expectedChecksums := map[string]string{
		"013": "b50a727e71cd7d6e61ad2795a965553359309d4cfbc58279ed08364eb2cf19c6",
		"014": "12ada5e84e6c1154dcb7e522804480d6a2c5d4cb2313c2a0883179d076e765e6",
		"015": "408e6cb3445f6116995da9852116f1795563f5ea17a65da667b6ec42a33dec2e",
		"016": "869da02928bed7950e7d0b2b3e609c806334a3839342e57631548f57b3ac1be4",
		"017": "f47e2ee9f70f0089c2cd225f5d28cc612b2f0c105d5ba001d55771636c02e349",
		"018": "e56a69e4f6ef6d750684067b6c6cb59cf57375bb4d7c862a5a487221898519f5",
	}
	migrations := sessionstore.Migrations()
	if len(migrations) != len(expectedChecksums) {
		t.Fatalf("expected %d embedded schema migrations, got %d", len(expectedChecksums), len(migrations))
	}
	for index, migration := range migrations {
		if migration.Version == "" || migration.Checksum == "" || migration.SQL == "" {
			t.Fatalf("migration %d is incomplete: %#v", index, migration)
		}
		if index > 0 && migrations[index-1].Version >= migration.Version {
			t.Fatalf("migrations are not strictly ordered: %q before %q", migrations[index-1].Version, migration.Version)
		}
		if expectedChecksum, found := expectedChecksums[migration.Version]; !found || migration.Checksum != expectedChecksum {
			t.Fatalf("migration %s checksum drifted: got %q want %q", migration.Version, migration.Checksum, expectedChecksum)
		}
	}
}

func TestSchemaEnforcesOneOperationCallFactPerAttempt(t *testing.T) {
	t.Parallel()

	tableDefinition := operationCallFactTableDefinition(t, sessionstore.SchemaSQL())
	required := []string{
		"operation_id VARCHAR(64)",
		"attempt_no INT UNSIGNED NOT NULL",
		"PRIMARY KEY (operation_id, attempt_no)",
		"INDEX idx_bkn_trace_operation_call_fact_interaction (interaction_id, started_at, operation_id, attempt_no)",
		"INDEX idx_bkn_trace_operation_call_fact_trace (trace_id, started_at, operation_id, attempt_no)",
	}
	for _, fragment := range required {
		if !strings.Contains(tableDefinition, fragment) {
			t.Errorf("operation call fact schema is missing %q", fragment)
		}
	}
}

func TestOperationCallFactSchemaStoresInputAndTerminalPayload(t *testing.T) {
	t.Parallel()

	tableDefinition := operationCallFactTableDefinition(t, sessionstore.SchemaSQL())
	required := []string{
		"conversation_id VARCHAR(64)",
		"interaction_id VARCHAR(64)",
		"receipt_id VARCHAR(64)",
		"tool_name VARCHAR(255) NOT NULL",
		"protocol VARCHAR(16)",
		"source_module VARCHAR(128)",
		"parent_operation_id VARCHAR(64)",
		"input_payload LONGTEXT NOT NULL",
		"output_payload LONGTEXT NULL",
		"error_payload LONGTEXT NULL",
		"request_id VARCHAR(128)",
		"trace_id VARCHAR(64)",
		"span_id VARCHAR(32)",
		"started_at DATETIME(6) NOT NULL",
		"finished_at DATETIME(6) NULL",
		"status VARCHAR(16)",
		"retryable BOOLEAN NOT NULL DEFAULT FALSE",
	}
	for _, fragment := range required {
		if !strings.Contains(tableDefinition, fragment) {
			t.Errorf("operation call fact schema is missing %q", fragment)
		}
	}
}

func TestLifecycleSchemasDoNotStoreOperationPayloadHashes(t *testing.T) {
	t.Parallel()

	schema := sessionstore.SchemaSQL()
	for _, tableName := range []string{"bkn_trace_operations", "bkn_trace_receipts"} {
		tableDefinition := lifecycleTableDefinition(t, schema, tableName)
		for _, columnName := range []string{"normalized_input_hash", "payload_hash"} {
			if strings.Contains(tableDefinition, columnName) {
				t.Errorf("%s must not store %s", tableName, columnName)
			}
		}
	}
}

func TestLifecycleSchemaDropsLegacyOperationPayloadHashColumns(t *testing.T) {
	t.Parallel()

	schema := strings.Join(strings.Fields(sessionstore.SchemaSQL()), " ")
	required := []string{
		"ALTER TABLE bkn_trace_operations DROP COLUMN IF EXISTS normalized_input_hash",
		"ALTER TABLE bkn_trace_receipts DROP COLUMN IF EXISTS normalized_input_hash",
		"ALTER TABLE bkn_trace_receipts DROP COLUMN IF EXISTS payload_hash",
	}
	for _, statement := range required {
		if !strings.Contains(schema, statement) {
			t.Errorf("schema is missing one-time legacy column removal %q", statement)
		}
	}
}

func operationCallFactTableDefinition(t *testing.T, schema string) string {
	t.Helper()
	return lifecycleTableDefinition(t, schema, "bkn_trace_operation_call_facts")
}

func lifecycleTableDefinition(t *testing.T, schema, tableName string) string {
	t.Helper()
	tableStart := strings.Index(schema, "CREATE TABLE IF NOT EXISTS "+tableName)
	if tableStart < 0 {
		t.Fatalf("schema is missing %s", tableName)
	}
	tableEnd := strings.Index(schema[tableStart:], ";")
	if tableEnd < 0 {
		t.Fatalf("%s table definition is not terminated", tableName)
	}
	return schema[tableStart : tableStart+tableEnd]
}
