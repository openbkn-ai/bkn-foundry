package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/evidencemigration"
)

type fakeAdmin struct {
	activated  evidencemigration.ManifestArtifact
	actor      string
	closedID   string
	closure    string
	reconciled string
}

func (f *fakeAdmin) ActivateArtifact(_ context.Context, artifact evidencemigration.ManifestArtifact, actor string) error {
	f.activated, f.actor = artifact, actor
	return nil
}

func (f *fakeAdmin) ReconcileManifest(_ context.Context, manifestID string) error {
	f.reconciled = manifestID
	return nil
}

func (f *fakeAdmin) CloseManifest(_ context.Context, manifestID, actor string) (string, error) {
	f.closedID, f.actor = manifestID, actor
	return f.closure, nil
}

func TestActivateCommandPersistsArtifactAndPrintsActiveReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	input := `{"manifest_id":"mig-1","contract_sha":"0016ad359b11d162e04bb11a78784c33fad0ec8d","source_snapshot_at":"2026-09-25T10:00:00.000Z","entry_count":"1","entries_digest":"0123456789012345678901234567890123456789012345678901234567890123","entries":[{"classification":"coverage_gap","classification_reason":"abandoned","event_id":null,"manifest_id":"mig-1","payload_hash":null,"producer_epoch":null,"producer_id":null,"producer_sequence":null,"producer_stream_id":null,"source_primary_key":"1","source_service":"bkn-backend","source_status":"abandoned","source_table":"bkn_backend_trace_outbox"}]}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := &fakeAdmin{}
	var output strings.Builder
	if err := run(context.Background(), []string{"activate", "--manifest", path, "--actor", "owner"}, admin, &output); err != nil {
		t.Fatal(err)
	}
	if admin.actor != "owner" || admin.activated.ManifestID != "mig-1" || len(admin.activated.Entries) != 1 {
		t.Fatalf("activation input = %+v actor=%q", admin.activated, admin.actor)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(output.String()), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["manifest_id"] != "mig-1" || receipt["state"] != "active" || receipt["entries_digest"] != admin.activated.EntriesDigest {
		t.Fatalf("active receipt = %v", receipt)
	}
}

func TestActivateCommandRejectsUnknownArtifactFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(`{"manifest_id":"mig-1","unexpected":"payload"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"activate", "--manifest", path, "--actor", "owner"}, &fakeAdmin{}, &strings.Builder{}); err == nil {
		t.Fatal("expected strict artifact decoder to reject unknown fields")
	}
}

func TestActivateCommandRejectsMissingFrozenEntryFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	input := `{"manifest_id":"mig-1","contract_sha":"0016ad359b11d162e04bb11a78784c33fad0ec8d","source_snapshot_at":"2026-09-25T10:00:00.000Z","entry_count":"1","entries_digest":"0123456789012345678901234567890123456789012345678901234567890123","entries":[{"classification":"coverage_gap","classification_reason":"abandoned","event_id":null,"manifest_id":"mig-1","payload_hash":null,"producer_epoch":null,"producer_id":null,"producer_sequence":null,"producer_stream_id":null,"source_primary_key":"1","source_service":"bkn-backend","source_table":"bkn_backend_trace_outbox"}]}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := &fakeAdmin{}
	if err := run(context.Background(), []string{"activate", "--manifest", path, "--actor", "owner"}, admin, &strings.Builder{}); err == nil {
		t.Fatal("expected incomplete C1 entry to be rejected")
	}
	if admin.activated.ManifestID != "" {
		t.Fatal("incomplete entry reached activation store")
	}
}

func TestReconcileCloseCommandPrintsClosureReceipt(t *testing.T) {
	admin := &fakeAdmin{closure: "cff9384b03fc081f0217245075997d19e3e0d2ec57d9d0d8549a50f7d3931674"}
	var output strings.Builder
	if err := run(context.Background(), []string{"reconcile-close", "--manifest-id", "mig-1", "--actor", "owner"}, admin, &output); err != nil {
		t.Fatal(err)
	}
	if admin.reconciled != "mig-1" || admin.closedID != "mig-1" || admin.actor != "owner" || !strings.Contains(output.String(), admin.closure) {
		t.Fatalf("closure receipt=%q admin=%+v", output.String(), admin)
	}
}
