// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import "time"

// RecordIntegrity is a current read of registered call records, independent
// of historical assembly state, execution success and claim support.
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
}
