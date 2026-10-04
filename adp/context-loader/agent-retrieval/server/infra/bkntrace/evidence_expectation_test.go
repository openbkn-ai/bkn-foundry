// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func expectationTestPublisher(t *testing.T, maxRecords int) *evidencepublisher.Publisher {
	t.Helper()
	p, err := evidencepublisher.New(evidencepublisher.Config{ProducerID: "real-producer-override", BaseStreamID: "retrieval", WorkloadIdentity: "retrieval", ProcessBootID: "boot", CapturePolicyRevision: "41", QueueMaxRecords: maxRecords, QueueMaxBytes: 1 << 24, MaxRecordBytes: 1 << 20}, &captureEvidenceSender{})
	if err != nil {
		t.Fatal(err)
	}
	SetEvidencePublisher(p)
	t.Cleanup(func() { SetEvidencePublisher(nil); p.Close(context.Background()) })
	return p
}
func submitExpectationEvents(t *testing.T, ctx context.Context, events []Event) {
	t.Helper()
	if err := SubmitEvents(ctx, nil, nil, events); err != nil {
		t.Fatal(err)
	}
}
func expectationEvent(id string) Event {
	return Event{"event_id": id, "event_type": "retrieval.completed"}
}
func TestEvidenceExpectationKeepsAllAcceptedAndDropped(t *testing.T) {
	p := expectationTestPublisher(t, 1)
	ctx := withEvidenceOutcome(testTraceContext())
	submitExpectationEvents(t, ctx, []Event{expectationEvent("one"), expectationEvent("two")})
	e := freezeEvidenceExpectation(ctx)
	if !e.Closed || len(e.Events) != 2 || e.Events[0].PublishDisposition != "accepted" || e.Events[1].DropReason != "queue_full" {
		t.Fatalf("expectation=%+v", e)
	}
	if e.Events[0].PayloadHash == "" || e.Events[0].ProducerID != "real-producer-override" || len(p.SnapshotQueue()) != 1 {
		t.Fatal("expected actual publisher identity")
	}
	if evidenceExpectationDurability(e) != "failed" {
		t.Fatal("partial accepted must not be pending/durable")
	}
}
func TestEvidenceExpectationNilSerializationAndClosedPublisher(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		SetEvidencePublisher(nil)
		ctx := withEvidenceOutcome(testTraceContext())
		submitExpectationEvents(t, ctx, []Event{expectationEvent("one")})
		e := freezeEvidenceExpectation(ctx)
		if len(e.Events) != 1 || e.Events[0].DropReason != "publisher_unavailable" {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("serialization", func(t *testing.T) {
		expectationTestPublisher(t, 2)
		ctx := withEvidenceOutcome(testTraceContext())
		event := expectationEvent("one")
		event["invalid"] = func() {}
		submitExpectationEvents(t, ctx, []Event{event})
		e := freezeEvidenceExpectation(ctx)
		if e.Events[0].DropReason != "serialization_failed" || e.Events[0].PayloadHash != "" {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("closed", func(t *testing.T) {
		p := expectationTestPublisher(t, 2)
		p.Close(context.Background())
		ctx := withEvidenceOutcome(testTraceContext())
		submitExpectationEvents(t, ctx, []Event{expectationEvent("one")})
		if e := freezeEvidenceExpectation(ctx); e.Events[0].DropReason != "publisher_closing" {
			t.Fatalf("%+v", e)
		}
	})
}
func TestEvidenceExpectationFreezeParallelAndNested(t *testing.T) {
	expectationTestPublisher(t, 128)
	parent := withEvidenceOutcome(testTraceContext())
	child := withEvidenceOutcome(parent)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			submitExpectationEvents(t, child, []Event{expectationEvent(fmt.Sprint(i))})
		}(i)
	}
	wg.Wait()
	e := freezeEvidenceExpectation(child)
	if len(e.Events) != 20 || !e.Closed {
		t.Fatalf("child=%+v", e)
	}
	if p := freezeEvidenceExpectation(parent); len(p.Events) != 0 || p.Events == nil {
		t.Fatalf("parent=%+v", p)
	}
	if err := SubmitEvents(child, nil, nil, []Event{expectationEvent("after-close")}); !errors.Is(err, ErrEvidenceExpectationClosed) {
		t.Fatalf("late event error=%v", err)
	}
	e.Events[0].EventID = "tampered"
	again := freezeEvidenceExpectation(child)
	if len(again.Events) != 20 || again.Events[0].EventID == "tampered" {
		t.Fatal("freeze mutated or accepted late event")
	}
}
func TestEvidenceExpectationLimitCannotBecomeDurable(t *testing.T) {
	expectationTestPublisher(t, 256)
	ctx := withEvidenceOutcome(testTraceContext())
	events := make([]Event, 129)
	for i := range events {
		events[i] = expectationEvent(fmt.Sprint(i))
	}
	submitExpectationEvents(t, ctx, events)
	e := freezeEvidenceExpectation(ctx)
	if e.Closed || len(e.Events) != 128 || evidenceExpectationDurability(e) != "failed" {
		t.Fatalf("overflow=%+v", e)
	}
}

func TestEvidenceExpectationDeduplicatesExactAndRejectsConflictingIDs(t *testing.T) {
	expectationTestPublisher(t, 4)
	ctx := withEvidenceOutcome(testTraceContext())
	submitExpectationEvents(t, ctx, []Event{expectationEvent("one"), expectationEvent("one")})
	if e := freezeEvidenceExpectation(ctx); len(e.Events) != 1 || !e.Closed {
		t.Fatalf("exact duplicate=%+v", e)
	}
	ctx = withEvidenceOutcome(testTraceContext())
	changed := expectationEvent("two")
	changed["different"] = true
	submitExpectationEvents(t, ctx, []Event{expectationEvent("two"), changed})
	if e := freezeEvidenceExpectation(ctx); e.Closed || evidenceExpectationDurability(e) != "failed" {
		t.Fatalf("conflicting duplicate=%+v", e)
	}
}

func TestInteractionArtifactUsesCallerCancellation(t *testing.T) {
	t.Setenv(envArtifactEndpoint, "http://artifact.invalid")
	old := artifactHTTPClient
	defer func() { artifactHTTPClient = old }()
	artifactHTTPClient = &http.Client{Transport: evidenceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return lifecycleJSONResponse(200, nil), nil
	})}
	ctx, cancel := context.WithCancel(testTraceContext())
	cancel()
	_, err := RecordInteractionArtifact(ctx, "conv", "int", InteractionArtifactQuestion, "question")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled artifact call err=%v", err)
	}
}

