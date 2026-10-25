// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Covers statement text, parameter framing, fixed-width values and timestamps.
// The variable strings are counted separately, including the already JSON-
// encoded task; no second encoding or public artifact-limit reduction occurs.
const answerPacketOverhead = 4096

func (t *transaction) checkAnswerPacketBudget(m sessionvo.ProjectionMutation) error {
	var maxPacket uint64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT @@max_allowed_packet").Scan(&maxPacket); err != nil {
		return err
	}
	required := uint64(len(m.Payload)) + uint64(len(m.AggregateType)) + uint64(len(m.AggregateID)) + uint64(len(m.EventType)) + uint64(len(m.EventID)) + answerPacketOverhead
	if required > maxPacket {
		return &isessionstore.AnswerArtifactTooLargeError{PayloadBytes: uint64(len(m.Payload)), RequiredBytes: required, MaxPacketBytes: maxPacket}
	}
	return nil
}
