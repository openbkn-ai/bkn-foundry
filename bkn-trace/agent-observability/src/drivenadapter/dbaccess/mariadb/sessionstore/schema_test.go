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
	}
	for _, fragment := range required {
		if !strings.Contains(schema, fragment) {
			t.Errorf("schema is missing %q", fragment)
		}
	}
}
