// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ievidenceledger

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

var (
	ErrPayloadConflict   = errors.New("event payload conflicts with durable ledger")
	ErrSequenceConflict  = errors.New("producer sequence conflicts with durable ledger")
	ErrCausalityConflict = errors.New("event causality conflicts with durable ledger")
	ErrOwnerMismatch     = errors.New("evidence owner does not match trusted conversation owner")
)

type Store interface {
	Commit(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, error)
	ListInteractionEvents(ctx context.Context, owner sessionvo.Owner, interactionID string) ([]ledgervo.Event, error)
}

type KafkaCoordinate struct {
	Topic     string
	Partition int
	Offset    int64
}

type KafkaDecision string

const (
	KafkaAccepted     KafkaDecision = "accepted"
	KafkaDeduplicated KafkaDecision = "deduplicated"
	KafkaConflict     KafkaDecision = "conflict"
)

type KafkaResult struct {
	Decision   KafkaDecision
	Ack        ledgervo.DurableAck
	ReasonCode string
}

// KafkaStore resolves a ledger write and Kafka terminal coordinate atomically.
type KafkaStore interface {
	CommitKafka(context.Context, ledgervo.Event, KafkaCoordinate) (KafkaResult, error)
}
