package bkntrace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestSubmitEventsPreservesAuthoritativeBusinessGraph(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval", WorkloadIdentity: "agent-retrieval",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41",
	}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); publisher.Close(context.Background()) })
	ctx := withRequestDerivedBusinessRefs(testTraceContext(), []BusinessRef{{
		RefType: "object_type", RefID: "object:kn-1:stock", Version: "v3",
	}})
	event := Event{
		"event_id": "evt-business", "event_type": "retrieval.completed",
		"conversation_id": "conv-1", "interaction_id": "int-1", "operation_id": "op-1", "attempt": 1,
		"observed_at": "2026-09-23T00:00:00Z", "causation_event_id": "evt-parent",
		"payload": map[string]any{"source_refs": []map[string]any{
			{"ref_type": "object", "ref_id": "object:kn-1:stock", "version_status": "unversioned"},
			{"ref_type": "knowledge_network", "ref_id": "kn:kn-1", "version_status": "unversioned"},
		}},
	}
	if err := SubmitEvents(ctx, nil, nil, []Event{event}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		BusinessRefs []trace30BusinessRef   `json:"business_refs"`
		Edges        []trace30OperationEdge `json:"operation_business_edges"`
		Causes       []string               `json:"causation_event_ids"`
	}
	if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.BusinessRefs) != 2 || len(got.Edges) != 2 {
		t.Fatalf("accepted Kafka event cannot form its business graph: refs=%#v edges=%#v", got.BusinessRefs, got.Edges)
	}
	if got.BusinessRefs[0].RefType != "object_type" || got.BusinessRefs[0].Version != "v3" {
		t.Fatalf("lost canonical request-derived target/version: %#v", got.BusinessRefs)
	}
	for _, edge := range got.Edges {
		if edge.OperationID != "op-1" || edge.Role != "read" || edge.ObservedAt != "2026-09-23T00:00:00Z" {
			t.Fatalf("business edge changed execution attribution: %#v", edge)
		}
	}
	if len(got.Causes) != 1 || got.Causes[0] != "evt-parent" {
		t.Fatalf("lost event causality: %#v", got.Causes)
	}
}

func TestPublishedBusinessGraphDoesNotInventOperationOrTrustDeclaredRefs(t *testing.T) {
	for _, tc := range []struct {
		name, operationID string
		wantEdges         int
	}{
		{"interaction artifact", "", 0}, {"managed operation", "op-1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, err := evidencepublisher.New(evidencepublisher.Config{
				ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval", WorkloadIdentity: "agent-retrieval",
				ProcessBootID: "boot-1", CapturePolicyRevision: "41",
			}, &captureEvidenceSender{})
			if err != nil {
				t.Fatal(err)
			}
			SetEvidencePublisher(publisher)
			t.Cleanup(func() { SetEvidencePublisher(nil); publisher.Close(context.Background()) })
			result := publishEvidenceEvent(Event{
				"event_id": "evt-boundary", "event_type": "retrieval.completed", "operation_id": tc.operationID,
				"interaction_id": "int-1", "attempt": 1, "observed_at": "2026-10-22T08:00:00Z",
				"business_refs": []map[string]any{{"ref_type": "object_type", "ref_id": "object:forged:secret", "version": "v1"}},
				"owner":         map[string]string{"effective_subject_id": "forged"},
				"payload":       map[string]any{},
			}, eventContext{conversationID: "conv-1", applicationID: "app-1", subjectType: "user", accountID: "user-1",
				businessRefs: []BusinessRef{{RefType: "object_type", RefID: "object:kn-1:stock", Version: "v3"}},
			})
			if result.Disposition != evidencepublisher.Accepted {
				t.Fatalf("publish: %#v", result)
			}
			var record struct {
				BusinessRefs []trace30BusinessRef   `json:"business_refs"`
				Edges        []trace30OperationEdge `json:"operation_business_edges"`
				Envelope     struct {
					Owner map[string]string `json:"owner"`
				} `json:"envelope"`
			}
			if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &record); err != nil {
				t.Fatal(err)
			}
			if len(record.BusinessRefs) != 1 || record.BusinessRefs[0].RefID != "object:kn-1:stock" || len(record.Edges) != tc.wantEdges {
				t.Fatalf("untrusted ref or invented operation: %#v", record)
			}
			if record.Envelope.Owner["effective_subject_id"] != "user-1" {
				t.Fatalf("untrusted owner: %#v", record.Envelope.Owner)
			}
		})
	}
}

