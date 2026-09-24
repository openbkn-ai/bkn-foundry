// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package ievidenceadmission

import (
	"context"
	"time"
)

type ClosureWatermark struct {
	InstanceID           string
	Revision             uint64
	LastAcceptedSequence uint64
	ClosedAt             string
}

type Snapshot struct {
	Revision           uint64
	Enabled            bool
	InstanceID         string
	RegisteredRevision uint64
	ProcessBootID      string
	Closure            ClosureWatermark
}

type Header struct {
	Key   string
	Value string
}

type Record struct {
	Topic            string
	Key              string
	Value            []byte
	Headers          []Header
	Partition        int
	Offset           int64
	BrokerTime       time.Time
	ProducerStreamID string
	ProducerSequence uint64
}

type RejectionDetails struct {
	EventID     string
	PayloadHash string
	ProducerID  string
	MigrationID string
	ReasonCode  string
}

// ReadOnlySource resolves only persisted historical C1 admission facts. It is
// deliberately read-only; policy and closure writes belong to the C1 control
// plane and are not reimplemented by C6.
type ReadOnlySource interface {
	Lookup(ctx context.Context, policyRevision uint64, producerInstanceID string) (Snapshot, error)
}

type RejectionWriter interface {
	RecordKafkaRejection(context.Context, Record, RejectionDetails) error
}
