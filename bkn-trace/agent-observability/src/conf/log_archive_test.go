// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReloadLogArchiveLimitsAndRejectInvalidSnapshots(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_CANDIDATES", "10000")
	t.Setenv("BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_PAYLOAD_BYTES", "10485760")
	file := filepath.Join(t.TempDir(), "limits.json")
	t.Setenv("BKN_OBSERVABILITY_LOG_ARCHIVE_LIMITS_FILE", file)
	for _, raw := range []string{`{"max_candidates":200,"max_payload_bytes":1048576}`, `{"max_candidates":400}`} {
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		l, err := ReadLogArchiveLimits(context.Background())
		if err != nil || l.MaxCandidates < 200 || l.MaxCandidates > 400 {
			t.Fatalf("reload: %+v %v", l, err)
		}
	}
	for _, bad := range []string{`null`, `[]`, `{"max_candidates":0}`, `{"max_payload_bytes":1023}`, `{"max_candidates":10001}`, `{"unknown":1}`, `{} {}`} {
		if err := os.WriteFile(file, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLogArchiveLimits(context.Background()); err == nil {
			t.Fatalf("invalid limits accepted: %s", bad)
		}
	}
}