func TestTrace30BusinessRefsCanonicalContract(t *testing.T) {
	for _, tc := range []struct {
		kind, id string
		valid    bool
	}{
		{"knowledge_network", "kn:kn-1", true},
		{"object", "object:kn-1:stock", true},
		{"relation", "relation:kn-1:supplies", true},
		{"action", "action_type:kn-1:buy", true},
		{"object_instance", `object_instance:kn-1:stock:{"id":"a:b"}`, true},
		{"action_instance", "action_instance:kn-1:buy:run:1", true},
		{"property", "property:kn-1:stock:qty", true},
		{"logic", "logic:kn-1:stock:capacity", true},
		{"data_resource", "resource:stock-view", true},
		{"function", "function:kn-1:capacity", true},
		{"metric", "metric:kn-1:capacity", true},
		{"metric", "metric:kn-1:capacity:v2", true},
		{"property", "property:kn-1:stock:qty:unexpected", false},
		{"property", "property:kn-1:stock:", false},
		{"property", "property:kn-1:qty", false},
		{"object_type", "object:kn-1:stock:unexpected", false},
		{"object_type", "object:kn-1:", false},
		{"relation_type", "object:kn-1:stock", false},
		{"object_instance", "object_instance:kn-1:stock:", false},
		{"action_instance", "action_instance:kn-1:buy", false},
		{"data_resource", "resource:view:unexpected", false},
		{"metric", "metric:kn-1:capacity:v0", false},
		{"metric", "metric:kn-1:capacity:latest", false},
		{"metric", "metric:kn-1:capacity:v1:extra", false},
		{"unknown", "unknown:kn-1:x", false},
	} {
		t.Run(tc.kind+"/"+tc.id, func(t *testing.T) {
			// Both producer payload references and request-derived references
			// must respect the consumer's exact canonical shape.
			for _, derived := range []bool{false, true} {
				event := Event{"payload": map[string]any{"source_refs": []map[string]any{{"ref_type": tc.kind, "ref_id": tc.id}}}}
				var refs []BusinessRef
				if derived {
					event = Event{}
					refs = []BusinessRef{{RefType: tc.kind, RefID: tc.id}}
				}
				got := trace30BusinessRefs(event, refs)
				if (len(got) == 1) != tc.valid || len(got) > 1 {
					t.Fatalf("derived=%v valid=%v refs=%#v", derived, tc.valid, got)
				}
			}
		})
	}
}

func TestPublishedBusinessGraphKeepsInvalidRefsOnlyInEnvelope(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval", WorkloadIdentity: "agent-retrieval",
		ProcessBootID: "boot-1", CapturePolicyRevision: "41",
	}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(publisher)
	t.Cleanup(func() { SetEvidencePublisher(nil); publisher.Close(context.Background()) })
	result := publishEvidenceEvent(Event{
		"event_id": "evt-invalid-ref", "event_type": "retrieval.completed", "operation_id": "op-1",
		"interaction_id": "int-1", "attempt": 1, "observed_at": "2026-10-22T08:00:00Z",
		"payload": map[string]any{"field_refs": []map[string]any{
			{"ref_type": "property", "ref_id": "property:kn-1:stock:qty"},
			{"ref_type": "property", "ref_id": "property:kn-1:stock:qty:invalid"},
		}},
	}, eventContext{conversationID: "conv-1", applicationID: "app-1", subjectType: "user", accountID: "user-1"})
	if result.Disposition != evidencepublisher.Accepted {
		t.Fatalf("invalid optional ref discarded the event: %#v", result)
	}
	var got struct {
		BusinessRefs []trace30BusinessRef   `json:"business_refs"`
		Edges        []trace30OperationEdge `json:"operation_business_edges"`
		Envelope     struct {
			Payload struct {
				Fields []map[string]any `json:"field_refs"`
			} `json:"payload"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(publisher.SnapshotQueue()[0].Value, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.BusinessRefs) != 1 || len(got.Edges) != 1 || len(got.Envelope.Payload.Fields) != 2 {
		t.Fatalf("invalid ref must not reject event or rewrite envelope: %#v", got)
	}
}
