// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type coreIdempotencyRecord struct {
	Scope                   string          `json:"scope"`
	Owner                   sessionvo.Owner `json:"owner"`
	ExternalConversationKey string          `json:"external_conversation_key"`
	IdempotencyKey          string          `json:"idempotency_key"`
	RequestHash             string          `json:"request_hash"`
	ResourceType            string          `json:"resource_type"`
	ResourceID              string          `json:"resource_id"`
	CreatedAt               time.Time       `json:"created_at"`
}

func (v coreIdempotencyRecord) native() sessionvo.IdempotencyRecord {
	return sessionvo.IdempotencyRecord{Scope: v.Scope, Owner: v.Owner, ExternalConversationKey: v.ExternalConversationKey, IdempotencyKey: v.IdempotencyKey, RequestHash: v.RequestHash, ResourceType: v.ResourceType, ResourceID: v.ResourceID, CreatedAt: v.CreatedAt}
}

// The upgrade wire format carries persisted lifecycle fields excluded from the
// public API. It belongs only to this offline command.
type coreInteractionRecord struct {
	sessionvo.Interaction
	StartKey    string `json:"start_idempotency_key,omitempty"`
	TerminalKey string `json:"terminal_idempotency_key,omitempty"`
	PayloadHash string `json:"terminal_payload_hash,omitempty"`
}

func interactionRecord(v sessionvo.Interaction) coreInteractionRecord {
	return coreInteractionRecord{v, v.StartIdempotencyKey, v.TerminalIdempotencyKey, v.TerminalPayloadHash}
}

func (v coreInteractionRecord) native() sessionvo.Interaction {
	v.StartIdempotencyKey = v.StartKey
	v.TerminalIdempotencyKey = v.TerminalKey
	v.TerminalPayloadHash = v.PayloadHash
	return v.Interaction
}

type coreImportPlanJSON coreImportPlan

func (p coreImportPlan) MarshalJSON() ([]byte, error) {
	var interactions []coreInteractionRecord
	if p.Interactions != nil {
		interactions = make([]coreInteractionRecord, len(p.Interactions))
		for n, v := range p.Interactions {
			interactions[n] = interactionRecord(v)
		}
	}
	return json.Marshal(struct {
		*coreImportPlanJSON
		Interactions []coreInteractionRecord `json:"interactions"`
	}{(*coreImportPlanJSON)(&p), interactions})
}

func (p *coreImportPlan) UnmarshalJSON(data []byte) error {
	var decoded coreImportPlan
	wire := struct {
		*coreImportPlanJSON
		Interactions []coreInteractionRecord `json:"interactions"`
	}{coreImportPlanJSON: (*coreImportPlanJSON)(&decoded)}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.Interactions != nil {
		decoded.Interactions = make([]sessionvo.Interaction, len(wire.Interactions))
		for n, v := range wire.Interactions {
			decoded.Interactions[n] = v.native()
		}
	}
	*p = decoded
	return nil
}
