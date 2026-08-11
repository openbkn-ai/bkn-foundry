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
	}
	for _, fragment := range required {
		if !strings.Contains(schema, fragment) {
			t.Errorf("schema is missing %q", fragment)
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
