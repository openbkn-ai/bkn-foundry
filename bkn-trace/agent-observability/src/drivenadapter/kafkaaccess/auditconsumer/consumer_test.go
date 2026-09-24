// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

package auditconsumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
)

type fakeValidator struct{ err error }

func (f fakeValidator) Validate(context.Context, Record) (auditstore.Event, error) {
	return auditstore.Event{EventID: "evt-1"}, f.err
}

type countingValidator struct{ calls int }

func (v *countingValidator) Validate(context.Context, Record) (auditstore.Event, error) {
	v.calls++
	return auditstore.Event{EventID: "evt-1"}, nil
}

type fakeLedger struct {
	decision auditstore.Decision
	err      error
}

func (f fakeLedger) Append(context.Context, auditstore.Event) (auditstore.Decision, error) {
	return f.decision, f.err
}

type fakeCommitter struct {
	calls int
	order *[]string
}

func (f *fakeCommitter) Commit(context.Context, int, int64) error {
	f.calls++
	if f.order != nil {
		*f.order = append(*f.order, "commit")
	}
	return nil
}

type orderedLedger struct {
	decision auditstore.Decision
	order    *[]string
}

func (l orderedLedger) Append(context.Context, auditstore.Event) (auditstore.Decision, error) {
	*l.order = append(*l.order, "ledger")
	return l.decision, nil
}

func validRecord() Record {
	return Record{Topic: Topic, Value: []byte(`{}`), Partition: 2, Offset: 40, BrokerTime: time.Now().UTC()}
}

func TestProcessCommitsOnlyAfterLedgerDecision(t *testing.T) {
	committer := &fakeCommitter{}
	consumer, err := New(fakeValidator{}, fakeLedger{decision: auditstore.DecisionInserted}, committer)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Process(context.Background(), validRecord()); err != nil {
		t.Fatal(err)
	}
	if committer.calls != 1 {
		t.Fatalf("commit calls = %d", committer.calls)
	}
}

func TestProcessUsesBrokerTimeWithoutPerRecordTimestampType(t *testing.T) {
	validator := &countingValidator{}
	committer := &fakeCommitter{}
	consumer, err := New(validator, fakeLedger{decision: auditstore.DecisionInserted}, committer)
	if err != nil {
		t.Fatal(err)
	}
	record := validRecord()
	if err := consumer.Process(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if validator.calls != 1 || committer.calls != 1 {
		t.Fatalf("record without per-record timestamp type did not reach validation and terminal commit (validate=%d commit=%d)", validator.calls, committer.calls)
	}
}

func TestTemporaryLedgerFailureDoesNotCommit(t *testing.T) {
	committer := &fakeCommitter{}
	consumer, err := New(fakeValidator{}, fakeLedger{err: errors.New("db unavailable")}, committer)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Process(context.Background(), validRecord()); err == nil {
		t.Fatal("temporary failure was swallowed")
	}
	if committer.calls != 0 {
		t.Fatalf("commit calls = %d", committer.calls)
	}
}

func TestPermanentValidationFailureCommitsRejectedOffset(t *testing.T) {
	committer := &fakeCommitter{}
	consumer, err := New(fakeValidator{err: &PermanentError{Err: errors.New("schema invalid")}}, fakeLedger{}, committer)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Process(context.Background(), validRecord()); err != nil {
		t.Fatal(err)
	}
	if committer.calls != 1 {
		t.Fatalf("commit calls = %d", committer.calls)
	}
}

func TestIdempotentAndConflictLedgerResultsAreTerminalBeforeOffsetCommit(t *testing.T) {
	for _, decision := range []auditstore.Decision{auditstore.DecisionIdempotent, auditstore.DecisionConflict} {
		t.Run(string(decision), func(t *testing.T) {
			order := []string{}
			committer := &fakeCommitter{order: &order}
			consumer, err := New(fakeValidator{}, orderedLedger{decision: decision, order: &order}, committer)
			if err != nil {
				t.Fatal(err)
			}
			if err := consumer.Process(context.Background(), validRecord()); err != nil {
				t.Fatal(err)
			}
			if len(order) != 2 || order[0] != "ledger" || order[1] != "commit" || committer.calls != 1 {
				t.Fatalf("ledger/offset ordering = %v (commit calls %d)", order, committer.calls)
			}
		})
	}
}
