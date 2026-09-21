// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/domain/valueobject/datasetvo"
)

// fakeDatasets stands in for a driven adapter: "good" loads, anything else fails.
type fakeDatasets struct{}

func (fakeDatasets) Load(ref string) (*datasetvo.Dataset, error) {
	if ref == "good" {
		return &datasetvo.Dataset{DatasetID: "probe", Version: "1", Cases: make([]datasetvo.Case, 3)}, nil
	}
	return nil, errors.New(ref + ": not found")
}

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"no command", nil, 2, "usage"},
		{"help", []string{"help"}, 0, "usage"},
		{"unknown", []string{"nope"}, 2, "unknown command"},
		{"planned", []string{"run"}, 2, "not implemented yet"},
		{"validate without refs", []string{"validate"}, 2, "at least one dataset"},
		{"validate reports failure", []string{"validate", "missing"}, 1, "missing: not found"},
		{"validate good dataset", []string{"validate", "good"}, 0, "probe@1  3 cases"},
		{"validate mixed", []string{"validate", "good", "missing"}, 1, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tc.args, &stdout, &stderr, Deps{Datasets: fakeDatasets{}})
			if code != tc.code {
				t.Fatalf("exit %d, want %d (stderr: %s)", code, tc.code, stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), tc.out) {
				t.Fatalf("output %q does not contain %q", stdout.String()+stderr.String(), tc.out)
			}
		})
	}
}
