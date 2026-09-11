// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyRequiresReviewedManifestAndAssemblyEvidence(t *testing.T) {
	if _, err := execute(context.Background(), modeApply, "", "", time.Now()); err == nil || !strings.Contains(err.Error(), "requires an explicitly reviewed") {
		t.Fatalf("missing manifest error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(`{"ee":{"assembly":{"was_assembled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(context.Background(), modeApply, "", path, time.Now()); err == nil || !strings.Contains(err.Error(), "assembly.evidence_ref") {
		t.Fatalf("missing assembly evidence error = %v", err)
	}
}

func TestLoadManifestRejectsUnknownOrTrailingData(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"unknown", `{"unknown":true}`, "unknown field"},
		{"trailing", `{} {}`, "multiple JSON values"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadManifest(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loadManifest error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteReportProducesReviewableJSON(t *testing.T) {
	var output bytes.Buffer
	report := commandReport{Mode: modeDryRun, GeneratedAt: time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)}
	if err := writeReport(&output, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mode": "dry-run"`, `"core"`, `"enterprise"`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("report missing %s: %s", want, output.String())
		}
	}
}
