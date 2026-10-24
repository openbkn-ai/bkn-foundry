// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"

// TraceMetadataScope is an already authorized Core trace scope. Empty legacy
// conversation/interaction IDs match only empty persisted IDs, never a wildcard.
type TraceMetadataScope struct {
	TraceID        string
	ConversationID string
	InteractionID  string
}

// TraceSourceMetadataTransaction reads only the explicitly authorized tuples.
type TraceSourceMetadataTransaction interface {
	ListFirstTraceSourceMetadata([]TraceMetadataScope) []sessionvo.OperationCallFact
}
