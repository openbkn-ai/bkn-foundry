// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencepublisher

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFirstAdmissionSurvivesLaterAdmissionClosure(t *testing.T) {
	for _, state := range []string{"publisher_closed", "runtime_unavailable"} {
		t.Run(state, func(t *testing.T) {
			p, err := New(publisherTestConfig(), &fakeSender{})
			if err != nil {
				t.Fatal(err)
			}
			event := publisherTestEvent()
			first := p.TryPublish(event)
			event.PreviousResult = &first
			var got PublishResult
			if state == "publisher_closed" {
				p.Close(context.Background())
				got = p.TryPublish(event)
			} else {
				r := &PublisherRuntime{publisher: p}
				got = r.TryPublish(event)
			}
			if got != first || p.NextSequence() != 2 {
				t.Fatalf("frozen admission changed: first=%+v got=%+v", first, got)
			}
			changed := event
			changed.EventID = "new-event"
			if state == "publisher_closed" {
				got = p.TryPublish(changed)
			} else {
				got = (&PublisherRuntime{publisher: p}).TryPublish(changed)
			}
			if got.Disposition != Dropped {
				t.Fatal("new event bypassed closed admission")
			}
		})
	}
}

func TestPreviousAdmissionReusesFrozenDecisionWithoutEnqueue(t *testing.T) {
	config := publisherTestConfig()
	config.QueueMaxRecords = 1
	p, err := New(config, &fakeSender{})
	if err != nil {
		t.Fatal(err)
	}
	event := publisherTestEvent()
	first := p.TryPublish(event)
	event.PreviousResult = &first
	second := p.TryPublish(event)
	if first != second || len(p.SnapshotQueue()) != 1 || p.NextSequence() != 2 {
		t.Fatalf("re-admission changed outcome or queue: first=%+v second=%+v", first, second)
	}
	var wire map[string]any
	if err := json.Unmarshal(p.SnapshotQueue()[0].Value, &wire); err != nil {
		t.Fatal(err)
	}
	if _, present := wire["PreviousResult"]; present {
		t.Fatal("local metadata leaked into wire")
	}
}

func TestPreviousAdmissionCannotHideChangedIdentity(t *testing.T) {
	for _, change := range []string{"payload", "type", "producer", "empty_hash"} {
		t.Run(change, func(t *testing.T) {
			config := publisherTestConfig()
			config.QueueMaxRecords = 1
			p, err := New(config, &fakeSender{})
			if err != nil {
				t.Fatal(err)
			}
			event := publisherTestEvent()
			first := p.TryPublish(event)
			prior := first
			switch change {
			case "payload":
				event.Envelope = json.RawMessage(`{"different":true}`)
			case "type":
				event.EventType = "retrieval.completed"
			case "producer":
				prior.ProducerID = "another-producer"
			case "empty_hash":
				prior.PayloadHash = ""
			}
			event.PreviousResult = &prior
			second := p.TryPublish(event)
			if second.Disposition != Dropped || second.Reason != ReasonQueueFull || second == first {
				t.Fatalf("changed identity was reused: first=%+v second=%+v", first, second)
			}
		})
	}
}
