// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import (
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
)

var ErrEvidenceConfirmationLimit = errors.New("evidence confirmation read budget exceeded")

// EvidenceConfirmationTransaction reads only the frozen expected IDs within
// the existing lifecycle transaction. Missing rows are not read errors.
type EvidenceConfirmationTransaction interface {
	ReadEvidenceEventsByIDs([]string) ([]ledgervo.Event, error)
}
