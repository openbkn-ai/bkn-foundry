// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import "testing"

func TestDurableAnswerConfigRequiresDurableStoreAndWorker(t *testing.T) {
	for _, test := range []struct {
		name, gate, core, evidence, projection string
		valid                                  bool
	}{
		{"default disabled", "", "memory", "memory", "false", true},
		{"enabled memory core", "true", "memory", "opensearch", "true", false},
		{"enabled memory evidence", "true", "mariadb", "memory", "true", false},
		{"enabled no worker", "true", "mariadb", "opensearch", "false", false},
		{"enabled durable pipeline", "true", "mariadb", "opensearch", "true", true},
		{"invalid gate", "maybe", "mariadb", "opensearch", "true", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BKN_TRACE_DURABLE_ANSWER_ENABLED", test.gate)
			t.Setenv("BKN_TRACE_CORE_STORE", test.core)
			t.Setenv("BKN_TRACE_CORE_MARIADB_DSN", "fixture@tcp(localhost:3306)/unused")
			t.Setenv("BKN_TRACE_EVIDENCE_STORE", test.evidence)
			t.Setenv("BKN_TRACE_PROJECTION_ENABLED", test.projection)
			config, err := NewCoreConfig()
			if (err == nil) != test.valid {
				t.Fatalf("config valid=%v err=%v", test.valid, err)
			}
			if err == nil && config.DurableAnswerEnabled != (test.gate == "true") {
				t.Fatalf("gate mismatch=%v", config.DurableAnswerEnabled)
			}
		})
	}
}
