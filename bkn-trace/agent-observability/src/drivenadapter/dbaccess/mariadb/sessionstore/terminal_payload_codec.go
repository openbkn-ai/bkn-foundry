// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type storedTerminalPayload struct {
	sessionvo.PayloadEnvelope
	EvidenceCompletion *sessionvo.EvidenceCompletion `json:"_trace_evidence_completion,omitempty"`
}

func marshalTerminalPayload(payload *sessionvo.PayloadEnvelope, completion *sessionvo.EvidenceCompletion) string {
	if payload == nil {
		return ""
	}
	return marshalJSON(storedTerminalPayload{*payload, completion})
}

// Presence checks retain exact JSON keys without copying large inline values.
// The typed decode below continues to validate and merge recorded metadata.
type terminalPayloadFieldPresence struct{}

func (*terminalPayloadFieldPresence) UnmarshalJSON([]byte) error { return nil }

func decodeTerminalPayload(raw string) (*sessionvo.PayloadEnvelope, *sessionvo.EvidenceCompletion, error) {
	body := []byte(raw)
	var value storedTerminalPayload
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, nil, err
	}
	var fields map[string]terminalPayloadFieldPresence
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, nil, err
	}
	if _, present := fields["_trace_evidence_completion"]; present {
		c := value.EvidenceCompletion
		if c == nil || (c.OriginalDurability != sessionvo.DurabilityPending && c.OriginalDurability != sessionvo.DurabilityDurable && c.OriginalDurability != sessionvo.DurabilityFailed) {
			return nil, nil, fmt.Errorf("%w: call_fact.evidence_completion", isessionstore.ErrInvalidEvidenceJSON)
		}
		if c.RejectionReason != "" {
			hash, err := hex.DecodeString(c.RejectedExpectationHash)
			if err != nil || len(hash) != 32 || c.Expectation != nil {
				return nil, nil, fmt.Errorf("%w: call_fact.rejected_expectation", isessionstore.ErrInvalidEvidenceJSON)
			}
		} else if !sessionvo.EvidenceExpectationShapeValid(c.Expectation) {
			return nil, nil, fmt.Errorf("%w: call_fact.evidence_expectation", isessionstore.ErrInvalidEvidenceJSON)
		}
	}
	return &value.PayloadEnvelope, value.EvidenceCompletion, nil
}
