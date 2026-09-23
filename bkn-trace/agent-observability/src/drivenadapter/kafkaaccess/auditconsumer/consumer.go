// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

// Package auditconsumer contains the offset discipline for the Audit Ledger
// Writer. It intentionally has no API for reading a producer SASL principal.
package auditconsumer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
)

const Topic = "openbkn.audit.v1"

type Record struct {
	Topic         string
	Value         []byte
	Partition     int
	Offset        int64
	BrokerTime    time.Time
	TimestampType string
}

type Validator interface {
	Validate(context.Context, Record) (auditstore.Event, error)
}

type Ledger interface {
	Append(context.Context, auditstore.Event) (auditstore.Decision, error)
}

type OffsetCommitter interface {
	Commit(context.Context, int, int64) error
}

type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

var ErrWrongTopic = errors.New("audit record topic is not openbkn.audit.v1")
var ErrWrongTimestampType = errors.New("audit record timestamp type is not LogAppendTime")

type Consumer struct {
	validator Validator
	ledger    Ledger
	committer OffsetCommitter
}

func New(validator Validator, ledger Ledger, committer OffsetCommitter) (*Consumer, error) {
	if validator == nil || ledger == nil || committer == nil {
		return nil, errors.New("audit consumer dependencies are required")
	}
	return &Consumer{validator: validator, ledger: ledger, committer: committer}, nil
}

// Process handles one record. Permanent validation failures and dedup/conflict
// decisions are committed; ledger/dependency failures are returned so the
// caller pauses the partition and retries without advancing its offset.
func (c *Consumer) Process(ctx context.Context, record Record) error {
	if record.Topic != Topic {
		return c.commitPermanent(ctx, record, ErrWrongTopic)
	}
	if record.TimestampType != "LogAppendTime" {
		return c.commitPermanent(ctx, record, ErrWrongTimestampType)
	}
	event, err := c.validator.Validate(ctx, record)
	if err != nil {
		var permanent *PermanentError
		if errors.As(err, &permanent) {
			return c.commitPermanent(ctx, record, permanent)
		}
		return fmt.Errorf("validate audit record: %w", err)
	}
	decision, err := c.ledger.Append(ctx, event)
	if err != nil {
		return fmt.Errorf("append audit record: %w", err)
	}
	switch decision {
	case auditstore.DecisionInserted, auditstore.DecisionIdempotent, auditstore.DecisionConflict:
		return c.commit(ctx, record)
	default:
		return fmt.Errorf("unknown audit ledger decision %q", decision)
	}
}

func (c *Consumer) commitPermanent(ctx context.Context, record Record, reason error) error {
	if err := c.commit(ctx, record); err != nil {
		return fmt.Errorf("commit permanent audit rejection (%v): %w", reason, err)
	}
	return nil
}

func (c *Consumer) commit(ctx context.Context, record Record) error {
	return c.committer.Commit(ctx, record.Partition, record.Offset+1)
}
