package accesslog

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

type kafkaPublisherStub struct {
	disposition auditpublisher.Disposition
	values      [][]byte
}

func (stub *kafkaPublisherStub) TryPublish(value []byte) auditpublisher.Disposition {
	stub.values = append(stub.values, append([]byte(nil), value...))
	return stub.disposition
}

func TestKafkaRecorderPublishesExistingAccessFactFailOpen(t *testing.T) {
	publisher := &kafkaPublisherStub{disposition: auditpublisher.Accepted}
	var outcomes []string
	recorder := NewKafkaRecorder(publisher, "test", func(outcome string) {
		outcomes = append(outcomes, outcome)
	})
	if err := recorder.Record(context.Background(), Entry{
		ActorID: "user-1", AuthMethod: "password", SourceChannel: "web",
		Action: "login", Outcome: "success", RequestID: "req-safe-access-success",
	}); err != nil {
		t.Fatal(err)
	}
	if len(publisher.values) != 1 {
		t.Fatalf("published values = %d, want 1", len(publisher.values))
	}
	publisher.disposition = auditpublisher.DroppedQueueFull
	if err := recorder.Record(context.Background(), Entry{
		Action: "logout", Outcome: "success", RequestID: "req-safe-access-drop",
	}); err == nil {
		t.Fatal("dropped publisher must be reported without failing caller semantics")
	}
	if len(publisher.values) != 2 {
		t.Fatalf("one access fact must have one publish attempt, values = %d", len(publisher.values))
	}
	if got, want := outcomes, []string{"accepted", "dropped_queue_full"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("bounded producer outcomes = %v, want %v", got, want)
	}
}
