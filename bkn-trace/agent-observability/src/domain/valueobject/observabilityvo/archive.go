// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import "time"

// ArchiveKind separates the two fixed operational retention policies. It is
// deliberately not a client configurable category or retention value.
type ArchiveKind string

const (
	ArchiveKindLog     ArchiveKind = "log"
	ArchiveKindTrace   ArchiveKind = "trace"
	LogRetentionDays               = 30
	TraceRetentionDays             = 7
)

func (kind ArchiveKind) Valid() bool { return kind == ArchiveKindLog || kind == ArchiveKindTrace }

func (kind ArchiveKind) RetentionDays() int {
	if kind == ArchiveKindTrace {
		return TraceRetentionDays
	}
	return LogRetentionDays
}

type ArchiveRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func (archiveRange ArchiveRange) Contains(value time.Time) bool {
	return (archiveRange.From.IsZero() || !value.Before(archiveRange.From)) && value.Before(archiveRange.To)
}

// NewArchiveRange deliberately has no caller-supplied range: records before
// the fixed cutoff are eligible, while records at the cutoff remain hot.
func NewArchiveRange(kind ArchiveKind, now time.Time, location *time.Location) ArchiveRange {
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	return ArchiveRange{To: day.AddDate(0, 0, -kind.RetentionDays())}
}

type ArchiveStatus string

const (
	ArchiveStatusCompleted         ArchiveStatus = "completed"
	ArchiveStatusFailed            ArchiveStatus = "failed"
	ArchiveStatusCleanupIncomplete ArchiveStatus = "cleanup_incomplete"
)

func (status ArchiveStatus) Terminal() bool {
	return status == ArchiveStatusCompleted || status == ArchiveStatusFailed || status == ArchiveStatusCleanupIncomplete
}
