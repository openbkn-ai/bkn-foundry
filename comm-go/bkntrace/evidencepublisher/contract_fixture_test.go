package evidencepublisher

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCanonicalPayloadHashMatchesLedgerGolden(t *testing.T) {
	raw := json.RawMessage(`{"event_id":"evt-1711","payload":{"definition":{"zeta":"a<b>&c","alpha":9007199254740993,"mid":{"name":"n","code":"c"},"ratio":0.5}},"event_type":"ontology.schema.snapshot"}`)
	got, err := canonicalPayloadHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	const want = "42210712b1261e4939024f526fa34b419faf2058ab44afc75e8aae249a4ec4a6"
	if got != want {
		t.Fatalf("canonicalPayloadHash() = %s, want %s", got, want)
	}
}

func TestTryPublishHeaderSetIsExactAndOrdered(t *testing.T) {
	publisher, err := New(publisherTestConfig(), &fakeSender{})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	if result := publisher.TryPublish(publisherTestEvent()); result.Disposition != Accepted {
		t.Fatalf("TryPublish() = %+v", result)
	}
	headers := publisher.SnapshotQueue()[0].Headers
	if len(headers) != 5 {
		t.Fatalf("header count = %d", len(headers))
	}
	want := []Header{
		{Key: "content-type", Value: "application/json"},
		{Key: "bkn-trace-schema-version", Value: "3.0.0"},
		{Key: "capture_policy_revision", Value: "41"},
		{Key: "producer_instance_id", Value: "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{Key: "bkn-evidence-record-class", Value: "live"},
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Fatalf("header[%d] = %+v, want %+v", i, headers[i], want[i])
		}
	}
}
