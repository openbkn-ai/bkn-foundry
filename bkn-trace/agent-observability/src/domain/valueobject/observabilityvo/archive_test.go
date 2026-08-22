// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import (
	"testing"
	"time"
)

func TestArchiveCutoffUsesFixedRetentionAndDeploymentDay(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, time.August, 14, 16, 24, 0, 0, location)

	logRange := NewArchiveRange(ArchiveKindLog, now, location)
	if logRange.To != time.Date(2026, time.July, 15, 0, 0, 0, 0, location) {
		t.Fatalf("unexpected log cutoff: %s", logRange.To)
	}
	traceRange := NewArchiveRange(ArchiveKindTrace, now, location)
	if traceRange.To != time.Date(2026, time.August, 7, 0, 0, 0, 0, location) {
		t.Fatalf("unexpected trace cutoff: %s", traceRange.To)
	}
	if !traceRange.Contains(time.Date(2026, time.August, 6, 23, 59, 59, 0, location)) || traceRange.Contains(traceRange.To) {
		t.Fatalf("archive range must be half-open: %+v", traceRange)
	}
}

func TestArchiveKindHasOnlyTheTwoFixedRetentionPolicies(t *testing.T) {
	if ArchiveKindLog.RetentionDays() != 30 || ArchiveKindTrace.RetentionDays() != 7 {
		t.Fatalf("fixed retention drifted: log=%d trace=%d", ArchiveKindLog.RetentionDays(), ArchiveKindTrace.RetentionDays())
	}
	if ArchiveKind("custom").Valid() {
		t.Fatal("custom archive kind must not be accepted")
	}
}
