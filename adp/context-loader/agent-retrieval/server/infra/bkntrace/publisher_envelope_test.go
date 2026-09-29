package bkntrace

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type captureEventPublisher struct{ events []evidencepublisher.Event }

func (p *captureEventPublisher) TryPublish(event evidencepublisher.Event) evidencepublisher.PublishResult {
	p.events = append(p.events, event)
	return evidencepublisher.PublishResult{Disposition: evidencepublisher.Accepted}
}

// The envelope copy is no longer pre-sized with len(event)+1; it must still carry every event key
// plus the trusted owner, and must leave the caller's event untouched.
func TestPublishEvidenceEventEnvelopeCopiesEventAndAddsOwner(t *testing.T) {
	publisher := &captureEventPublisher{}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil) })

	event := Event{"event_id": "evt-1", "event_type": "retrieval.completed", "owner": "spoofed"}
	ec := eventContext{applicationID: "app-1", subjectType: "user", accountID: "acct-1", observedAt: "2026-09-23T00:00:00Z"}
	publishEvidenceEvent(event, ec)

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	var envelope map[string]any
	if err := json.Unmarshal(publisher.events[0].Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["event_id"] != "evt-1" || envelope["event_type"] != "retrieval.completed" {
		t.Fatalf("envelope lost event keys: %#v", envelope)
	}
	owner, ok := envelope["owner"].(map[string]any)
	if !ok || owner["application_principal_id"] != "app-1" || owner["effective_subject_id"] != "acct-1" {
		t.Fatalf("envelope owner = %#v, want trusted owner from request context", envelope["owner"])
	}
	if event["owner"] != "spoofed" || len(event) != 3 {
		t.Fatalf("caller event was mutated: %#v", event)
	}
}
