// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"

// TraceFailureMetadataTransaction is optional so legacy stores retain their
// existing implementation. It selects only the first failed call's identity
// and Error payload per authorized trace/conversation/interaction, never input/output.
type TraceFailureMetadataTransaction interface {
	ListFirstTraceFailureMetadata([]TraceMetadataScope) []sessionvo.OperationCallFact
}
