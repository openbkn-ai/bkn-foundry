// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// The original diagnostic reproduced process-local backoff; this asserts the
// durable behavior across independent services sharing the same storage.
func TestIssue2149RestartAndSecondInstanceHonorBackoff(t *testing.T) {
	snapshot, _, clock := integrityFixture()
	first := integrityServiceForSnapshot(t, snapshot)
	store := first.sessionStore.(*integritySnapshotStore)
	store.clock = func() time.Time { return clock }
	alignIntegrityFixtureVersions(t, store)
	store.readErr = errors.New("synthetic dependency unavailable; private record detail")
	run := func(service *Service) error {
		_, err := service.PersistRecordIntegrityBatch(context.Background(), "", 10)
		return err
	}
	if err := run(first); err == nil || store.reads != 1 || strings.Contains(err.Error(), "private record detail") {
		t.Fatalf("first failure missing/unsafe: reads=%d err=%v", store.reads, err)
	}
	original := materializationRetryState(t, store, "int")
	second := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	restarted := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	for _, service := range []*Service{first, second, restarted} {
		if err := run(service); err != nil || store.reads != 1 {
			t.Fatalf("existing schedule bypassed: reads=%d err=%v", store.reads, err)
		}
	}
	if state := materializationRetryState(t, store, "int"); state != original {
		t.Fatalf("deferred calls changed durable state: %+v", state)
	}
	// Diagnostic reads expose the schedule only to the authorized owner and never
	// inspect original evidence, disclose a lease token, or create a verdict.
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	state, found, err := restarted.GetRecordIntegrityRetryStatus(context.Background(), "int", scope)
	if err != nil || !found || state.Status != isessionstore.RecordIntegrityRetryWait || state.Attempts != 1 || state.NextAttemptAt != original.NextAttemptAt || state.Token != "" || store.reads != 1 {
		t.Fatalf("authorized schedule: %+v %v %v", state, found, err)
	}
	if _, found, err := restarted.GetRecordIntegrityRetryStatus(context.Background(), "int", evidencevo.QueryScope{AccountID: "foreign", AccountType: "user"}); err != nil || found {
		t.Fatalf("foreign schedule exposed: found=%v err=%v", found, err)
	}
	clock = original.NextAttemptAt
	store.readErr = nil
	if err := run(restarted); err != nil || store.reads != 2 {
		t.Fatalf("scheduled recovery failed: reads=%d err=%v", store.reads, err)
	}
	if state := materializationRetryState(t, store, "int"); state.Status != isessionstore.RecordIntegritySucceeded || state.Attempts != 2 {
		t.Fatalf("recovery not durable: %+v", state)
	}
}

func TestIssue2149TwoServicesDoNotInspectSameSourceConcurrently(t *testing.T) {
	snapshot, _, _ := integrityFixture()
	first := integrityServiceForSnapshot(t, snapshot)
	store := first.sessionStore.(*integritySnapshotStore)
	alignIntegrityFixtureVersions(t, store)
	blocking := &blockingIntegritySnapshotStore{integritySnapshotStore: store, started: make(chan struct{}), release: make(chan struct{})}
	first.sessionStore = blocking
	second := New(evidencestore.New(), WithSessionStore(blocking), WithCurrentRecordIntegrity())
	done := make(chan error, 1)
	go func() { _, err := first.PersistRecordIntegrityBatch(context.Background(), "", 10); done <- err }()
	defer func() {
		close(blocking.release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first inspection not entered")
	}
	state := materializationRetryState(t, store, "int")
	if state.Status != isessionstore.RecordIntegrityRunning || state.Attempts != 1 {
		t.Fatalf("missing active claim: %+v", state)
	}
	if next, err := second.PersistRecordIntegrityBatch(context.Background(), "", 10); err != nil || next != "" {
		t.Fatalf("second service acquired active source: %q %v", next, err)
	}
	if current := materializationRetryState(t, store, "int"); current != state {
		t.Fatalf("second service changed active claim: %+v", current)
	}
}
