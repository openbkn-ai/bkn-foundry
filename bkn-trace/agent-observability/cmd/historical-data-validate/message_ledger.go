// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type historicalMessagePayload struct {
	QuestionArtifactRef string `json:"question_artifact_ref"`
	ContentHash         string `json:"content_hash"`
	IntentHash          string `json:"intent_hash"`
	Mode                string `json:"mode"`
	AgentID             string `json:"agent_id"`
}
type historicalMessageEvent struct {
	EventID          string                   `json:"event_id"`
	ConversationID   string                   `json:"conversation_id"`
	InteractionID    string                   `json:"interaction_id"`
	Owner            sessionvo.Owner          `json:"owner"`
	ProducerID       string                   `json:"producer_id"`
	ProducerStreamID string                   `json:"producer_stream_id"`
	ProducerEpoch    uint64                   `json:"producer_epoch"`
	ProducerSequence uint64                   `json:"producer_sequence"`
	StartedAt        historicalLedgerTime     `json:"started_at"`
	ObservedAt       historicalLedgerTime     `json:"observed_at"`
	EmittedAt        historicalLedgerTime     `json:"emitted_at"`
	IngestedAt       historicalLedgerTime     `json:"ingested_at"`
	EventType        string                   `json:"event_type"`
	Payload          historicalMessagePayload `json:"payload"`
}

// prepareMessageLedger converts stored human-message observations into native
// interaction-start evidence. It uses supplied source identities and times;
// durable import and Core ownership verification remain separate operations.
func prepareMessageLedger(reader io.Reader, writer io.Writer) error {
	var source struct {
		MessageEvents []historicalMessageEvent `json:"message_events"`
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected historical message input")
	}
	rows := make([]historicalLedgerRow, 0, len(source.MessageEvents))
	for _, message := range source.MessageEvents {
		questionHash, hashErr := hex.DecodeString(strings.TrimPrefix(message.Payload.ContentHash, "sha256:"))
		if hashErr != nil || len(questionHash) != 32 || !strings.HasPrefix(message.Payload.ContentHash, "sha256:") {
			return errors.New("invalid historical question content hash")
		}
		if message.EventType != "agent.interaction.started" || message.Payload.Mode != "chat" || message.Payload.AgentID == "" || message.Payload.IntentHash == "" || !strings.HasPrefix(message.Payload.QuestionArtifactRef, "artifact:") || len(message.Payload.QuestionArtifactRef) == len("artifact:") {
			return errors.New("invalid historical message observation")
		}
		started, observed, emitted, ingested := time.Time(message.StartedAt), time.Time(message.ObservedAt), time.Time(message.EmittedAt), time.Time(message.IngestedAt)
		if started.IsZero() || observed.IsZero() || emitted.IsZero() || ingested.IsZero() || observed.Before(started) || emitted.Before(observed) || !ingested.Equal(ingested.Truncate(time.Microsecond)) {
			return errors.New("invalid historical message timestamps")
		}
		payload, err := json.Marshal(struct {
			Payload historicalMessagePayload `json:"payload"`
		}{message.Payload})
		if err != nil {
			return err
		}
		event := ledgervo.Event{EventID: message.EventID, EventType: message.EventType, SchemaVersion: "3.0.0", Owner: message.Owner, ConversationID: message.ConversationID, InteractionID: message.InteractionID, ProducerID: message.ProducerID, ProducerStreamID: message.ProducerStreamID, ProducerEpoch: message.ProducerEpoch, ProducerSequence: message.ProducerSequence, CausalityStatus: "complete", StartedAt: started, ObservedAt: observed, EmittedAt: emitted, Envelope: payload}
		event.PayloadHash = ledgervo.CanonicalPayloadHash(event.Envelope)
		envelope, err := json.Marshal(event)
		if err != nil {
			return err
		}
		rows = append(rows, historicalLedgerRow{EventID: event.EventID, PayloadHash: event.PayloadHash, ImmutableRecordHash: ledgervo.ImmutableRecordHash(event), SchemaVersion: event.SchemaVersion, EventType: event.EventType, ConversationID: event.ConversationID, InteractionID: event.InteractionID, ProducerID: event.ProducerID, ProducerStreamID: event.ProducerStreamID, ProducerEpoch: event.ProducerEpoch, ProducerSequence: event.ProducerSequence, CausalityStatus: event.CausalityStatus, StartedAt: historicalLedgerTime(started.Truncate(time.Microsecond)), ObservedAt: historicalLedgerTime(observed.Truncate(time.Microsecond)), EmittedAt: historicalLedgerTime(emitted.Truncate(time.Microsecond)), IngestedAt: message.IngestedAt, Envelope: string(envelope)})
	}
	rows, err := prepareHistoricalLedger(rows)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(struct {
		Ledger []historicalLedgerRow `json:"ledger"`
	}{rows})
}
