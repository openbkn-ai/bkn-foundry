// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"net/http"
	"strings"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// recallingBackend is the shared mock with the two answers this file is about
// made realistic: recall returns a subset of the network rather than all of it,
// and the by-id lookup is all-or-nothing the way bkn-backend's is — it answers
// 404 when any one of the requested ids is missing, without saying which.
type recallingBackend struct {
	*mockBknBackend
	recalled  []*interfaces.ObjectType
	existing  map[string]*interfaces.ObjectType
	lookupErr error
	lookups   [][]string
}

func (b *recallingBackend) SearchObjectTypes(
	context.Context, *interfaces.QueryConceptsReq,
) (*interfaces.ObjectTypeConcepts, error) {
	return &interfaces.ObjectTypeConcepts{Entries: b.recalled}, nil
}

func (b *recallingBackend) GetObjectTypeDetail(
	_ context.Context, _ string, otIDs []string, _ bool,
) ([]*interfaces.ObjectType, error) {
	b.lookups = append(b.lookups, append([]string(nil), otIDs...))
	if b.lookupErr != nil {
		return nil, b.lookupErr
	}
	// bkn-backend validates every id before it looks anything up, and an id
	// holding * or / is refused with 400 for the whole batch.
	for _, id := range otIDs {
		if strings.ContainsAny(id, "*/") {
			return nil, &infraErr.HTTPError{HTTPCode: http.StatusBadRequest, Code: "InvalidParameter.ID"}
		}
		// The id travels in a URL path. A % that opens no valid escape fails
		// url.Parse inside this process, and the adapter reports that with no
		// status code at all — the shape nothing can classify.
		if strings.Contains(id, "%") {
			return nil, &infraErr.HTTPError{HTTPCode: 0, Code: "RequestFailed"}
		}
	}
	out := make([]*interfaces.ObjectType, 0, len(otIDs))
	for _, id := range otIDs {
		found, ok := b.existing[id]
		if !ok {
			return nil, &infraErr.HTTPError{HTTPCode: http.StatusNotFound, Code: "ObjectTypeNotFound"}
		}
		out = append(out, found)
	}
	return out, nil
}

func objectType(id string) *interfaces.ObjectType {
	return &interfaces.ObjectType{ID: id, Name: id, Comment: id}
}

// newPinnedScenario builds a network whose recall surfaces only recalledIDs
// while existingIDs are everything the network actually has.
func newPinnedScenario(t *testing.T, recalledIDs, existingIDs []string) (*localSearchImpl, *recallingBackend) {
	t.Helper()
	detail := createMockNetworkDetail(0, 0, 0)
	backend := &recallingBackend{
		mockBknBackend: &mockBknBackend{networkDetail: detail},
		existing:       map[string]*interfaces.ObjectType{},
	}
	for _, id := range recalledIDs {
		backend.recalled = append(backend.recalled, objectType(id))
	}
	for _, id := range existingIDs {
		backend.existing[id] = objectType(id)
	}
	return &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}, backend
}

func retrievePinned(t *testing.T, svc *localSearchImpl, pinned ...string) *interfaces.KnSearchConceptResult {
	t.Helper()
	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 5
	cfg.ObjectTypes = pinned
	res, err := svc.conceptRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "China"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

// The reported failure (#1850): an object type the caller named is in the
// network but scored nothing for this query, and the answer was "it does not
// exist in this knowledge network".
func TestPinnedObjectTypeSurvivesAnUnrelatedRecall(t *testing.T) {
	svc, backend := newPinnedScenario(t, []string{"players"}, []string{"players", "teams"})

	res := retrievePinned(t, svc, "teams")

	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("the pinned object type is missing: %v", conceptIDs(res.ObjectTypes))
	}
	if len(res.UnmatchedObjectTypes) != 0 {
		t.Fatalf("a real object type was reported as unmatched: %v", res.UnmatchedObjectTypes)
	}
	if len(backend.lookups) != 1 {
		t.Fatalf("expected one lookup for the pinned id, got %v", backend.lookups)
	}
}

