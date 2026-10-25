// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestFirstTraceFailureMetadataPreservesFirstFactAndIdentityBoundaries(t *testing.T) {
	store := New()
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	first := sessionvo.OperationCallFact{TraceID: "trace", ConversationID: "conv", InteractionID: "int", OperationID: "a", Attempt: 1, StartedAt: now, Status: sessionvo.AttemptFailed,
		Error: &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"first"`)},
		Input: sessionvo.PayloadEnvelope{Inline: json.RawMessage(`"unrelated"`)}, Output: &sessionvo.PayloadEnvelope{}, CapabilityProfile: &sessionvo.CapabilityProfile{ManifestID: "unrelated"}}
	facts := []sessionvo.OperationCallFact{first}
	for _, change := range []func(*sessionvo.OperationCallFact){
		func(f *sessionvo.OperationCallFact) { f.Attempt = 2; f.Error = nil },
		func(f *sessionvo.OperationCallFact) { f.OperationID = "b"; f.Error = nil },
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "0"
			f.StartedAt = now.Add(time.Second)
			f.Error = nil
		},
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "earlier-completed"
			f.StartedAt = now.Add(-time.Second)
			f.Status = sessionvo.AttemptCompleted
		},
		func(f *sessionvo.OperationCallFact) { f.OperationID = "other-conv"; f.ConversationID = "other" },
		func(f *sessionvo.OperationCallFact) { f.OperationID = "other-int"; f.InteractionID = "other" },
		func(f *sessionvo.OperationCallFact) { f.OperationID = "other-trace"; f.TraceID = "unrequested" },
	} {
		fact := first
		change(&fact)
		facts = append(facts, fact)
	}
	for _, fact := range facts {
		store.operationCalls[operationCallFactKey(fact.OperationID, fact.Attempt)] = fact
	}
	tx := memoryTransaction{s: store}
	got := tx.ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "conv", InteractionID: "int"}, {}, {TraceID: "trace", ConversationID: "conv", InteractionID: "int"}})
	if len(got) != 1 {
		t.Fatalf("identity groups collapsed or leaked: %+v", got)
	}
	for _, fact := range got {
		if fact.Input.Inline != nil || fact.Output != nil || fact.CapabilityProfile != nil || fact.EvidenceCompletion != nil {
			t.Fatalf("unrelated payload retained: %+v", fact)
		}
		if fact.InteractionID == "int" && fact.ConversationID == "conv" && (fact.OperationID != "a" || fact.Attempt != 1 || string(fact.Error.Inline) != `"first"`) {
			t.Fatalf("first failed fact changed: %+v", fact)
		}
	}
	got[0].Error.Inline[1] = 'X'
	again := tx.ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "conv", InteractionID: "int"}})
	if !reflect.DeepEqual(again[0].Error.Inline, first.Error.Inline) {
		t.Fatal("returned metadata mutated stored error")
	}
	if got := tx.ListFirstTraceFailureMetadata(nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestFirstTraceFailureMetadataDoesNotSkipEmptyFirstError(t *testing.T) {
	store := New()
	first := sessionvo.OperationCallFact{TraceID: "trace", ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, StartedAt: time.Now(), Status: sessionvo.AttemptFailed}
	later := first
	later.Attempt = 2
	later.Error = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: json.RawMessage(`"later-keyword"`)}
	store.operationCalls[operationCallFactKey(first.OperationID, first.Attempt)] = first
	store.operationCalls[operationCallFactKey(later.OperationID, later.Attempt)] = later
	got := (memoryTransaction{s: store}).ListFirstTraceFailureMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "conv", InteractionID: "int"}})
	if len(got) != 1 || got[0].Attempt != 1 || got[0].Error != nil {
		t.Fatalf("later error replaced first failed fact: %+v", got)
	}
}

func TestFirstTraceSourceMetadataKeepsScopeAndEarliestNonemptySource(t *testing.T) {
	store := New()
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	base := sessionvo.OperationCallFact{TraceID: "trace", ConversationID: "conv", InteractionID: "visible", OperationID: "a", Attempt: 1, StartedAt: now, SourceModule: "visible-module", Error: &sessionvo.PayloadEnvelope{Inline: json.RawMessage(`"private"`)}}
	seed := func(f sessionvo.OperationCallFact) {
		store.operationCalls[operationCallFactKey(f.OperationID, f.Attempt)] = f
	}
	seed(base)
	for _, change := range []func(*sessionvo.OperationCallFact){
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "empty"
			f.StartedAt = now.Add(-time.Hour)
			f.SourceModule = ""
		},
		func(f *sessionvo.OperationCallFact) { f.Attempt = 2; f.SourceModule = "later-attempt" },
		func(f *sessionvo.OperationCallFact) { f.OperationID = "b"; f.SourceModule = "later-op" },
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "0"
			f.StartedAt = now.Add(time.Hour)
			f.SourceModule = "later-time"
		},
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "private"
			f.InteractionID = "unauthorized"
			f.StartedAt = now.Add(-time.Hour)
			f.SourceModule = "private-module"
		},
		func(f *sessionvo.OperationCallFact) {
			f.OperationID = "other-conv"
			f.ConversationID = "other"
			f.SourceModule = "other-conv-module"
		},
		func(f *sessionvo.OperationCallFact) { f.OperationID = "other-trace"; f.TraceID = "unrequested" },
	} {
		fact := base
		change(&fact)
		seed(fact)
	}
	got := (memoryTransaction{s: store}).ListFirstTraceSourceMetadata([]isessionstore.TraceMetadataScope{{TraceID: "trace", ConversationID: "conv", InteractionID: "visible"}, {TraceID: "trace", ConversationID: "conv", InteractionID: "visible"}, {}})
	if len(got) != 1 {
		t.Fatalf("source groups collapsed/leaked: %+v", got)
	}
	for _, f := range got {
		if f.Error != nil || f.Output != nil || f.Input.Inline != nil || f.Status != "" {
			t.Fatalf("payload retained: %+v", f)
		}
		if f.InteractionID == "visible" && f.ConversationID == "conv" && (f.SourceModule != "visible-module" || f.Attempt != 1 || f.OperationID != "a") {
			t.Fatalf("source order changed: %+v", f)
		}
	}
}

func TestTraceMetadataLegacyTupleNeverWidensToCollidingScopes(t *testing.T) {
	store := New()
	legacy := sessionvo.OperationCallFact{OperationID: "legacy", Attempt: 1, TraceID: "trace", StartedAt: time.Now(), Status: sessionvo.AttemptFailed, SourceModule: "legacy-module"}
	store.operationCalls[operationCallFactKey(legacy.OperationID, legacy.Attempt)] = legacy
	for i := 0; i < 1001; i++ {
		fact := legacy
		fact.OperationID = fmt.Sprintf("foreign-%04d", i)
		fact.ConversationID = fact.OperationID
		fact.InteractionID = fact.OperationID
		fact.SourceModule = "foreign-private"
		fact.Error = &sessionvo.PayloadEnvelope{Inline: json.RawMessage(`"foreign-private"`)}
		store.operationCalls[operationCallFactKey(fact.OperationID, fact.Attempt)] = fact
	}
	tx := memoryTransaction{s: store}
	scopes := []isessionstore.TraceMetadataScope{{TraceID: "trace"}, {TraceID: "trace"}, {ConversationID: "ignored-empty-trace"}}
	for _, facts := range [][]sessionvo.OperationCallFact{tx.ListFirstTraceFailureMetadata(scopes), tx.ListFirstTraceSourceMetadata(scopes)} {
		if len(facts) != 1 || facts[0].OperationID != "legacy" || facts[0].ConversationID != "" || facts[0].InteractionID != "" || facts[0].Error != nil {
			t.Fatalf("empty legacy tuple read colliding foreign groups: %+v", facts)
		}
	}
}
