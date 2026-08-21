// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (concept recall)
// file: concept_retrieval.go
package knsearch

import (
	"context"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// objectTypeRelationMultiplier The multiple of the number of object types relative to topK when filtering without relationship/relationship.
const objectTypeRelationMultiplier = 2

// conceptRetrieval concept recall main logic.
//
// Routing strategy:
// - When config.ConceptGroups is not empty, use conceptRetrievalByGroups: call BKN directly.
// of SearchObjectTypes/SearchRelationTypes/SearchActionTypes, grouped by BKN in.
// Complete the recall within the scope, skipping the local full GetKnowledgeNetworkDetail and local filtering.
// - When config.ConceptGroups is empty, follow the historical path: pull all network details -> optional rough call.
// Back -> Relationship sorting -> Object selection -> Attribute clipping.
//
// The reason for this split: BKN concept_group uses the object_type collection as its boundary.
// The grouping range of relation_type/action_type is derived by BKN by object boundaries. ContextLoader.
// Instead of duplicating this grouping derivation logic, use BKN typed search as the source of truth.
func (s *localSearchImpl) conceptRetrieval(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	config *interfaces.KnSearchConceptRetrievalConfig,
) (*interfaces.KnSearchConceptResult, error) {
	var err error
	var unmatchedObjectTypes []string
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	if len(config.ConceptGroups) > 0 {
		return s.conceptRetrievalByGroups(ctx, req, config)
	}

	networkDetail, err := s.bknBackend.GetKnowledgeNetworkDetail(ctx, req.KnID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[ConceptRetrieval] GetKnowledgeNetworkDetail failed: %v", err)
		return nil, err
	}

	s.logger.WithContext(ctx).Debugf("[ConceptRetrieval] Network detail: object_types=%d, relation_types=%d, action_types=%d",
		len(networkDetail.ObjectTypes), len(networkDetail.RelationTypes), len(networkDetail.ActionTypes))

	// Apply the caller's object type scope here, on the raw candidate pool: everything below
	// (scoring, relation ranking, the TopK cut) must only ever see object types that are in
	// scope, or a pinned object type ranking below TopK would be cut before the filter runs.
	scope := newObjectTypeScope(config.ObjectTypes, config.ExcludeObjectTypes)
	networkDetail.ObjectTypes, unmatchedObjectTypes = scope.apply(networkDetail.ObjectTypes)
	networkDetail.RelationTypes, networkDetail.ActionTypes = scope.applyToConcepts(
		networkDetail.ObjectTypes, networkDetail.RelationTypes, networkDetail.ActionTypes)
	s.logScopeOutcome(ctx, "", scope, networkDetail.ObjectTypes, unmatchedObjectTypes)

	// 2. Rough recall (optional, for large-scale knowledge networks)
	coarseScored := false
	if boolValue(config.EnableCoarseRecall) && len(networkDetail.RelationTypes) >= config.CoarseMinRelationCount {
		s.logger.WithContext(ctx).Infof("[ConceptRetrieval] Enable coarse recall, relation_count=%d >= threshold=%d",
			len(networkDetail.RelationTypes), config.CoarseMinRelationCount)
		networkDetail, err = s.coarseRecall(ctx, req.KnID, req.Query, networkDetail, config)
		if err != nil {
			s.logger.WithContext(ctx).Warnf("[ConceptRetrieval] Coarse recall failed, continue with full schema: %v", err)
			// Failure of rough recall will not affect subsequent processes, and the complete Schema will continue to be used.
		} else {
			coarseScored = true
		}
	}

	// 3. Coarse object type scoring (BM25 + kNN). Coarse recall only fires on very large networks
	// (relation count >= CoarseMinRelationCount) and most real networks never reach it, so without
	// this channel object types carry no query signal at all and can only be brought out passively
	// by a relation endpoint. It is also the first stage of the two-stage object ranking: it decides
	// which object types are worth handing to the reranker in step 4.
	if !coarseScored {
		s.scoreObjectTypes(ctx, req.KnID, req.Query, networkDetail.ObjectTypes, config)
	}

	// 4. Rank relation types and score object types against the query, in one rerank call.
	rankedRelations := s.rankConcepts(ctx, req.Query, networkDetail.ObjectTypes, networkDetail.RelationTypes,
		config.TopK, req.EnableRerank, req.RerankModel, config.ObjectRerankCandidateLimit)
	s.logger.WithContext(ctx).Debugf("[ConceptRetrieval] Ranked relations: %d -> top_k=%d", len(networkDetail.RelationTypes), len(rankedRelations))

	// 5. Object type selection: Sort by self-relevance, relationship endpoints are incorporated as schema self-consistent constraints.
	selectedObjects := s.selectObjectTypesForConceptRetrieval(networkDetail.ObjectTypes, rankedRelations, config.TopK)
	s.logger.WithContext(ctx).Debugf("[ConceptRetrieval] Selected objects: %d", len(selectedObjects))

	// 5. Convert to local response structure (consistent with Python schema_brief semantics)
	brief := boolValue(config.SchemaBrief)
	objectTypesLocal := s.convertObjectTypesToLocal(selectedObjects, brief, req.IncludeColumns)
	relationTypesLocal := s.convertRelationTypesToLocal(rankedRelations, brief)
	actionTypesLocal := s.convertActionTypesToLocal(networkDetail.ActionTypes, networkDetail.ID, networkDetail.ObjectTypes)

	// 7. Get sample data (optional)
	if boolValue(config.IncludeSampleData) && len(objectTypesLocal) > 0 {
		s.fetchSampleData(ctx, req.KnID, objectTypesLocal, boolValue(config.SchemaBrief))
	}

	return &interfaces.KnSearchConceptResult{
		ObjectTypes:          objectTypesLocal,
		RelationTypes:        relationTypesLocal,
		ActionTypes:          actionTypesLocal,
		UnmatchedObjectTypes: unmatchedObjectTypes,
	}, nil
}

// conceptRetrievalByGroups is the concept recall path in the scenario where concept_groups is not empty.
//
// Design points:
// - Directly call BKN's typed search API (SearchObjectTypes / SearchRelationTypes /.
// SearchActionTypes), concept_groups range filtering is completed by BKN; no longer relied on.
// GetKnowledgeNetworkDetail with local filtering.
// - If any of the three calls fails, an upward transparent transmission error will occur immediately (including the scenario where the group does not exist). There is no intention here.
// BKN errors (e.g. 5xx + "all concept group not found ..." returned by unknown group)
// Swallowing an empty result, the caller needs to distinguish between "the grouping is legal but has no concept" and "the grouping itself does not exist".
// The BKN side returning 5xx when the grouping is unknown is a known semantic deviation and should be changed to 4xx through an independent issue.
// ContextLoader does not translate error codes before switching.
// - relation/action may refer to objects not hit by independent object search; batch before conversion.
// Complete these object details to avoid returning schema for missing endpoint objects.
//   - Sorting, object selection, and attribute clipping reuse existing functions (rankRelationTypes/.
//     selectObjectTypesForConceptRetrieval / convertXxxToLocal / fetchSampleData），
//
// Ensure that the downstream behaviors under the two paths are aligned.
func (s *localSearchImpl) conceptRetrievalByGroups(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	config *interfaces.KnSearchConceptRetrievalConfig,
) (*interfaces.KnSearchConceptResult, error) {
	objectLimit := config.CoarseObjectLimit
	relationLimit := config.CoarseRelationLimit
	// The Action type lacks a dedicated limit; the action magnitude on the BKN side is similar to that of object and can be reused.
	// CoarseObjectLimit is enough to avoid introducing new fields to the configuration surface.
	actionLimit := config.CoarseObjectLimit

	objectReq := s.buildCoarseRecallQuery(req.KnID, req.Query, objectLimit, config.ConceptGroups)
	objectResp, err := s.bknBackend.SearchObjectTypes(ctx, objectReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[ConceptRetrieval][Groups] SearchObjectTypes failed: %v", err)
		return nil, err
	}

	relationReq := s.buildCoarseRecallQuery(req.KnID, req.Query, relationLimit, config.ConceptGroups)
	relationResp, err := s.bknBackend.SearchRelationTypes(ctx, relationReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[ConceptRetrieval][Groups] SearchRelationTypes failed: %v", err)
		return nil, err
	}

	actionReq := s.buildCoarseRecallQuery(req.KnID, req.Query, actionLimit, config.ConceptGroups)
	actionResp, err := s.bknBackend.SearchActionTypes(ctx, actionReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[ConceptRetrieval][Groups] SearchActionTypes failed: %v", err)
		return nil, err
	}

	var (
		objects   []*interfaces.ObjectType
		relations []*interfaces.RelationType
		actions   []*interfaces.ActionType
	)
	if objectResp != nil {
		objects = objectResp.Entries
	}
	if relationResp != nil {
		relations = relationResp.Entries
	}
	if actionResp != nil {
		actions = actionResp.Entries
	}

	s.logger.WithContext(ctx).Debugf(
		"[ConceptRetrieval][Groups] BKN returned: groups=%v object_types=%d relation_types=%d action_types=%d",
		config.ConceptGroups, len(objects), len(relations), len(actions),
	)

	objects, err = s.completeReferencedObjectTypes(ctx, req.KnID, objects, relations, actions)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[ConceptRetrieval][Groups] complete referenced object types failed: %v", err)
		return nil, err
	}

	// Scope after endpoint completion, not before. Two reasons pull the same way:
	//   - completion appends every object type a relation or action points at and is not already
	//     in the pool, so filtering first would let it fetch the excluded ones straight back in;
	//   - an object type the caller pinned may sit outside the concept groups and reach the pool
	//     only through completion, so filtering first would report it as non-existent.
	// It still runs before ranking and the TopK cut, which is the ordering the scope requires.
	scope := newObjectTypeScope(config.ObjectTypes, config.ExcludeObjectTypes)
	objects, unmatchedObjectTypes := scope.apply(objects)
	relations, actions = scope.applyToConcepts(objects, relations, actions)
	s.logScopeOutcome(ctx, "[Groups]", scope, objects, unmatchedObjectTypes)

	rankedRelations := s.rankConcepts(ctx, req.Query, objects, relations,
		config.TopK, req.EnableRerank, req.RerankModel, config.ObjectRerankCandidateLimit)
	selectedObjects := s.selectObjectTypesForConceptRetrieval(objects, rankedRelations, config.TopK)

	brief := boolValue(config.SchemaBrief)
	objectTypesLocal := s.convertObjectTypesToLocal(selectedObjects, brief, req.IncludeColumns)
	relationTypesLocal := s.convertRelationTypesToLocal(rankedRelations, brief)
	actionTypesLocal := s.convertActionTypesToLocal(actions, req.KnID, objects)

	if boolValue(config.IncludeSampleData) && len(objectTypesLocal) > 0 {
		s.fetchSampleData(ctx, req.KnID, objectTypesLocal, brief)
	}

	return &interfaces.KnSearchConceptResult{
		ObjectTypes:          objectTypesLocal,
		RelationTypes:        relationTypesLocal,
		ActionTypes:          actionTypesLocal,
		UnmatchedObjectTypes: unmatchedObjectTypes,
	}, nil
}

