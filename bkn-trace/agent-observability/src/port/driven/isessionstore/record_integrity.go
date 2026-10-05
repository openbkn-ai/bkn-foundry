// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const MaxStoredRecordIntegrityBytes = 512 << 10
const MaxRecordIntegrityDirtyIDs = 1000

var ErrRecordIntegrityLimit = errors.New("stored record integrity exceeds metadata limit")
var ErrRecordIntegrityDirtyLimit = errors.New("record integrity transaction exceeds dirty interaction limit")

// RecordIntegrityStore is an optional internal capability. Callers resolve
// canonical authorization before evaluating/saving; candidates are worker-only
// identities, not an authorized list. Source versions come from the actual
// consistent snapshot. CAS accepts only the first result for that source.
type RecordIntegrityStore interface {
	SaveStoredRecordIntegrity(context.Context, string, sessionvo.StoredRecordIntegrity) (bool, error)
	ListRecordIntegrityCandidates(context.Context, string, int) ([]sessionvo.Interaction, error)
	InvalidateRecordIntegrity(context.Context, string, sessionvo.Owner) error
}

func MarshalStoredRecordIntegrity(stored sessionvo.StoredRecordIntegrity) ([]byte, error) {
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxStoredRecordIntegrityBytes {
		return nil, ErrRecordIntegrityLimit
	}
	return raw, nil
}
