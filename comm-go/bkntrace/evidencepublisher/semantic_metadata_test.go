package evidencepublisher

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPublisherPreservesExplicitSemanticMetadata(t *testing.T) {
	event := publisherTestEvent()
	event.OperationID, event.Attempt = "op-1", 1
	// Decode the proposed optional transport fields so this regression also
	// compiles against the old Event contract, where they were silently lost.
	metadata := []byte(`{"business_refs":[{"ref_type":"object_type","ref_id":"object:kn:stock","version":"unversioned"}],"operation_business_edges":[{"operation_id":"op-1","business_ref":{"ref_type":"object_type","ref_id":"object:kn:stock","version":"unversioned"},"role":"read","observed_at":"2026-09-22T08:00:01Z"}],"causation_event_ids":["evt-parent"]}`)
	if err := json.Unmarshal(metadata, &event); err != nil {
		t.Fatal(err)
	}
	publisher, err := New(publisherTestConfig(), &fakeSender{})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	if result := publisher.TryPublish(event); result.Disposition != Accepted {
		t.Fatalf("publish = %#v", result)
	}
	var got, want map[string]any
	if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata, &want); err != nil {
		t.Fatal(err)
	}
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("Kafka record lost %s: got %#v, want %#v", key, got[key], value)
		}
	}
	if got["payload_hash"] != "2a1cf0c9bbb7146106feb3d0d5139e0bf67a6700e2585d77d20d78289dfafae6" {
		t.Fatal("optional metadata changed the envelope payload hash")
	}
}