func (s *localSearchImpl) completeReferencedObjectTypes(
	ctx context.Context,
	knID string,
	objects []*interfaces.ObjectType,
	relations []*interfaces.RelationType,
	actions []*interfaces.ActionType,
) ([]*interfaces.ObjectType, error) {
	known := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		if obj == nil || obj.ID == "" {
			continue
		}
		known[obj.ID] = struct{}{}
	}

	missingSeen := map[string]struct{}{}
	missingIDs := []string{}
	addMissing := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := known[id]; ok {
			return
		}
		if _, ok := missingSeen[id]; ok {
			return
		}
		missingSeen[id] = struct{}{}
		missingIDs = append(missingIDs, id)
	}

	for _, rel := range relations {
		if rel == nil {
			continue
		}
		addMissing(rel.SourceObjectTypeID)
		addMissing(rel.TargetObjectTypeID)
	}
	for _, action := range actions {
		if action == nil {
			continue
		}
		addMissing(action.ObjectTypeID)
	}

	if len(missingIDs) == 0 {
		return objects, nil
	}

	enriched, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, missingIDs, true)
	if err != nil {
		return nil, err
	}

	out := make([]*interfaces.ObjectType, 0, len(objects)+len(enriched))
	out = append(out, objects...)
	for _, obj := range enriched {
		if obj == nil || obj.ID == "" {
			continue
		}
		if _, ok := known[obj.ID]; ok {
			continue
		}
		known[obj.ID] = struct{}{}
		out = append(out, obj)
	}

	return out, nil
}