// The failure was also intermittent: a relation pointing at the pinned type
// pulled it into the pool through endpoint completion, so the same request
// answered differently depending on what else this query happened to recall.
// Both shapes must now give the same answer.
func TestPinnedObjectTypeDoesNotDependOnRecalledRelations(t *testing.T) {
	withoutRelation, _ := newPinnedScenario(t, []string{"players"}, []string{"players", "teams"})
	got := conceptIDs(retrievePinned(t, withoutRelation, "teams").ObjectTypes)

	withRelation, backend := newPinnedScenario(t, []string{"players"}, []string{"players", "teams"})
	backend.mockBknBackend.relationTypesResp = &interfaces.RelationTypeConcepts{
		Entries: []*interfaces.RelationType{{
			ID: "plays_for", Name: "plays_for",
			SourceObjectTypeID: "players", TargetObjectTypeID: "teams",
		}},
	}
	want := conceptIDs(retrievePinned(t, withRelation, "teams").ObjectTypes)

	if !equalStrings(got, want) {
		t.Fatalf("the answer still depends on what else was recalled: without a relation %v, with one %v", got, want)
	}
}

// An id that really is not in the network is still reported, and reported alone.
func TestPinnedObjectTypeAbsentFromTheNetworkIsReported(t *testing.T) {
	svc, _ := newPinnedScenario(t, []string{"players"}, []string{"players", "teams"})

	res := retrievePinned(t, svc, "teams", "nosuchtype")

	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("the real pinned object type was dropped: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"nosuchtype"}) {
		t.Fatalf("unmatched = %v, want only the id that does not exist", res.UnmatchedObjectTypes)
	}
}

// The batch lookup is all-or-nothing, so one bad id would otherwise take the
// good ones down with it. The fallback is what turns "some of these are wrong"
// into "these are wrong".
func TestPinnedObjectTypesFallBackToOneLookupEach(t *testing.T) {
	svc, backend := newPinnedScenario(t, nil, []string{"teams", "players"})

	res := retrievePinned(t, svc, "teams", "nosuchtype", "players")

	if !containsConcept(res.ObjectTypes, "teams") || !containsConcept(res.ObjectTypes, "players") {
		t.Fatalf("a bad id took the good ones down with it: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"nosuchtype"}) {
		t.Fatalf("unmatched = %v", res.UnmatchedObjectTypes)
	}
	if len(backend.lookups) != 4 {
		t.Fatalf("expected the batch and then one lookup per id, got %v", backend.lookups)
	}
}

// A dependency failure is not a caller mistake. Reporting it as "this object
// type does not exist" would send an agent rewriting a correct request.
func TestPinnedObjectTypeLookupFailurePropagates(t *testing.T) {
	svc, backend := newPinnedScenario(t, []string{"players"}, []string{"teams"})
	backend.lookupErr = &infraErr.HTTPError{HTTPCode: http.StatusInternalServerError, Code: "Boom"}

	cfg := DefaultConceptRetrievalConfig()
	cfg.ObjectTypes = []string{"teams"}
	_, err := svc.conceptRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "129", Query: "China"}, cfg)
	if err == nil {
		t.Fatal("a failing dependency must not be reported as a missing object type")
	}
}

// An allow list that is half right must not narrow the search in silence: the
// ids that could not be used are named alongside whatever the search found.
func TestPartiallyUsableAllowListIsReported(t *testing.T) {
	svc, _ := newPinnedScenario(t, []string{"players"}, []string{"players", "teams"})

	res := retrievePinned(t, svc, "teams", "nosuchtype")

	if !equalStrings(res.UnmatchedObjectTypes, []string{"nosuchtype"}) {
		t.Fatalf("unmatched = %v", res.UnmatchedObjectTypes)
	}
	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("the usable id was dropped: %v", conceptIDs(res.ObjectTypes))
	}
}

