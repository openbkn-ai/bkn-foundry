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

type fakeLedger struct {
	decision auditstore.Decision
	err      error
}

func (f fakeLedger) Append(context.Context, auditstore.Event) (auditstore.Decision, error) {
	return f.decision, f.err
}

type fakeCommitter struct {
	calls     int
	partition int
	offset    int64
}

func (f *fakeCommitter) Commit(context.Context, int, int64) error { f.calls++; return nil }

func validRecord() Record {
	return Record{Topic: Topic, Value: []byte(`{}`), Partition: 2, Offset: 40, BrokerTime: time.Now().UTC(), TimestampType: "LogAppendTime"}
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