// scoreObjectTypes scores object types for their relevance to query (written as obj.Score).
//
// The difference from coarseRecall: it only scores, does not clip the candidate set, and is not affected by CoarseMinRelationCount.
// threshold constraints. Previously, the globally unique assignment point for obj.Score was inside coarseRecall, which required a relational type.
// Number >= CoarseMinRelationCount (default 5000), the normal-scale knowledge network is never triggered, resulting in the object type.
// The correlation never has a signal source and can only be passively brought out by the relationship endpoint (issue #778).
//
// Failure in scoring does not affect the main process: when the score is not obtained, the original relationship endpoint selection is returned, and the behavior is the same as before repair.
func (s *localSearchImpl) scoreObjectTypes(
	ctx context.Context,
	knID string,
	query string,
	objectTypes []*interfaces.ObjectType,
	config *interfaces.KnSearchConceptRetrievalConfig,
) {
	if strings.TrimSpace(query) == "" || len(objectTypes) == 0 {
		return
	}

	limit := config.CoarseObjectLimit
	if limit <= 0 {
		limit = DefaultConceptRetrievalConfig().CoarseObjectLimit
	}

	resp, err := s.bknBackend.SearchObjectTypes(ctx, s.buildCoarseRecallQuery(knID, query, limit, config.ConceptGroups))
	if err != nil {
		s.logger.WithContext(ctx).Warnf("[ScoreObjectTypes] SearchObjectTypes failed, object relevance unavailable: %v", err)
		return
	}
	if resp == nil || len(resp.Entries) == 0 {
		return
	}

	scoreByID := make(map[string]float64, len(resp.Entries))
	for _, entry := range resp.Entries {
		if entry == nil || entry.ID == "" {
			continue
		}
		scoreByID[entry.ID] = entry.Score
	}

	scored := 0
	for _, obj := range objectTypes {
		if obj == nil {
			continue
		}
		if score, ok := scoreByID[obj.ID]; ok && score > 0 {
			obj.Score = score
			scored++
		}
	}
	s.logger.WithContext(ctx).Debugf("[ScoreObjectTypes] scored %d/%d object types", scored, len(objectTypes))
}

// selectObjectTypesForConceptRetrieval selects the object types that participate in the response.
//
// The main order of sorting is the correlation between the object type itself and query (obj.Score). Relationship endpoints are no longer unique to object types.
// Source, only incorporated as schema self-consistent constraints - if the returned relationship points to an unreturned object, the caller will get.
// Decapitated quote. Therefore, the order is: topK with the highest correlation takes up space first, and then is merged into the endpoint of the selected relationship. There is still room left.
// Only complete based on relevance. Endpoint incorporation is not constrained by maxObjectCount, and self-consistency takes precedence over budget.
//
// If no object type gets a score (the scoring backend is unavailable, etc.), return to the endpoint priority before repair: at this time.
// There is no correlation, and filling the slots in a defined order will simply change behavior out of thin air.
//
// The behavior before repair is the opposite: the relationship endpoint is returned when it is full, and the object's own correlation is not involved at all, resulting in.
// Object classes that are highly matched by the query but have low (or no) relationship rankings are discarded as a whole (issue #778).
func (s *localSearchImpl) selectObjectTypesForConceptRetrieval(
	objectTypes []*interfaces.ObjectType,
	relations []*interfaces.RelationType,
	topK int,
) []*interfaces.ObjectType {
	if len(objectTypes) == 0 {
		return objectTypes
	}

	maxObjectCount := topK * objectTypeRelationMultiplier
	if len(relations) > 0 {
		maxObjectCount = maxInt(len(relations)*objectTypeRelationMultiplier, topK)
	}
	if maxObjectCount <= 0 {
		maxObjectCount = len(objectTypes)
	}

	ranked := sortObjectTypesByScore(objectTypes)
	known := make(map[string]struct{}, len(ranked))
	scoreAvailable := false
	for _, obj := range ranked {
		known[obj.ID] = struct{}{}
		if obj.Score > 0 {
			scoreAvailable = true
		}
	}

	endpoints := make(map[string]struct{}, len(relations)*2)
	for _, rel := range relations {
		if rel == nil {
			continue
		}
		for _, id := range []string{rel.SourceObjectTypeID, rel.TargetObjectTypeID} {
			if id == "" {
				continue
			}
			if _, ok := known[id]; !ok {
				continue
			}
			endpoints[id] = struct{}{}
		}
	}

	selected := make(map[string]struct{}, maxObjectCount+len(endpoints))
	// 1. The topK with the highest correlation occupies the position first: This is a direct reflection of the query intention and has a higher priority than the relationship endpoint.
	// Skip when no points are available, leaving spots for endpoints, keeping the same order as before the fix.
	if scoreAvailable {
		for _, obj := range ranked {
			if len(selected) >= topK {
				break
			}
			selected[obj.ID] = struct{}{}
		}
	}
	// 2. Incorporate the endpoints of the selected relationships to ensure that the returned relationships do not point to missing objects. There is no upper limit here:
	// If the endpoint is truncated, it is equivalent to letting the broken reference go, and the parameter layer can only make up for it from the result of this function and cannot save it.
	for id := range endpoints {
		selected[id] = struct{}{}
	}
	// 3. If there is still room left, continue to fill it up based on relevance.
	for _, obj := range ranked {
		if len(selected) >= maxObjectCount {
			break
		}
		selected[obj.ID] = struct{}{}
	}

	out := make([]*interfaces.ObjectType, 0, len(selected))
	appendSelected := func(match func(id string) bool) {
		for _, obj := range ranked {
			if _, ok := selected[obj.ID]; !ok {
				continue
			}
			if !match(obj.ID) {
				continue
			}
			out = append(out, obj)
			delete(selected, obj.ID)
		}
	}
	if !scoreAvailable {
		// The same as before the repair: endpoints are given priority, and the rest are completed in the order of definition.
		appendSelected(func(id string) bool { _, ok := endpoints[id]; return ok })
	}
	appendSelected(func(string) bool { return true })
	return out
}

