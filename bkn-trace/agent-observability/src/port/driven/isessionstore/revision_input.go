// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

var ErrRevisionInputConflict = errors.New("revision input conflicts with sealed record")

// Separate internal capability; the existing Transaction/Reader contracts stay unchanged.
type RevisionInputTransaction interface {
	SaveRevisionInput(sessionvo.SealedRevisionInput, int) error
}
type RevisionInputReader interface {
	ReadRevisionInput(context.Context, string, string, int) (sessionvo.SealedRevisionInput, bool, error)
}
