// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ledgersvc

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"

// ValidateHistoricalEvent performs the same pure validation as ledger ingestion.
// It does not establish database ownership, causality or durable admission.
func ValidateHistoricalEvent(event ledgervo.Event) error {
	return validateEvent(event)
}
