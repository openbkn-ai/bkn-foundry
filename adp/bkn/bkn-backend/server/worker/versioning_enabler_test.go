// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type unversionedAccess struct {
	interfaces.KNVersionAccess
	ids []string
	err error
}

func (a *unversionedAccess) ListUnversionedKNIDs(context.Context) ([]string, error) {
	return a.ids, a.err
}

// enablingVersions turns versioning on as told per network and records each call.
type enablingVersions struct {
	interfaces.KNVersionService
	results map[string]error
	skipped map[string]bool
	calls   []string
	notes   []string
}

func (v *enablingVersions) EnableUnversioned(_ context.Context, knID string, notes string) (bool, error) {
	v.calls = append(v.calls, knID)
	v.notes = append(v.notes, notes)
	if err := v.results[knID]; err != nil {
		return false, err
	}
	return !v.skipped[knID], nil
}

func TestVersioningEnablerVersionsEachUnversionedNetwork(t *testing.T) {
	kvs := &enablingVersions{
		results: map[string]error{"kn2": errors.New("lock wait timeout")},
		skipped: map[string]bool{"kn3": true},
	}
	e := NewVersioningEnabler(&unversionedAccess{ids: []string{"kn1", "kn2", "kn3", "kn4"}},
		func() interfaces.KNVersionService { return kvs })

	// One network failing does not stop the others; one versioned meanwhile is not counted.
	if got := e.RunOnce(context.Background()); got != 2 {
		t.Fatalf("enabled %d, want 2", got)
	}
	if len(kvs.calls) != 4 {
		t.Fatalf("calls %v", kvs.calls)
	}
	for _, notes := range kvs.notes {
		if notes != versioningEnablerNotes {
			t.Fatalf("notes %q", notes)
		}
	}
}

func TestVersioningEnablerWithoutServiceOrListDoesNothing(t *testing.T) {
	e := NewVersioningEnabler(&unversionedAccess{ids: []string{"kn1"}},
		func() interfaces.KNVersionService { return nil })
	if got := e.RunOnce(context.Background()); got != 0 {
		t.Fatalf("enabled %d without a service", got)
	}

	kvs := &enablingVersions{}
	e = NewVersioningEnabler(&unversionedAccess{err: errors.New("db down")},
		func() interfaces.KNVersionService { return kvs })
	if got := e.RunOnce(context.Background()); got != 0 || len(kvs.calls) != 0 {
		t.Fatalf("enabled %d, calls %v after a failed list", got, kvs.calls)
	}
}

func TestVersioningEnablerStopsBetweenNetworks(t *testing.T) {
	kvs := &enablingVersions{}
	e := NewVersioningEnabler(&unversionedAccess{ids: []string{"kn1", "kn2"}},
		func() interfaces.KNVersionService { return kvs })
	e.Stop()
	if got := e.RunOnce(context.Background()); got != 0 || len(kvs.calls) != 0 {
		t.Fatalf("enabled %d, calls %v after stop", got, kvs.calls)
	}
}