func TestLifecycleErrorRetainsHTTPStatusForNonJSONOutage(t *testing.T) {
	client := lifecycleClientWithTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("upstream unavailable")), Header: make(http.Header)}, nil
	})
	apiErr, err := client.Call(trustedLifecycleTestContext(), "GET", "/health", nil, nil)
	if err != nil || apiErr == nil || apiErr.HTTPStatus != 503 || !IsTraceInfrastructureFailure(apiErr, err) {
		t.Fatalf("api=%+v err=%v", apiErr, err)
	}
}
func TestGuardFinishIncludesFrozenContractAndPartialDroppedFailure(t *testing.T) {
	expectationTestPublisher(t, 1)
	ctx := withEvidenceOutcome(testTraceContext())
	submitExpectationEvents(t, ctx, []Event{expectationEvent("one"), expectationEvent("two")})
	var body struct {
		EvidenceExpectation *EvidenceExpectation `json:"evidence_expectation"`
		EvidenceDurability  string               `json:"evidence_durability"`
	}
	client := lifecycleClientWithTransport(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return lifecycleJSONResponse(200, OperationResult{}), nil
	})
	_, apiErr, err := NewGuard(client).Finish(ctx, pendingGuardState(), map[string]any{"result": "ok"}, false, false)
	if err != nil || apiErr != nil || body.EvidenceExpectation == nil || len(body.EvidenceExpectation.Events) != 2 || body.EvidenceDurability != "failed" {
		t.Fatalf("body=%+v api=%+v err=%v", body, apiErr, err)
	}
}

func TestEvidenceExpectationRealBuilderMatchesQueuedIdentity(t *testing.T) {
	p := expectationTestPublisher(t, 4)
	ctx := withEvidenceOutcome(testTraceContext())
	events := BuildRunCypherEvents(ctx, "kn_demo", "RETURN 1", 1, nil)
	if len(events) != 1 {
		t.Fatalf("builder events=%d", len(events))
	}
	if err := SubmitEvents(ctx, nil, nil, events); err != nil {
		t.Fatal(err)
	}
	e := freezeEvidenceExpectation(ctx)
	if len(e.Events) != 1 || e.Events[0].EventType != "data.query.observed" {
		t.Fatalf("builder expectation=%+v", e)
	}
	var record struct {
		EventID     string `json:"event_id"`
		EventType   string `json:"event_type"`
		PayloadHash string `json:"payload_hash"`
		ProducerID  string `json:"producer_id"`
	}
	if err := json.Unmarshal(p.SnapshotQueue()[0].Value, &record); err != nil {
		t.Fatal(err)
	}
	item := e.Events[0]
	if item.EventID != record.EventID || item.EventType != record.EventType || item.PayloadHash != record.PayloadHash || item.ProducerID != record.ProducerID {
		t.Fatalf("item=%+v record=%+v", item, record)
	}
}

func TestEvidenceExpectationSupportsMultipleBatches(t *testing.T) {
	expectationTestPublisher(t, 4)
	ctx := withEvidenceOutcome(testTraceContext())
	submitExpectationEvents(t, ctx, []Event{expectationEvent("one")})
	submitExpectationEvents(t, ctx, []Event{expectationEvent("two")})
	e := freezeEvidenceExpectation(ctx)
	if len(e.Events) != 2 || evidenceExpectationDurability(e) != "pending" {
		t.Fatalf("multiple batches=%+v", e)
	}
}
