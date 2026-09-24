// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysvc

import (
	"context"
	"errors"
	"testing"
)

type readerFunc func(context.Context) (Snapshot, error)

func (f readerFunc) Read(ctx context.Context) (Snapshot, error) { return f(ctx) }

func TestServiceReadPreservesAuthoritativeRuntimeState(t *testing.T) {
	snapshot := Snapshot{
		Revision:       7,
		DesiredState:   StateDisabled,
		EffectiveState: StateDisabling,
		Operation: Operation{
			ID:               "op-7",
			Phase:            PhaseDisabling,
			RequestedState:   StateDisabled,
			ExpectedRevision: 7,
		},
		Acknowledgements: []EndpointAcknowledgement{
			{InstanceID: "collector-a", Ready: true, State: AckDraining, Queue: QueueDisposition{Exported: 12, Dropped: 1}},
		},
	}

	got, err := New(readerFunc(func(context.Context) (Snapshot, error) { return snapshot, nil })).Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.Revision != snapshot.Revision || got.EffectiveState != StateDisabling || got.Operation.Phase != PhaseDisabling {
		t.Fatalf("Read() lost desired/effective/phase state: %+v", got)
	}
	if got.Acknowledgements[0].Queue.Dropped != 1 {
		t.Fatalf("Read() lost queue disposition: %+v", got.Acknowledgements[0].Queue)
	}
}

func TestServiceReadFailsClosedWhenReaderIsUnavailable(t *testing.T) {
	want := errors.New("store unavailable")
	_, err := New(readerFunc(func(context.Context) (Snapshot, error) { return Snapshot{}, want })).Read(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Read() error = %v, want %v", err, want)
	}
}

func TestSnapshotValidateRejectsBooleanOnlyState(t *testing.T) {
	if err := (Snapshot{Revision: 1}).Validate(); err == nil {
		t.Fatal("Validate() accepted a snapshot without desired/effective state")
	}
}
