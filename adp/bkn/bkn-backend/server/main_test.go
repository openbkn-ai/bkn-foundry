package main

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type flushCaptureSender struct{ records chan evidencepublisher.Record }

func (s flushCaptureSender) Send(_ context.Context, record evidencepublisher.Record) error {
	s.records <- record
	return nil
}

func TestEvidenceFlushLoopDeliversQueuedRecord(t *testing.T) {
	records := make(chan evidencepublisher.Record, 1)
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "bkn-backend", BaseStreamID: "bkn-backend", WorkloadIdentity: "bkn-backend",
		ProcessBootID: "test-boot", CapturePolicyRevision: "1",
	}, flushCaptureSender{records: records})
	if err != nil {
		t.Fatal(err)
	}
	if publisher.TryPublish(evidencepublisher.Event{EventID: "evt-flush", EventType: "test.event", Envelope: []byte(`{"ok":true}`)}).Disposition != evidencepublisher.Accepted {
		t.Fatal("event was not accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := startEvidenceFlushLoop(ctx, publisher, time.Millisecond)
	select {
	case <-records:
	case <-time.After(time.Second):
		t.Fatal("flush loop did not deliver queued record")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flush loop did not stop")
	}
}