// bkn-backend validates ids before it looks anything up, so one holding * or /
// is refused with 400 for the whole batch. Before this path existed such an id
// was simply reported as unusable; failing the entire search over it would be a
// worse answer than the one it replaced.
func TestMalformedPinnedIDDoesNotFailTheSearch(t *testing.T) {
	svc, _ := newPinnedScenario(t, nil, []string{"teams"})

	res := retrievePinned(t, svc, "teams", "*")

	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("a malformed id took the good one down with it: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"*"}) {
		t.Fatalf("unmatched = %v, want only the malformed id", res.UnmatchedObjectTypes)
	}
}

// The scope layer matches ids case-insensitively on purpose. Resolving by id
// has to forgive the same thing, or a pinned id in the wrong case would work
// only while recall happened to surface it.
func TestPinnedObjectTypeIsResolvedCaseInsensitively(t *testing.T) {
	svc, _ := newPinnedScenario(t, nil, []string{"teams"})

	res := retrievePinned(t, svc, "TEAMS")

	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("a pinned id in the wrong case was not resolved: %v", conceptIDs(res.ObjectTypes))
	}
	if len(res.UnmatchedObjectTypes) != 0 {
		t.Fatalf("unmatched = %v", res.UnmatchedObjectTypes)
	}
}

// The network is free to spell an id in mixed case, so the fallback still asks
// as the caller wrote it after the folded spelling misses.
func TestPinnedObjectTypeKeepsTheCallerSpellingAsAFallback(t *testing.T) {
	svc, backend := newPinnedScenario(t, nil, []string{"Teams"})

	res := retrievePinned(t, svc, "Teams", "nosuchtype")

	if !containsConcept(res.ObjectTypes, "Teams") {
		t.Fatalf("a mixed-case id the network really has was dropped: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"nosuchtype"}) {
		t.Fatalf("unmatched = %v", res.UnmatchedObjectTypes)
	}
	if len(backend.lookups) < 2 {
		t.Fatalf("expected the folded spelling and then the caller's, got %v", backend.lookups)
	}
}

// An id holding a stray % never reaches the service: url.Parse fails in this
// process and the adapter reports it with no status code, which is the one
// shape error classification cannot rescue. It is ruled out before the call.
func TestUnaddressablePinnedIDIsReportedWithoutACall(t *testing.T) {
	svc, backend := newPinnedScenario(t, nil, []string{"teams"})

	res := retrievePinned(t, svc, "teams", "100%")

	if !containsConcept(res.ObjectTypes, "teams") {
		t.Fatalf("an unaddressable id took the good one down with it: %v", conceptIDs(res.ObjectTypes))
	}
	if !equalStrings(res.UnmatchedObjectTypes, []string{"100%"}) {
		t.Fatalf("unmatched = %v, want only the unaddressable id", res.UnmatchedObjectTypes)
	}
	for _, lookup := range backend.lookups {
		for _, id := range lookup {
			if strings.Contains(id, "%") {
				t.Fatalf("the unaddressable id was still sent: %v", backend.lookups)
			}
		}
	}
}

// A malformed id is refused before the call too, so the ids that do go out are
// only ever ones the service can answer about.
func TestMalformedPinnedIDIsNotSent(t *testing.T) {
	svc, backend := newPinnedScenario(t, nil, []string{"teams"})

	retrievePinned(t, svc, "teams", "*")

	for _, lookup := range backend.lookups {
		for _, id := range lookup {
			if strings.ContainsAny(id, "*/") {
				t.Fatalf("a malformed id was still sent: %v", backend.lookups)
			}
		}
	}
}

// One id in the wrong case must not push every pinned id through the fallback:
// the batch asks in the folded spelling, which is how object type ids are
// written, so the usual case still costs one call.
func TestWrongCasePinnedIDStillResolvesInOneCall(t *testing.T) {
	svc, backend := newPinnedScenario(t, nil, []string{"teams", "players"})

	res := retrievePinned(t, svc, "TEAMS", "players")

	if !containsConcept(res.ObjectTypes, "teams") || !containsConcept(res.ObjectTypes, "players") {
		t.Fatalf("object types = %v", conceptIDs(res.ObjectTypes))
	}
	if len(backend.lookups) != 1 {
		t.Fatalf("expected one batch lookup, got %v", backend.lookups)
	}
}
