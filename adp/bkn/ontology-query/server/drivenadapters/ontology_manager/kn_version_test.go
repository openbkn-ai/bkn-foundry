// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

// versionedBackend stands in for bkn-backend: it records the query of every request and
// answers with the headers and status a test sets.
type versionedBackend struct {
	mu       sync.Mutex
	queries  []string
	respond  func(call int, w http.ResponseWriter)
	server   *httptest.Server
	accessor *ontologyManagerAccess
}

func newVersionedBackend(t *testing.T, respond func(call int, w http.ResponseWriter)) *versionedBackend {
	t.Helper()
	b := &versionedBackend{respond: respond}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		call := len(b.queries)
		b.queries = append(b.queries, r.URL.RawQuery)
		b.mu.Unlock()
		b.respond(call, w)
	}))
	t.Cleanup(b.server.Close)
	b.accessor = &ontologyManagerAccess{ontologyManagerUrl: b.server.URL, httpClient: newVersionAwareHTTPClient()}
	return b
}

func objectTypeBody(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"entries":[{"id":"ot-1","name":"ot"}]}`))
}

func pinnedContext(t *testing.T, knVersion, draftRevision string) (context.Context, *interfaces.KNVersionPin) {
	t.Helper()
	pin, err := interfaces.NewKNVersionPin(knVersion, draftRevision)
	if err != nil {
		t.Fatal(err)
	}
	return interfaces.WithKNVersionPin(context.Background(), pin), pin
}

func TestFirstReadFixesTheVersionOfEveryLaterRead(t *testing.T) {
	backend := newVersionedBackend(t, func(_ int, w http.ResponseWriter) {
		w.Header().Set(interfaces.HTTPHeaderKNVersion, "3")
		objectTypeBody(w)
	})
	ctx, pin := pinnedContext(t, "", "")

	if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.accessor.ListRelationTypes(ctx, "kn-1", "main", interfaces.RelationTypesQuery{}); err != nil {
		t.Fatal(err)
	}

	want := []string{"branch=main", "branch=main&kn_version=3", "branch=main&limit=-1&kn_version=3"}
	if len(backend.queries) != len(want) {
		t.Fatalf("queries = %v", backend.queries)
	}
	for i := range want {
		if backend.queries[i] != want[i] {
			t.Errorf("query %d = %q, want %q", i, backend.queries[i], want[i])
		}
	}
	if pin.Token() != "v3" {
		t.Fatalf("Token() = %q", pin.Token())
	}
}

func TestDraftReadsCarryTheDraftRevisionOfTheFirstRead(t *testing.T) {
	backend := newVersionedBackend(t, func(_ int, w http.ResponseWriter) {
		w.Header().Set(interfaces.HTTPHeaderKNVersion, "draft")
		w.Header().Set(interfaces.HTTPHeaderKNDraftRevision, "9")
		objectTypeBody(w)
	})
	ctx, _ := pinnedContext(t, "draft", "")

	for _, id := range []string{"ot-1", "ot-2"} {
		if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", id); err != nil {
			t.Fatal(err)
		}
	}
	if backend.queries[0] != "branch=main&kn_version=draft" ||
		backend.queries[1] != "branch=main&kn_version=draft&draft_revision=9" {
		t.Fatalf("queries = %v", backend.queries)
	}
}

func TestUnversionedNetworkGetsTheRequestItAlwaysGot(t *testing.T) {
	backend := newVersionedBackend(t, func(_ int, w http.ResponseWriter) { objectTypeBody(w) })
	ctx, _ := pinnedContext(t, "", "")

	for _, id := range []string{"ot-1", "ot-2"} {
		if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", id); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range backend.queries {
		if query != "branch=main" {
			t.Fatalf("query = %q, want only branch=main", query)
		}
	}
	// A context without any pin (scheduled work, async executions) is untouched as well.
	if _, _, err := backend.accessor.GetObjectType(context.Background(), "kn-1", "main", "ot-3"); err != nil {
		t.Fatal(err)
	}
	if backend.queries[2] != "branch=main" {
		t.Fatalf("query without pin = %q", backend.queries[2])
	}
}

func TestPurgedVersionFailsTheQueryWithTheBackendError(t *testing.T) {
	backend := newVersionedBackend(t, func(call int, w http.ResponseWriter) {
		if call == 0 {
			w.Header().Set(interfaces.HTTPHeaderKNVersion, "3")
			objectTypeBody(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error_code":"BknBackend.KNVersion.VersionPurged","description":"purged"}`))
	})
	ctx, _ := pinnedContext(t, "", "")

	if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-2")
	versionErr, ok := logics.KNVersionError(ctx, err)
	if !ok || versionErr.HTTPCode != http.StatusGone || versionErr.ErrorCode != "BknBackend.KNVersion.VersionPurged" {
		t.Fatalf("error = %#v (%v), want 410 BknBackend.KNVersion.VersionPurged", versionErr, err)
	}

	// The same holds for the calls that wrap their error.
	_, err = backend.accessor.ListRelationTypes(ctx, "kn-1", "main", interfaces.RelationTypesQuery{})
	if versionErr, ok := logics.KNVersionError(ctx, err); !ok || versionErr.HTTPCode != http.StatusGone {
		t.Fatalf("ListRelationTypes error = %v", err)
	}
}

func TestChangedDraftFailsTheQueryWithTheBackendError(t *testing.T) {
	backend := newVersionedBackend(t, func(call int, w http.ResponseWriter) {
		if call == 0 {
			w.Header().Set(interfaces.HTTPHeaderKNVersion, "draft")
			w.Header().Set(interfaces.HTTPHeaderKNDraftRevision, "4")
			objectTypeBody(w)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error_code":"BknBackend.KNVersion.DraftRevisionMismatch","description":"changed"}`))
	})
	ctx, _ := pinnedContext(t, "draft", "")

	_, _, _ = backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-1")
	_, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-2")
	versionErr, ok := logics.KNVersionError(ctx, err)
	if !ok || versionErr.HTTPCode != http.StatusConflict ||
		versionErr.ErrorCode != "BknBackend.KNVersion.DraftRevisionMismatch" {
		t.Fatalf("error = %#v (%v), want 409 DraftRevisionMismatch", versionErr, err)
	}
	if backend.queries[1] != "branch=main&kn_version=draft&draft_revision=4" {
		t.Fatalf("second read asked for %q", backend.queries[1])
	}
}

func TestReadFromAnotherVersionFailsTheQuery(t *testing.T) {
	backend := newVersionedBackend(t, func(call int, w http.ResponseWriter) {
		w.Header().Set(interfaces.HTTPHeaderKNVersion, []string{"3", "4"}[call])
		objectTypeBody(w)
	})
	ctx, _ := pinnedContext(t, "", "")

	if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-2")
	versionErr, ok := logics.KNVersionError(ctx, err)
	if !ok || versionErr.HTTPCode != http.StatusConflict || versionErr.ErrorCode != "OntologyQuery.KNVersion.Changed" {
		t.Fatalf("error = %#v (%v), want 409 OntologyQuery.KNVersion.Changed", versionErr, err)
	}
}
