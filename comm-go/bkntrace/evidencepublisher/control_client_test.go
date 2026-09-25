package evidencepublisher

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestControlClientSendsFrozenHeartbeatAndAck(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if got := request.Header.Get("Authorization"); got != "Bearer workload-token" {
			t.Fatalf("Authorization = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.String() != "https://trace.internal/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat" {
				t.Fatalf("unexpected heartbeat request: %s %s", request.Method, request.URL)
			}
			if got, want := string(body), `{"instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}`; got != want {
				t.Fatalf("heartbeat body = %s; want %s", got, want)
			}
		case 2:
			if request.Method != http.MethodPost || request.URL.String() != "https://trace.internal/api/agent-observability/v1/internal/trace-evidence/operations/op-42:publisher-ack" {
				t.Fatalf("unexpected ACK request: %s %s", request.Method, request.URL)
			}
			if got, want := string(body), `{"producer_instance_id":"spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-1","capture_policy_revision":42,"last_accepted_sequence":7,"published":5,"dropped":2,"queue_empty":true,"acknowledged_at":"2026-09-25T08:00:00.000Z"}`; got != want {
				t.Fatalf("ack body = %s; want %s", got, want)
			}
		default:
			t.Fatalf("unexpected extra control request")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	control, err := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Heartbeat(context.Background(), "spiffe://cluster.local/ns/openbkn/sa/bkn-backend", "boot-1", 42); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	ack := DrainResult{ProducerInstanceID: "spiffe://cluster.local/ns/openbkn/sa/bkn-backend#boot-1", CapturePolicyRevision: "42", LastAcceptedSequence: 7, Published: 5, Dropped: 2, QueueEmpty: true}
	if err := control.Acknowledge(context.Background(), ConfigurationOperation{ID: "op-42", Revision: 42}, ack, time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
}

func TestControlClientDoesNotSendMismatchedAck(t *testing.T) {
	control, err := NewControlClient(ControlClientConfig{
		BaseURL: "https://trace.internal",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("control endpoint called for mismatched ACK")
			return nil, nil
		})},
		TokenSource: staticTokenSource("workload-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ack := DrainResult{ProducerInstanceID: "producer#boot", CapturePolicyRevision: "41", LastAcceptedSequence: 7, Published: 5, Dropped: 2, QueueEmpty: true}
	if err := control.Acknowledge(context.Background(), ConfigurationOperation{ID: "op-42", Revision: 42}, ack, time.Now()); err == nil {
		t.Fatal("Acknowledge() error = nil; want revision mismatch rejected")
	}
}

func TestControlClientAcknowledgementUsesUTCMilliseconds(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"producer_instance_id":"producer#boot","capture_policy_revision":42,"last_accepted_sequence":0,"published":0,"dropped":0,"queue_empty":true,"acknowledged_at":"2026-09-25T00:00:00.123Z"}`; got != want {
			t.Fatalf("ack body = %s; want %s", got, want)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	control, err := NewControlClient(ControlClientConfig{BaseURL: "https://trace.internal", HTTPClient: client, TokenSource: staticTokenSource("workload-token")})
	if err != nil {
		t.Fatal(err)
	}
	ack := DrainResult{ProducerInstanceID: "producer#boot", CapturePolicyRevision: "42", QueueEmpty: true}
	acknowledgedAt := time.Date(2026, time.September, 25, 8, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	if err := control.Acknowledge(context.Background(), ConfigurationOperation{ID: "op-42", Revision: 42}, ack, acknowledgedAt); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
}
