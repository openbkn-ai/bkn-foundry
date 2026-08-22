// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import "time"

type RequestSummary struct {
	RequestID      string    `json:"request_id"`
	ConversationID string    `json:"conversation_id"`
	InteractionID  string    `json:"interaction_id"`
	OperationCount int       `json:"operation_count"`
	ReceiptCount   int       `json:"receipt_count"`
	TraceIDs       []string  `json:"trace_ids,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}
