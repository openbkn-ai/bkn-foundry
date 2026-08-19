// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// objectTypeScope is the caller-pinned allow/deny list over the object types that take part in
// recall: object_types keeps only the listed ids, exclude_object_types removes them.
//
// It is applied to the candidate pool *before* relevance ranking and the TopK cut, never after.
// Filtering afterwards looks equivalent and is not: concept recall keeps only max_object_types
// entries, so an object type the caller pinned by id would be dropped by the cut before the filter
// ever saw it, and the caller would get an empty result for an object type that does exist.
type objectTypeScope struct {
	// include maps the normalized id to the spelling the caller used, so an id that matches
	// nothing can be reported back the way it was written.
	include map[string]string
	// includeOrder preserves the caller's order for that report.
	includeOrder []string
	exclude      map[string]struct{}
}

// newObjectTypeScope builds a scope, or returns nil when the caller pinned nothing.
func newObjectTypeScope(include, exclude []string) *objectTypeScope {
	scope := &objectTypeScope{
		include: make(map[string]string, len(include)),
		exclude: make(map[string]struct{}, len(exclude)),
	}
	for _, id := range include {
		normalized := normalizeObjectTypeID(id)
		if normalized == "" {
			continue
		}
		if _, ok := scope.include[normalized]; ok {
			continue
		}
		scope.include[normalized] = strings.TrimSpace(id)
		scope.includeOrder = append(scope.includeOrder, normalized)
	}
	for _, id := range exclude {
		if normalized := normalizeObjectTypeID(id); normalized != "" {
			scope.exclude[normalized] = struct{}{}
		}
	}
	if len(scope.include) == 0 && len(scope.exclude) == 0 {
		return nil
	}
	return scope
}

// normalizeObjectTypeID trims and lowercases an id. Object type ids are lowercase by definition
// (bkn-backend RegexPattern_NonBuiltin_ID), so folding case only forgives a caller who typed the
// id back in the wrong case; it cannot merge two distinct object types.
func normalizeObjectTypeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// apply filters the candidate pool and reports which ids of the allow list matched no object type.
//
// Exclusion wins over inclusion: an id in both lists is dropped. That ordering is the useful one --
// the allow list says where to look, the deny list says what turned out to be noise, and the second
// statement is the more recent one.
func (s *objectTypeScope) apply(objectTypes []*interfaces.ObjectType) (kept []*interfaces.ObjectType, unmatched []string) {
	if s == nil {
		return objectTypes, nil
	}

	matched := make(map[string]struct{}, len(s.include))
	kept = make([]*interfaces.ObjectType, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		id := normalizeObjectTypeID(objectType.ID)
		if len(s.include) > 0 {
			if _, ok := s.include[id]; !ok {
				continue
			}
			matched[id] = struct{}{}
		}
		if _, excluded := s.exclude[id]; excluded {
			continue
		}
		kept = append(kept, objectType)
	}

	for _, id := range s.includeOrder {
		if _, ok := matched[id]; !ok {
			unmatched = append(unmatched, s.include[id])
		}
	}
	return kept, unmatched
}

// logScopeOutcome records what the scope did to the candidate pool.
//
// The unmatched ids are logged at warn: when only some of them miss, the query still returns rows
// and nothing in the response would otherwise reveal that part of the caller's allow list was a
// typo. The all-miss case is reported to the caller in the response message instead.
func (s *localSearchImpl) logScopeOutcome(
	ctx context.Context,
	pathTag string,
	scope *objectTypeScope,
	kept []*interfaces.ObjectType,
	unmatched []string,
) {
	if scope == nil {
		return
	}
	s.logger.WithContext(ctx).Debugf(
		"[ConceptRetrieval]%s Object type scope applied: include=%d exclude=%d kept=%d",
		pathTag, len(scope.include), len(scope.exclude), len(kept),
	)
	if len(unmatched) > 0 {
		s.logger.WithContext(ctx).Warnf(
			"[ConceptRetrieval]%s object_types not found in this knowledge network: %v",
			pathTag, unmatched,
		)
	}
}

// normalizeObjectTypeIDs trims and de-duplicates a caller-supplied id list while keeping its
// first-seen order. Mirrors normalizeConceptGroups for the object type scope knobs.
//
// De-duplication folds case, but the entries keep the caller's spelling: matching is
// case-insensitive anyway, and an id echoed back in an error should read the way it was typed.
func normalizeObjectTypeIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		normalized := normalizeObjectTypeID(id)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, strings.TrimSpace(id))
	}
	return out
}