// sortObjectTypesByScore sorts object types in descending order of relevance; those without scores maintain their original relative order and come last.
func sortObjectTypesByScore(objectTypes []*interfaces.ObjectType) []*interfaces.ObjectType {
	scored := make([]*interfaces.ObjectType, 0, len(objectTypes))
	unscored := make([]*interfaces.ObjectType, 0, len(objectTypes))
	for _, obj := range objectTypes {
		if obj == nil {
			continue
		}
		if obj.Score > 0 {
			scored = append(scored, obj)
		} else {
			unscored = append(unscored, obj)
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return append(scored, unscored...)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// coarseRecall coarse recall: first prune the candidate set in large-scale knowledge networks.
// Business logic: construct knn+match query conditions and call the basic search interface.
func (s *localSearchImpl) coarseRecall(
	ctx context.Context,
	knID string,
	query string,
	detail *interfaces.KnowledgeNetworkDetail,
	config *interfaces.KnSearchConceptRetrievalConfig,
) (*interfaces.KnowledgeNetworkDetail, error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, nil)

	// Construct a collection of object type IDs after rough recall.
	coarseObjectIDs := make(map[string]bool)
	coarseRelationIDs := make(map[string]bool)
	coarseObjectScores := make(map[string]float64)
	coarseRelationScores := make(map[string]float64)

	// Rough recall object type.
	objectReq := s.buildCoarseRecallQuery(knID, query, config.CoarseObjectLimit, config.ConceptGroups)
	coarseObjects, objErr := s.bknBackend.SearchObjectTypes(ctx, objectReq)
	if objErr != nil {
		s.logger.WithContext(ctx).Warnf("[CoarseRecall] SearchObjectTypes failed: %v", objErr)
	} else if coarseObjects != nil {
		for _, obj := range coarseObjects.Entries {
			coarseObjectIDs[obj.ID] = true
			if obj.Score > 0 {
				coarseObjectScores[obj.ID] = obj.Score
			}
		}
	}

	// Rough recall relationship type.
	relationReq := s.buildCoarseRecallQuery(knID, query, config.CoarseRelationLimit, config.ConceptGroups)
	coarseRelations, relErr := s.bknBackend.SearchRelationTypes(ctx, relationReq)
	if relErr != nil {
		s.logger.WithContext(ctx).Warnf("[CoarseRecall] SearchRelationTypes failed: %v", relErr)
	} else if coarseRelations != nil {
		for _, rel := range coarseRelations.Entries {
			coarseRelationIDs[rel.ID] = true
			if rel.Score > 0 {
				coarseRelationScores[rel.ID] = rel.Score
			}
		}
	}

	// Filter raw data.
	filteredDetail := &interfaces.KnowledgeNetworkDetail{
		ID:          detail.ID,
		ActionTypes: detail.ActionTypes, // ActionTypes does not do rough recall filtering.
	}

	// Filter object type.
	if len(coarseObjectIDs) > 0 {
		relationEndpointIDs := make(map[string]bool)
		if len(coarseRelationIDs) > 0 {
			for _, rel := range detail.RelationTypes {
				if !coarseRelationIDs[rel.ID] {
					continue
				}
				if rel.SourceObjectTypeID != "" {
					relationEndpointIDs[rel.SourceObjectTypeID] = true
				}
				if rel.TargetObjectTypeID != "" {
					relationEndpointIDs[rel.TargetObjectTypeID] = true
				}
			}
		}

		candidateObjectIDs := make(map[string]bool, len(coarseObjectIDs)+len(relationEndpointIDs))
		for id := range coarseObjectIDs {
			candidateObjectIDs[id] = true
		}
		for id := range relationEndpointIDs {
			candidateObjectIDs[id] = true
		}

		var pruned []*interfaces.ObjectType
		for _, obj := range detail.ObjectTypes {
			if candidateObjectIDs[obj.ID] {
				if score, ok := coarseObjectScores[obj.ID]; ok {
					obj.Score = score
				}
				pruned = append(pruned, obj)
			}
		}
		if len(pruned) > 0 {
			filteredDetail.ObjectTypes = pruned
		} else {
			filteredDetail.ObjectTypes = detail.ObjectTypes
		}
	} else {
		filteredDetail.ObjectTypes = detail.ObjectTypes
	}

	// Filter relationship types.
	if len(coarseRelationIDs) > 0 {
		var pruned []*interfaces.RelationType
		for _, rel := range detail.RelationTypes {
			if coarseRelationIDs[rel.ID] {
				if score, ok := coarseRelationScores[rel.ID]; ok {
					rel.Score = score
				}
				pruned = append(pruned, rel)
			}
		}
		if len(pruned) > 0 {
			filteredDetail.RelationTypes = pruned
		} else {
			filteredDetail.RelationTypes = detail.RelationTypes
		}
	} else {
		filteredDetail.RelationTypes = detail.RelationTypes
	}

	s.logger.WithContext(ctx).Infof("[CoarseRecall] After coarse recall: objects=%d->%d, relations=%d->%d",
		len(detail.ObjectTypes), len(filteredDetail.ObjectTypes),
		len(detail.RelationTypes), len(filteredDetail.RelationTypes))

	return filteredDetail, nil
}

// buildCoarseRecallQuery builds coarse recall query conditions.
// Business logic: Use knn + match combination query, sort by score in descending order.
func (s *localSearchImpl) buildCoarseRecallQuery(knID, query string, limit int, conceptGroups []string) *interfaces.QueryConceptsReq {
	return &interfaces.QueryConceptsReq{
		KnID:          knID,
		ConceptGroups: normalizeConceptGroups(conceptGroups),
		Cond: &interfaces.KnCondition{
			Operation: interfaces.KnOperationTypeOr,
			SubConditions: []*interfaces.KnCondition{
				{
					Field:      "*",
					Operation:  interfaces.KnOperationTypeKnn,
					Value:      query,
					ValueFrom:  interfaces.CondValueFromConst,
					LimitKey:   interfaces.CondLimitKeyK,
					LimitValue: limit,
				},
				{
					Field:     "*",
					Operation: interfaces.KnOperationTypeMatch,
					Value:     query,
					ValueFrom: interfaces.CondValueFromConst,
				},
			},
		},
		Sort: []*interfaces.KnSortParams{
			{Field: "_score", Direction: "desc"},
		},
		Limit:     limit,
		NeedTotal: false,
	}
}

// rankRelationTypes semantically ranks relationship types and takes Top-K.
// Semantic ranking using the Rerank service. Object types keep their coarse score.
func (s *localSearchImpl) rankRelationTypes(
	ctx context.Context,
	query string,
	objectTypes []*interfaces.ObjectType,
	relations []*interfaces.RelationType,
	topK int,
	enableRerank bool,
	rerankModel string,
) []*interfaces.RelationType {
	return s.rankConcepts(ctx, query, objectTypes, relations, topK, enableRerank, rerankModel, 0)
}

// rankConcepts ranks relation types and scores object types against the query in a single rerank
// call. Both concept recall paths enter here.
//
// Object types used to reach the response on their coarse score alone, and that score cannot carry
// the job: it is BM25 over ten equally weighted fields summed with a kNN channel whose 0..1 score
// is dwarfed by unbounded BM25, so lexical noise decides the order and the vector channel cannot
// correct it. A query carrying schema meta-words ("<entity> object type field") therefore ranked a
// long-commented object type above the one actually named after the entity. The reranker is the
// only component in this pipeline that can tell a meta-word from a business term, so object types
// now go through it too.
//
// The two categories share one call instead of paying for two round trips, and sharing buys a
// second property: scores from one rerank invocation are comparable, so an object type and a
// relation type can be reasoned about on the same scale.
//
// objectCandidateLimit caps how many object types enter the call; 0 leaves object types on their
// coarse score, which is what the relation-only entry point wants.
//
// Relation ranking keeps its contract exactly: same Top-K cut, same downgrade chain.
func (s *localSearchImpl) rankConcepts(
	ctx context.Context,
	query string,
	objectTypes []*interfaces.ObjectType,
	relations []*interfaces.RelationType,
	topK int,
	enableRerank bool,
	rerankModel string,
	objectCandidateLimit int,
) []*interfaces.RelationType {
	objectCandidates := objectRerankCandidates(objectTypes, objectCandidateLimit)

	if len(relations) == 0 && len(objectCandidates) == 0 {
		return relations
	}

	// When Rerank is not enabled: keep original order, only truncate Top-K (for alignment with
	// Python's current concept recall behavior). Object types keep their coarse score for the same
	// reason - the caller opted out of paying for the model.
	if !enableRerank {
		return truncateRelations(relations, topK)
	}

	documents := make([]string, 0, len(objectCandidates)+len(relations))
	for _, obj := range objectCandidates {
		documents = append(documents, buildObjectText(obj))
	}
	objectDocCount := len(documents)
	documents = append(documents, s.buildRelationDocuments(objectTypes, relations)...)

	// Call the Rerank service; model is the default reranker checked in the empty model management (#842)
	rerankResp, err := s.rerankClient.Rerank(ctx, query, documents, rerankModel)
	if err != nil || rerankResp == nil {
		s.logger.WithContext(ctx).Warnf("[RankConcepts] Rerank unavailable (%v); degrading to coarse-recall _score order", err)
		return s.degradeRelationRanking(ctx, query, relations, topK)
	}

	objectScores := make([]float64, objectDocCount)
	relationScores := make([]float64, len(relations))
	objectApplied, relationApplied := 0, 0
	for _, result := range rerankResp.Results {
		switch {
		case result.Index < 0 || result.Index >= len(documents):
		case result.Index < objectDocCount:
			objectScores[result.Index] = result.RelevanceScore
			objectApplied++
		default:
			relationScores[result.Index-objectDocCount] = result.RelevanceScore
			relationApplied++
		}
	}

	// A 200 carrying no usable index is the reranker being unavailable in a way that never surfaces
	// as an error - an unregistered model answered through an error envelope, a vendor that returned
	// an empty list. Taking it at face value would clear every coarse score and leave nothing in its
	// place, which is strictly worse than not having called the model at all. Instance reranking
	// guards this the same way.
	if objectApplied == 0 && relationApplied == 0 {
		s.logger.WithContext(ctx).Warnf("[RankConcepts] Rerank returned no usable index; degrading to coarse-recall _score order")
		return s.degradeRelationRanking(ctx, query, relations, topK)
	}

	// Write back per category, and only where the model actually answered. A vendor that returns
	// only its top N can come back with relation scores and no object score at all; zeroing the
	// object types on the strength of that would bury them for a reason the model never gave.
	if objectApplied > 0 {
		// The rerank score replaces the coarse score instead of blending with it: the two are not on
		// one scale (BM25 is unbounded, the reranker answers in 0..1), so an object type still holding
		// a coarse score would sort above every reranked one. Object types that missed the candidate
		// cut are zeroed for that same reason - they already lost on the coarse signal.
		for _, obj := range objectTypes {
			if obj != nil {
				obj.Score = 0
			}
		}
		for i, obj := range objectCandidates {
			obj.Score = objectScores[i]
		}
		s.logger.WithContext(ctx).Debugf("[RankConcepts] Reranked %d/%d object types", objectApplied, len(objectTypes))
	} else if objectDocCount > 0 {
		s.logger.WithContext(ctx).Warnf("[RankConcepts] Rerank scored no object type; keeping coarse-recall order for %d", objectDocCount)
	}

	if relationApplied == 0 && len(relations) > 0 {
		// The model answered, but not about a single relation - a vendor returning only its top N can
		// do this. Recall order is not relevance, so handing back its head would be the #788 failure
		// through a success path. Take the same ladder an unavailable reranker takes.
		s.logger.WithContext(ctx).Warnf("[RankConcepts] Rerank scored no relation type; degrading for %d", len(relations))
		return s.degradeRelationRanking(ctx, query, relations, topK)
	}

	ranked := rankRelationsByScores(relations, relationScores, topK)
	s.logger.WithContext(ctx).Debugf("[RankConcepts] Rerank completed, top_k=%d", len(ranked))
	return ranked
}

// degradeRelationRanking is the relation ranking used whenever the reranker gives nothing usable.
// Relevance is not lost when the reranker is unavailable (not registered / NameNotExist): sort by
// the coarse recall BM25 _score first, which is an existing signal at zero extra latency, and fall
// back to pure name matching only when _score is all zero (coarse recall off). Object types keep
// whatever the coarse pass gave them, which is the pre-rerank behaviour.
func (s *localSearchImpl) degradeRelationRanking(
	ctx context.Context,
	query string,
	relations []*interfaces.RelationType,
	topK int,
) []*interfaces.RelationType {
	if ranked, ok := rankRelationTypesByScore(relations, topK); ok {
		return ranked
	}
	s.logger.WithContext(ctx).Warnf("[RankConcepts] coarse-recall _score all zero; falling back to simple name match")
	return s.rankRelationTypesBySimpleMatch(query, relations, topK)
}

// objectRerankCandidates picks the object types that go into the rerank call, best coarse score
// first. The coarse BM25/kNN pass stays in front of the model as the cheap first stage: reranking
// every object type of a large network would blow the model's input budget, and the coarse score is
// the only signal available to choose the candidates with. Object types the coarse pass never
// scored still ride along while there is room, so a network whose coarse recall returned nothing at
// all is reranked whole rather than not at all.
func objectRerankCandidates(objectTypes []*interfaces.ObjectType, limit int) []*interfaces.ObjectType {
	if limit <= 0 || len(objectTypes) == 0 {
		return nil
	}
	ranked := sortObjectTypesByScore(objectTypes)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// buildRelationDocuments renders each relation for the reranker, holding index alignment with the
// input slice: a nil relation still occupies its slot.
func (s *localSearchImpl) buildRelationDocuments(
	objectTypes []*interfaces.ObjectType,
	relations []*interfaces.RelationType,
) []string {
	objectNameByID := make(map[string]string, len(objectTypes))
	for _, obj := range objectTypes {
		if obj == nil || obj.ID == "" {
			continue
		}
		name := strings.TrimSpace(obj.Name)
		if name == "" {
			name = obj.ID
		}
		objectNameByID[obj.ID] = name
	}

	documents := make([]string, len(relations))
	for i, rel := range relations {
		if rel == nil {
			continue
		}
		sourceName := objectNameByID[rel.SourceObjectTypeID]
		targetName := objectNameByID[rel.TargetObjectTypeID]
		relationName := strings.TrimSpace(rel.Name)
		if relationName == "" {
			relationName = rel.ID
		}
		documents[i] = buildRelationText(sourceName, relationName, targetName, rel.Comment)
	}
	return documents
}

// scoredRelation pairs a relation type with whatever relevance the current scorer gave it.
type scoredRelation struct {
	relation *interfaces.RelationType
	score    float64
}

// takeRelevantRelations orders by score, drops everything the scorer found no relevance in, and
// cuts to Top-K.
//
// Dropping rather than ranking is the point (#788). Every scorer on this path can come back with
// nothing relevant at all: the reranker is not registered, coarse recall is off so every _score is
// zero, the literal name match finds no substring in a multi-word query. A Top-K taken from an
// all-zero list is a relation picked by nothing but slice order - and it does not stop there,
// because relation endpoint completion then pulls its two object types into the response. One
// meaningless row becomes three, and the caller cannot tell them from real hits. An empty list is
// the honest answer, and an empty list also leaves object_types clean.
//
// Stable sort, so relations the scorer rated equally keep recall order.
func takeRelevantRelations(scored []scoredRelation, topK int) []*interfaces.RelationType {
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	result := make([]*interfaces.RelationType, 0, len(scored))
	for _, item := range scored {
		if item.score <= 0 {
			// Sorted descending: the first non-positive score ends the relevant run.
			break
		}
		if topK > 0 && len(result) >= topK {
			break
		}
		result = append(result, item.relation)
	}
	return result
}

// rankRelationsByScores orders relations by the given per-index scores and takes the relevant Top-K.
func rankRelationsByScores(relations []*interfaces.RelationType, scores []float64, topK int) []*interfaces.RelationType {
	if len(relations) == 0 {
		return relations
	}

	scored := make([]scoredRelation, len(relations))
	for i, rel := range relations {
		score := 0.0
		if i < len(scores) {
			score = scores[i]
		}
		scored[i] = scoredRelation{relation: rel, score: score}
	}
	return takeRelevantRelations(scored, topK)
}

// truncateRelations keeps the incoming order and cuts to Top-K.
func truncateRelations(relations []*interfaces.RelationType, topK int) []*interfaces.RelationType {
	if topK <= 0 || topK >= len(relations) {
		return relations
	}
	return relations[:topK]
}

// buildObjectText renders an object type for the reranker. The name carries the identity and the
// comment carries what it is for; properties are left out on purpose - they multiply the document
// by the width of the table for a signal the name and comment already carry.
func buildObjectText(obj *interfaces.ObjectType) string {
	if obj == nil {
		return ""
	}
	name := strings.TrimSpace(obj.Name)
	if name == "" {
		name = obj.ID
	}
	comment := strings.TrimSpace(obj.Comment)
	if comment == "" {
		return name
	}
	return name + "，" + comment
}

func buildRelationText(sourceName, relationName, targetName, relationComment string) string {
	var parts []string
	if strings.TrimSpace(sourceName) != "" {
		parts = append(parts, strings.TrimSpace(sourceName))
	}
	if strings.TrimSpace(relationName) != "" {
		if strings.TrimSpace(relationComment) != "" {
			parts = append(parts, strings.TrimSpace(relationName)+"，"+strings.TrimSpace(relationComment))
		} else {
			parts = append(parts, strings.TrimSpace(relationName))
		}
	}
	if strings.TrimSpace(targetName) != "" {
		parts = append(parts, strings.TrimSpace(targetName))
	}
	return strings.Join(parts, " ")
}

// rankRelationTypesByScore Sort by rough recall BM25 _score in descending order and take Top-K as Rerank.
// Dependencies in the event of unavailability preserve the downgrade path. Returning ok=false means all _scores are 0 (e.g. not enabled.
// Coarse recall), in this case you should fall back to rankRelationTypesBySimpleMatch. Using stable sorting, equal division preserves recall order.
func rankRelationTypesByScore(relations []*interfaces.RelationType, topK int) ([]*interfaces.RelationType, bool) {
	anyScore := false
	for _, rel := range relations {
		if rel != nil && rel.Score != 0 {
			anyScore = true
			break
		}
	}
	if !anyScore {
		return nil, false
	}

	scored := make([]scoredRelation, 0, len(relations))
	for _, rel := range relations {
		if rel == nil {
			continue
		}
		scored = append(scored, scoredRelation{relation: rel, score: rel.Score})
	}
	// ok=true only says coarse recall had a signal to offer; the cut still drops the relations that
	// signal did not reach, so a network where one relation scored does not drag Top-K-1 zeros along.
	return takeRelevantRelations(scored, topK), true
}

// rankRelationTypesBySimpleMatch uses simple matching to sort (fallback when Rerank fails)
func (s *localSearchImpl) rankRelationTypesBySimpleMatch(
	query string,
	relations []*interfaces.RelationType,
	topK int,
) []*interfaces.RelationType {
	// Simple relevance scoring (based on name matching)
	scored := make([]scoredRelation, len(relations))
	for i, rel := range relations {
		score := s.calculateRelevanceScore(query, rel.Name, rel.Comment)
		scored[i] = scoredRelation{relation: rel, score: score}
	}

	// calculateRelevanceScore is literal substring matching, so a multi-word query scores zero
	// against every relation far more often than not. This is the last rung of the ladder: whatever
	// it cannot vouch for, nothing downstream can.
	return takeRelevantRelations(scored, topK)
}

// calculateRelevanceScore calculates the relevance score between Query and concept.
func (s *localSearchImpl) calculateRelevanceScore(query, name, comment string) float64 {
	if strings.TrimSpace(query) == "" {
		return 0
	}

	score := 0.0

	// Name exactly matches.
	if name == query {
		score += 1.0
	}

	// Name contains Query.
	if name != "" {
		if containsFold(name, query) {
			score += 0.5
		}
		if containsFold(query, name) {
			score += 0.3
		}
	}

	// Description contains Query.
	if comment != "" && containsFold(comment, query) {
		score += 0.2
	}

	return score
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// pruneProperties attribute clipping: only retain the properties most relevant to Query.
func (s *localSearchImpl) pruneProperties(
	_ context.Context,
	query string,
	objectTypes []*interfaces.KnSearchObjectType,
	config *interfaces.KnSearchConceptRetrievalConfig,
) []*interfaces.KnSearchObjectType {
	// Collect all attributes and their scores.
	type propertyWithScore struct {
		objIndex  int
		propIndex int
		isLogic   bool
		score     float64
	}

	var allProperties []propertyWithScore

	for objIdx, obj := range objectTypes {
		for propIdx, prop := range obj.DataProperties {
			score := s.calculateRelevanceScore(query, prop.Name, prop.Comment)
			allProperties = append(allProperties, propertyWithScore{
				objIndex:  objIdx,
				propIndex: propIdx,
				isLogic:   false,
				score:     score,
			})
		}
		for propIdx, prop := range obj.LogicProperties {
			score := s.calculateRelevanceScore(query, prop.Name, prop.Comment)
			allProperties = append(allProperties, propertyWithScore{
				objIndex:  objIdx,
				propIndex: propIdx,
				isLogic:   true,
				score:     score,
			})
		}
	}

	// Sort by score descending.
	sort.Slice(allProperties, func(i, j int) bool {
		return allProperties[i].score > allProperties[j].score
	})

	// Mark attributes to keep.
	perObjectDataCount := make(map[int]int)
	perObjectLogicCount := make(map[int]int)
	keepDataProps := make(map[int]map[int]bool)
	keepLogicProps := make(map[int]map[int]bool)

	for i := range objectTypes {
		keepDataProps[i] = make(map[int]bool)
		keepLogicProps[i] = make(map[int]bool)
	}

	globalCount := 0
	for _, prop := range allProperties {
		if globalCount >= config.GlobalPropertyTopK {
			break
		}

		if prop.isLogic {
			if perObjectLogicCount[prop.objIndex] < config.PerObjectPropertyTopK {
				keepLogicProps[prop.objIndex][prop.propIndex] = true
				perObjectLogicCount[prop.objIndex]++
				globalCount++
			}
		} else {
			if perObjectDataCount[prop.objIndex] < config.PerObjectPropertyTopK {
				keepDataProps[prop.objIndex][prop.propIndex] = true
				perObjectDataCount[prop.objIndex]++
				globalCount++
			}
		}
	}

	// Apply crop.
	for objIdx, obj := range objectTypes {
		var filteredDataProps []*interfaces.KnSearchDataProperty
		for propIdx, prop := range obj.DataProperties {
			if keepDataProps[objIdx][propIdx] {
				filteredDataProps = append(filteredDataProps, prop)
			}
		}
		obj.DataProperties = filteredDataProps

		var filteredLogicProps []*interfaces.KnSearchLogicProperty
		for propIdx, prop := range obj.LogicProperties {
			if keepLogicProps[objIdx][propIdx] {
				filteredLogicProps = append(filteredLogicProps, prop)
			}
		}
		obj.LogicProperties = filteredLogicProps
	}

	return objectTypes
}

// fetchSampleData gets sample data.
func (s *localSearchImpl) fetchSampleData(ctx context.Context, knID string, objectTypes []*interfaces.KnSearchObjectType, schemaBrief bool) {
	for _, obj := range objectTypes {
		// Call instance retrieval to obtain a piece of sample data.
		req := &interfaces.QueryObjectInstancesReq{
			KnID:               knID,
			OtID:               obj.ConceptID,
			IncludeTypeInfo:    false,
			IncludeLogicParams: false,
			Limit:              1,
		}

		resp, err := s.ontologyQuery.QueryObjectInstances(ctx, req)
		if err != nil {
			s.logger.WithContext(ctx).Warnf("[FetchSampleData] Failed to fetch sample for %s: %v", obj.ConceptID, err)
			continue
		}

		if len(resp.Data) > 0 {
			if dataMap, ok := resp.Data[0].(map[string]any); ok {
				if !schemaBrief {
					obj.SampleData = dataMap
					continue
				}
				briefSample := make(map[string]any, len(dataMap))
				for k, v := range dataMap {
					if k == "_score" {
						continue
					}
					briefSample[k] = v
				}
				obj.SampleData = briefSample
			}
		}
	}
}

// ==================== Type conversion function ====================.

// mappedFieldColumn mapped_field from data property (untyped map, of the form {"name": "..."})
// Extract physical column names; fallback to logical name fallback when mapped_field is missing or formatted abnormally.
func mappedFieldColumn(mappedField any, fallback string) string {
	if m, ok := mappedField.(map[string]any); ok {
		if name, ok := m["name"].(string); ok && name != "" {
			return name
		}
	}
	return fallback
}

// convertObjectTypesToLocal maps object types to local response structures; brief controls the scope of included fields;
// When includeColumns is true, the physical column name (mapped_field) of each attribute is additionally populated for use by run_sql.
func (s *localSearchImpl) convertObjectTypesToLocal(objects []*interfaces.ObjectType, brief bool, includeColumns bool) []*interfaces.KnSearchObjectType {
	result := make([]*interfaces.KnSearchObjectType, len(objects))
	for i, obj := range objects {
		var conceptType string
		var primaryKeys []string
		var tags []string
		// data_source is always retained: brief also requires data_source.id to write run_sql.
		dataSource := obj.DataSource
		if !brief {
			conceptType = "object_type"
			primaryKeys = obj.PrimaryKeys
			tags = obj.Tags
		}
		localObj := &interfaces.KnSearchObjectType{
			ConceptType: conceptType,
			ConceptID:   obj.ID,
			ConceptName: obj.Name,
			Comment:     obj.Comment,
			Tags:        tags,
			DataSource:  dataSource,
			PrimaryKeys: primaryKeys,
			Score:       obj.Score,
		}

		if len(obj.DataProperties) > 0 {
			localObj.DataProperties = make([]*interfaces.KnSearchDataProperty, len(obj.DataProperties))
			for j, prop := range obj.DataProperties {
				p := &interfaces.KnSearchDataProperty{
					Name:                prop.Name,
					Type:                prop.Type,
					ConditionOperations: prop.ConditionOperations,
				}
				if !brief {
					p.Comment = prop.Comment
				}
				if includeColumns {
					p.Column = mappedFieldColumn(prop.MappedField, prop.Name)
				}
				localObj.DataProperties[j] = p
			}
		}

		// Convert logic properties (full fields: DataSource, Parameters)
		if len(obj.LogicProperties) > 0 {
			localObj.LogicProperties = make([]*interfaces.KnSearchLogicProperty, len(obj.LogicProperties))
			for j, prop := range obj.LogicProperties {
				lp := &interfaces.KnSearchLogicProperty{
					Name:       prop.Name,
					Type:       string(prop.Type),
					DataSource: prop.DataSource,
					Parameters: prop.Parameters,
				}
				if !brief {
					lp.Comment = prop.Comment
				}
				localObj.LogicProperties[j] = lp
			}
		}

		result[i] = localObj
	}
	return result
}

// convertRelationTypesToLocal converts relation types to local response format, aligned with Python schema_brief:
// - brief=true: only return concept_id, concept_name, source_object_type_id, target_object_type_id (excluding concept_type, comment)
// - brief=false: Return the complete field (including concept_type, comment)
func (s *localSearchImpl) convertRelationTypesToLocal(relations []*interfaces.RelationType, brief bool) []*interfaces.KnSearchRelationType {
	result := make([]*interfaces.KnSearchRelationType, len(relations))
	for i, rel := range relations {
		var conceptType, comment string
		if !brief {
			conceptType = "relation_type"
			comment = rel.Comment
		}
		result[i] = &interfaces.KnSearchRelationType{
			ConceptType:        conceptType,
			ConceptID:          rel.ID,
			ConceptName:        rel.Name,
			Comment:            comment,
			SourceObjectTypeID: rel.SourceObjectTypeID,
			TargetObjectTypeID: rel.TargetObjectTypeID,
		}
	}
	return result
}

func (s *localSearchImpl) convertActionTypesToLocal(
	actions []*interfaces.ActionType,
	knID string,
	objectTypes []*interfaces.ObjectType,
) []*interfaces.KnSearchActionType {
	objNameMap := make(map[string]string, len(objectTypes))
	for _, obj := range objectTypes {
		if obj == nil || obj.ID == "" {
			continue
		}
		objNameMap[obj.ID] = obj.Name
	}

	result := make([]*interfaces.KnSearchActionType, len(actions))
	for i, act := range actions {
		result[i] = &interfaces.KnSearchActionType{
			ID:             act.ID,
			Name:           act.Name,
			ObjectTypeID:   act.ObjectTypeID,
			ObjectTypeName: objNameMap[act.ObjectTypeID],
			Comment:        act.Comment,
			Tags:           act.Tags,
			KnID:           knID,
		}
	}
	return result
}
