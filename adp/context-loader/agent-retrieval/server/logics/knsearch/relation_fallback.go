// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fallbackLookupLimit bounds the one search that finds the fallback paths of the recalled primary
// relations. Fallback paths share their primary's endpoints, and relations between one pair of
// object types are few.
const fallbackLookupLimit = 1000

// pairFallbackRelations pairs each fallback path (BKN spec 3.0 §7.6: a secondary relation with
// fallback_for) with its primary relation. It returns the relations to answer with, in rank order
// and without fallback paths, and the fallback path of each primary that has one, by primary id.
//
//   - A recalled fallback path whose primary was recalled too is attached to it.
//   - A recalled fallback path whose primary was not takes the primary's place in the ranking: the
//     query is about this pair, and the primary is the one to try first.
//   - A recalled primary that may have a fallback path has it looked up, so the rule reaches the
//     caller whether or not the fallback path ranked.
//
// The lookups only refine the answer. When one fails the relations are still answered: a fallback
// path whose primary cannot be read is left out, and a primary whose fallback path cannot be read
// goes without it.
func (s *localSearchImpl) pairFallbackRelations(ctx context.Context, knID string,
	ranked []*interfaces.RelationType) ([]*interfaces.RelationType, map[string]*interfaces.RelationType) {
	recalled := make(map[string]bool, len(ranked))
	for _, rel := range ranked {
		if rel != nil {
			recalled[rel.ID] = true
		}
	}

	fallbacks := map[string]*interfaces.RelationType{}
	var missingPrimaries []string
	for _, rel := range ranked {
		if !rel.IsFallback() {
			continue
		}
		if _, paired := fallbacks[rel.FallbackFor]; paired {
			continue
		}
		fallbacks[rel.FallbackFor] = rel
		if !recalled[rel.FallbackFor] {
			missingPrimaries = append(missingPrimaries, rel.FallbackFor)
		}
	}

	primaries := map[string]*interfaces.RelationType{}
	if len(missingPrimaries) > 0 {
		found, err := s.bknBackend.GetRelationTypeDetail(ctx, knID, missingPrimaries, true)
		if err != nil {
			s.logger.WithContext(ctx).Warnf("[ConceptRetrieval] primary relations of recalled fallback paths unreadable, fallback paths left out: %v", err)
		}
		for _, rel := range found {
			if rel != nil && rel.Lifecycle.Inactive() == nil && fallbacks[rel.ID] != nil {
				primaries[rel.ID] = rel
			}
		}
	}

	out := make([]*interfaces.RelationType, 0, len(ranked))
	emitted := map[string]bool{}
	for _, rel := range ranked {
		if rel == nil {
			continue
		}
		if rel.IsFallback() {
			primary := primaries[rel.FallbackFor]
			if fallbacks[rel.FallbackFor] != rel || primary == nil || emitted[primary.ID] {
				continue
			}
			rel = primary
		}
		if emitted[rel.ID] {
			continue
		}
		emitted[rel.ID] = true
		out = append(out, rel)
	}
	// A fallback path whose primary could not be read pairs with nothing.
	for primaryID := range fallbacks {
		if !emitted[primaryID] {
			delete(fallbacks, primaryID)
		}
	}

	s.findFallbacks(ctx, knID, out, fallbacks)
	return out, fallbacks
}

// findFallbacks looks up, in one search, the fallback paths of the primaries in relations that may
// have one and have none yet, and records them in fallbacks.
func (s *localSearchImpl) findFallbacks(ctx context.Context, knID string,
	relations []*interfaces.RelationType, fallbacks map[string]*interfaces.RelationType) {
	waiting := map[string]bool{}
	var endpoints []*interfaces.KnCondition
	seenPair := map[[2]string]bool{}
	for _, rel := range relations {
		if fallbacks[rel.ID] != nil || !rel.MayHaveFallback() {
			continue
		}
		waiting[rel.ID] = true
		pair := [2]string{rel.SourceObjectTypeID, rel.TargetObjectTypeID}
		if seenPair[pair] {
			continue
		}
		seenPair[pair] = true
		endpoints = append(endpoints, &interfaces.KnCondition{
			Operation: interfaces.KnOperationTypeAnd,
			SubConditions: []*interfaces.KnCondition{
				{Field: "source_object_type_id", Operation: interfaces.KnOperationTypeEqual, Value: pair[0], ValueFrom: interfaces.CondValueFromConst},
				{Field: "target_object_type_id", Operation: interfaces.KnOperationTypeEqual, Value: pair[1], ValueFrom: interfaces.CondValueFromConst},
			},
		})
	}
	if len(waiting) == 0 {
		return
	}

	resp, err := s.bknBackend.SearchRelationTypes(ctx, &interfaces.QueryConceptsReq{
		KnID:              knID,
		Cond:              &interfaces.KnCondition{Operation: interfaces.KnOperationTypeOr, SubConditions: endpoints},
		Limit:             fallbackLookupLimit,
		ExcludeDeprecated: true,
	})
	if err != nil || resp == nil {
		s.logger.WithContext(ctx).Warnf("[ConceptRetrieval] fallback paths of recalled primary relations unreadable, answered without them: %v", err)
		return
	}
	for _, rel := range resp.Entries {
		if rel.IsFallback() && waiting[rel.FallbackFor] && fallbacks[rel.FallbackFor] == nil {
			fallbacks[rel.FallbackFor] = rel
		}
	}
}

// relationProperties reads the relation properties an indirect relation carries in
// mapping_rules.properties. Any other mapping has none.
func relationProperties(mappingRules any) []*interfaces.DataProperty {
	rules, ok := mappingRules.(map[string]any)
	if !ok || rules["properties"] == nil {
		return nil
	}
	raw, err := json.Marshal(rules["properties"])
	if err != nil {
		return nil
	}
	var props []*interfaces.DataProperty
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil
	}
	out := props[:0]
	for _, p := range props {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// fallbackRule is the rule for using a fallback path, worded as in SPECIFICATION.md §7.6.
func fallbackRule(ctx context.Context, fallbackID string) string {
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.SearchSchema.FallbackRule",
		map[string]any{"Fallback": fallbackID})
	if err != nil {
		return ""
	}
	return text
}
