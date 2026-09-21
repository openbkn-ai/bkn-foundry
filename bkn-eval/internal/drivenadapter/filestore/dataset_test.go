// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ds.json")
	raw := `{"dataset_id":"probe","version":"1","network":{"name":"n"},"cases":[],"extra":true}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Datasets{}).Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadValidatesAfterDecoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ds.json")
	raw := `{"dataset_id":"probe","version":"1","network":{"name":"n"},"cases":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Datasets{}).Load(path); err == nil || !strings.Contains(err.Error(), "at least one case") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestShippedDatasetsAreValid(t *testing.T) {
	paths, err := filepath.Glob("../../../datasets/*/dataset.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped datasets found")
	}
	for _, path := range paths {
		if _, err := (Datasets{}).Load(path); err != nil {
			t.Errorf("%v", err)
		}
	}
}
