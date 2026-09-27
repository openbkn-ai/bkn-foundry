// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"net/http"
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
