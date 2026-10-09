// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import "time"

// RecordIntegrity describes registered call records independently of execution
// success, historical assembly state and claim support.
type RecordIntegrity struct {
	Status    string          `json:"status"`
	CheckedAt time.Time       `json:"checked_at"`
	Scope     string          `json:"scope"`
	Missing   []MissingRecord `json:"missing"`
}

type MissingRecord struct {
	OperationID string `json:"operation_id"`
	Attempt     uint32 `json:"attempt"`
	ToolName    string `json:"tool_name,omitempty"`
	Reason      string `json:"reason"`
	Field       string `json:"field"`
	// DropReason preserves the recorded publisher outcome for missing result evidence.
	DropReason string `json:"drop_reason,omitempty"`
}

// StoredRecordIntegrity is derived metadata, never a lifecycle state. A nil
// record is uncomputed; Applicable=false records a completed non-applicable check.
type StoredRecordIntegrity struct {
	SourceVersion uint64           `json:"source_version"`
	Applicable    bool             `json:"applicable"`
	Report        *RecordIntegrity `json:"report,omitempty"`
}

func CopyStoredRecordIntegrity(value *StoredRecordIntegrity) *StoredRecordIntegrity {
	if value == nil {
		return nil
	}
	result := *value
	if value.Report != nil {
		report := *value.Report
		if report.Missing != nil {
			report.Missing = append([]MissingRecord{}, report.Missing...)
		}
		result.Report = &report
	}
	return &result
}
