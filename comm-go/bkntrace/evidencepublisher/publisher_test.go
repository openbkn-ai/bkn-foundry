package evidencepublisher

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSender struct {
	mu       sync.Mutex
	records  []Record
	failures int
}

type blockingSender struct{}

func (blockingSender) Send(ctx context.Context, _ Record) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *fakeSender) Send(_ context.Context, record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return errors.New("broker unavailable")
	}
	s.records = append(s.records, record)
	return nil
}

func publisherTestConfig() Config {
	return Config{
		Topic:                 "openbkn.evidence.v1",
		ProducerID:            "bkn-backend",
		BaseStreamID:          "bkn-backend",
		WorkloadIdentity:      "spiffe://cluster.local/ns/openbkn/sa/bkn-backend",
		ProcessBootID:         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		CapturePolicyRevision: "41",
		QueueMaxRecords:       2,
		QueueMaxBytes:         4096,
		MaxRecordBytes:        1 << 20,
		MaxAge:                time.Second,
		MaxAttempts:           3,
		RetryBackoff:          time.Millisecond,
		ShutdownTimeout:       time.Second,
	}
}

func publisherTestEvent() Event {
	return Event{
		EventID:        "evt-publisher-test",
		EventType:      "object_type.get.observed",
		SchemaVersion:  "3.0.0",
		ConversationID: "conv-1",
		InteractionID:  "int-1",
		StartedAt:      "2026-09-22T08:00:00Z",
		ObservedAt:     "2026-09-22T08:00:01Z",
		EmittedAt:      "2026-09-22T08:00:02Z",
		Envelope:       json.RawMessage(`{"event":{"bkn.trace.schema.version":"2.1.0","event_type":"object_type.get.observed"},"owner":{"application_principal_id":"bkn-backend","effective_subject_type":"service","effective_subject_id":"svc-1"}}`),
	}
}

func TestTryPublishBuildsFrozenRecordAndUsesGoLedgerHash(t *testing.T) {
	sender := &fakeSender{}
	publisher, err := New(publisherTestConfig(), sender)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())

	result := publisher.TryPublish(publisherTestEvent())
	if result.Disposition != Accepted || result.EventID != "evt-publisher-test" {
		t.Fatalf("TryPublish() = %+v, want accepted event", result)
	}
	record := publisher.SnapshotQueue()[0]
	if record.Key != "bkn-backend:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("record key = %q", record.Key)
	}
	if got := record.Header("producer_instance_id"); got != "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("instance header = %q", got)
	}
	var value map[string]any
	if err := json.Unmarshal(record.Value, &value); err != nil {
		t.Fatal(err)
	}
	if value["producer_sequence"] != float64(1) || value["producer_epoch"] != float64(1) {
		t.Fatalf("identity fields = %#v", value)
	}
	if value["payload_hash"] != "2a1cf0c9bbb7146106feb3d0d5139e0bf67a6700e2585d77d20d78289dfafae6" {
		t.Fatalf("payload_hash = %v", value["payload_hash"])
	}
}

func TestTryPublishIsFailOpenWhenQueueIsFullAndDoesNotConsumeSequence(t *testing.T) {
	sender := &fakeSender{}
	cfg := publisherTestConfig()
	cfg.QueueMaxRecords = 1
	publisher, err := New(cfg, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	first := publisher.TryPublish(publisherTestEvent())
	secondEvent := publisherTestEvent()
	secondEvent.EventID = "evt-publisher-full"
	second := publisher.TryPublish(secondEvent)
	if first.Disposition != Accepted || second.Disposition != Dropped || second.Reason != ReasonQueueFull {
		t.Fatalf("results = %+v, %+v", first, second)
	}
	if got := publisher.NextSequence(); got != 2 {
		t.Fatalf("next sequence = %d, want 2", got)
	}
}

func TestPublisherRetriesFiniteFailuresAndReportsDropped(t *testing.T) {
	sender := &fakeSender{failures: 3}
	cfg := publisherTestConfig()
	cfg.MaxAttempts = 3
	publisher, err := New(cfg, sender)
	if err != nil {
		t.Fatal(err)
	}
	result := publisher.TryPublish(publisherTestEvent())
	if result.Disposition != Accepted {
		t.Fatalf("TryPublish() = %+v", result)
	}
	ack := publisher.Close(context.Background())
	if ack.Published != 0 || ack.Dropped != 1 || ack.QueueEmpty != true {
		t.Fatalf("close ack = %+v", ack)
	}
}

func TestPublisherRejectsNonCanonicalCapturePolicyRevision(t *testing.T) {
	cfg := publisherTestConfig()
	cfg.CapturePolicyRevision = "041"
	if _, err := New(cfg, &fakeSender{}); err == nil {
		t.Fatal("New() accepted a capture policy revision with a leading zero")
	}
}

func TestPublisherCloseHonorsShutdownTimeout(t *testing.T) {
	cfg := publisherTestConfig()
	cfg.ShutdownTimeout = 10 * time.Millisecond
	cfg.MaxAttempts = 2
	cfg.RetryBackoff = time.Millisecond
	publisher, err := New(cfg, blockingSender{})
	if err != nil {
		t.Fatal(err)
	}
	if result := publisher.TryPublish(publisherTestEvent()); result.Disposition != Accepted {
		t.Fatalf("TryPublish() = %+v", result)
	}
	started := time.Now()
	ack := publisher.Close(context.Background())
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Close() took %s, timeout was %s", elapsed, cfg.ShutdownTimeout)
	}
	if ack.Dropped != 1 || ack.QueueEmpty != true {
		t.Fatalf("close ack = %+v", ack)
	}
}

func TestPublisherFlushSendsWithoutClosing(t *testing.T) {
	sender := &fakeSender{}
	publisher, err := New(publisherTestConfig(), sender)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	if publisher.TryPublish(publisherTestEvent()).Disposition != Accepted {
		t.Fatal("publish rejected")
	}
	ack := publisher.Flush(context.Background())
	if ack.Published != 1 || !ack.QueueEmpty || len(sender.records) != 1 {
		t.Fatalf("flush ack=%+v records=%d", ack, len(sender.records))
	}
	second := publisherTestEvent()
	second.EventID = "evt-flush-second"
	if publisher.TryPublish(second).Disposition != Accepted {
		t.Fatal("publisher closed after flush")
	}
}

func TestPublisherFlushHonorsContextTimeoutAndCanRetry(t *testing.T) {
	publisher, err := New(publisherTestConfig(), blockingSender{})
	if err != nil {
		t.Fatal(err)
	}
	if publisher.TryPublish(publisherTestEvent()).Disposition != Accepted {
		t.Fatal("publish rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	ack := publisher.Flush(ctx)
	if ack.Dropped != 1 || !ack.QueueEmpty {
		t.Fatalf("flush ack=%+v", ack)
	}
	if publisher.TryPublish(publisherTestEvent()).Disposition != Accepted {
		t.Fatal("publisher closed after timeout flush")
	}
}

func TestPublisherFlushConcurrentCallsAreSafe(t *testing.T) {
	sender := &fakeSender{}
	publisher, err := New(publisherTestConfig(), sender)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	if publisher.TryPublish(publisherTestEvent()).Disposition != Accepted {
		t.Fatal("publish rejected")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); publisher.Flush(context.Background()) }()
	}
	wg.Wait()
	if len(sender.records) != 1 {
		t.Fatalf("records=%d, want 1", len(sender.records))
	}
}
