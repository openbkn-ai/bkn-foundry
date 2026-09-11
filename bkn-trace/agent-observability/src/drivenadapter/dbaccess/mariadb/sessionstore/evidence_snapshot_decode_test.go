// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestEvidenceSnapshotJSONDistinguishesInvalidFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		invalid   bool
	}{
		{"absent", "", false}, {"null", "null", false}, {"empty", "[]", false},
		{"refs", `["event-1"]`, false}, {"broken", `["private-text"`, true},
		{"wrong_type", `{"private-text":1}`, true}, {"partial_decode", `["event-1",42]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refs []string
			tx := &transaction{strictEvidenceJSON: true}
			err := tx.decodeEvidenceJSON("receipt.artifact_refs", tc.raw, &refs)
			if errors.Is(err, isessionstore.ErrInvalidEvidenceJSON) != tc.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil && (!strings.Contains(err.Error(), "receipt.artifact_refs") || strings.Contains(err.Error(), "private-text")) {
				t.Fatal("error must identify field without exposing stored content")
			}
			var legacy []string
			if err := (&transaction{}).decodeEvidenceJSON("receipt.artifact_refs", tc.raw, &legacy); err != nil {
				t.Fatal("legacy decoder behavior changed")
			}
		})
	}
}
