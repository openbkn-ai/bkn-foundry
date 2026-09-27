// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"errors"
	"net/http"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// completePinnedObjectTypes puts the object types the caller named in
// object_types into the candidate pool, whether or not concept recall surfaced
// them.
//
// The pool this runs against is a recall result: buildCoarseRecallQuery scores
// object type names and comments against the query. An id the caller pinned is
// not a query, it is a decision already taken, and scoping a decision by a
// recall makes the answer depend on the wording. Worse, it made the answer
// depend on unrelated concepts: completeReferencedObjectTypes appends every
// object type a recalled relation or action points at, so a pinned type landed
// in the pool whenever some relation happened to reference it and fell out when
// none did — the same request answered differently on two days (#1850).
//
// So the pinned ids are resolved the same way relation endpoints are, by asking
// the knowledge network for them. What comes back exists and enters the pool;
// what does not come back is genuinely absent, and scope.apply reports exactly
// those. Authorization is unaffected: every selected object type still goes
// through objectpermission.FilterObjectTypes afterwards.
func (s *localSearchImpl) completePinnedObjectTypes(
	ctx context.Context,
	knID string,
	objects []*interfaces.ObjectType,
	pinned []string,
) ([]*interfaces.ObjectType, error) {
	if len(pinned) == 0 {
		return objects, nil
	}

	known := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		if obj == nil || obj.ID == "" {
			continue
		}
		known[normalizeObjectTypeID(obj.ID)] = struct{}{}
	}

	absent := make([]string, 0, len(pinned))
	for _, id := range normalizeObjectTypeIDs(pinned) {
		if _, ok := known[normalizeObjectTypeID(id)]; ok {
			continue
		}
		absent = append(absent, id)
	}
	if len(absent) == 0 {
		return objects, nil
	}

	resolved, err := s.fetchExistingObjectTypes(ctx, knID, absent)
	if err != nil {
		return nil, err
	}
	for _, obj := range resolved {
		if obj == nil || obj.ID == "" {
			continue
		}
		id := normalizeObjectTypeID(obj.ID)
		if _, ok := known[id]; ok {
			continue
		}
		known[id] = struct{}{}
		objects = append(objects, obj)
	}
	return objects, nil
}

// fetchExistingObjectTypes returns the object types among ids that the network
// has and this caller may see, and drops the rest without failing.
//
// The batch endpoint is all-or-nothing: bkn-backend answers 404 when any one id
// is missing and 403 when any one is not the caller's to read, and neither
// answer says which. The batch is still tried first, because the case worth
// optimising is the one where every pinned id is real. Only when the batch is
// refused does this fall back to one call per id, which is what turns "some of
// these are wrong" into "these are wrong".
func (s *localSearchImpl) fetchExistingObjectTypes(
	ctx context.Context,
	knID string,
	ids []string,
) ([]*interfaces.ObjectType, error) {
	resolved, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, ids, true)
	if err == nil {
		return resolved, nil
	}
	if !objectTypeLookupRefused(err) {
		return nil, err
	}
	s.logger.WithContext(ctx).Infof(
		"[KnSearchLocal] pinned object types refused as a batch, resolving one by one: %v", ids)

	out := make([]*interfaces.ObjectType, 0, len(ids))
	for _, id := range ids {
		found, err := s.lookupOneObjectType(ctx, knID, id)
		if err != nil {
			return nil, err
		}
		if found != nil {
			out = append(out, found)
		}
	}
	return out, nil
}

// lookupOneObjectType resolves a single pinned id, or reports it as one the
// caller cannot use by returning nil.
//
// It tries the caller's spelling and then the folded one. The scope layer
// matches ids case-insensitively on purpose — it "forgives a caller who typed
// the id back in the wrong case" — and resolving by id must forgive the same
// thing, or a pinned Teams would resolve only while recall happened to surface
// it, which is the inconsistency this whole change is about.
func (s *localSearchImpl) lookupOneObjectType(
	ctx context.Context,
	knID string,
	id string,
) (*interfaces.ObjectType, error) {
	attempts := []string{id}
	if folded := normalizeObjectTypeID(id); folded != id {
		attempts = append(attempts, folded)
	}
	for _, attempt := range attempts {
		found, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, []string{attempt}, true)
		if err != nil {
			if objectTypeLookupRefused(err) {
				continue
			}
			return nil, err
		}
		if len(found) > 0 {
			return found[0], nil
		}
	}
	return nil, nil
}

// objectTypeLookupRefused reports whether the lookup failed over the id itself
// rather than over the service answering.
//
// Three answers mean that, and all three land the id in the same place —
// reported back to the caller as one it cannot use. The network does not have
// it (404); it will not show it to this caller (403); or the id is not a
// well-formed resource id at all (400, which bkn-backend answers for an id
// holding * or /, before it looks anything up).
//
// They are deliberately not told apart: saying "this exists but is not yours"
// would answer a question the caller has no right to ask. And 400 has to be in
// this set, not outside it — an id the caller simply mistyped was reported as
// unusable before this path existed, and failing the whole search over it would
// be a worse answer than the one it replaced.
//
// Every other failure is the dependency, and must not be dressed up as a caller
// mistake: an agent told its object type does not exist will rewrite a request
// that was correct.
func objectTypeLookupRefused(err error) bool {
	var httpErr *infraErr.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	switch httpErr.HTTPCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}
