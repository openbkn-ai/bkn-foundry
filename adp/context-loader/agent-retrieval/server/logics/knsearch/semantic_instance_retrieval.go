// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (semantic instance recall)
// file: semantic_instance_retrieval.go
package knsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Recall channel names are logged to identify which path has a problem.
const (
	channelKnn   = "knn"
	channelMatch = "match"
)

// retrievalChannel A recall channel: an independently issued query.
type retrievalChannel struct {
	name string
	cond *interfaces.KnCondition
}

// channelOutcome is the recall result of a single channel. scored records whether this path returned _score.
// If not (fall back to the source store for direct query) the response order does not represent relevance, the ranking is meaningless, and cannot be used for RRF.
type channelOutcome struct {
	name   string
	nodes  []*interfaces.KnSearchNode
	scored bool
	err    error
}

// semanticInstanceRetrieval semantic instance recall main logic.
// Process: Traverse object types -> Vector retrieval -> Scoring and sorting -> Global score filtering -> Attribute filtering.
func (s *localSearchImpl) semanticInstanceRetrieval(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objectTypes []*interfaces.KnSearchObjectType,
	config *interfaces.KnSearchRetrievalConfig,
) (*interfaces.KnSearchSemanticInstanceResult, error) {
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	if len(objectTypes) == 0 {
		return &interfaces.KnSearchSemanticInstanceResult{
			Message: infraErr.LocalizedDetail(ctx, "NoSearchableObjectTypes"),
		}, nil
	}

	instanceConfig := config.SemanticInstanceRetrieval
	propertyConfig := config.PropertyFilter

	// Drop the object types the query has nothing to do with before paying for them. A request may
	// override the deployment's calibrated ratio, the same way it may override the relevance gate.
	objectTypeRatio := instanceConfig.MinObjectTypeScoreRatio
	if objectTypeRatio <= 0 && s.config != nil {
		objectTypeRatio = s.config.InstanceSearchConfig.MinObjectTypeScoreRatio
	}
	objectTypes = s.filterObjectTypesByScore(ctx, objectTypes, objectTypeRatio)

	// Hold instance recall to the number of object types the caller asked for.
	//
	// Concept recall deliberately returns more than top_k: it pulls in the endpoints of the relations
	// it selected so the schema it hands back has no dangling references, which can double the list.
	// That is right for the schema half of the answer and wrong for this half — every extra object
	// type here is at least one more downstream query, and with a vector channel one more embedding
	// call. Left uncapped, max_object_types=10 issued queries against 20 object types, so the knob
	// that is supposed to bound the cost understated it by half.
	if limit := conceptTopK(config); limit > 0 && len(objectTypes) > limit {
		s.logger.WithContext(ctx).Debugf(
			"[SemanticInstanceRetrieval] Capping instance recall at max_object_types=%d (concept recall selected %d)",
			limit, len(objectTypes))
		objectTypes = objectTypes[:limit]
	}

	// The relevance gate: a request may override the deployment's calibrated threshold.
	minRerankerScore := instanceConfig.MinRerankerScore
	if minRerankerScore <= 0 && s.config != nil {
		minRerankerScore = s.config.InstanceSearchConfig.MinRerankerScore
	}
	if minRerankerScore > 0 && normalizeRerankMode(instanceConfig.InstanceRerankMode) == InstanceRerankModeOff {
		// Turning the gate on implies paying for the model: it is the only score in this pipeline that
		// judges relevance in absolute terms. The fusion score cannot serve — a channel's top row
		// scores 1.0 whether or not anything in the object type has to do with the query.
		gated := *instanceConfig
		gated.InstanceRerankMode = InstanceRerankModeOn
		instanceConfig = &gated
		s.logger.WithContext(ctx).Infof(
			"[SemanticInstanceRetrieval] min_reranker_score=%.4f is set, enabling rerank for this query",
			minRerankerScore)
	}

	// One lookup of what each object type indexed for semantic recall, reused by reranking so the
	// document it sends the model leads with those fields.
	searchableByType := make(map[string][]searchableField, len(objectTypes))
	for _, objType := range objectTypes {
		if objType == nil {
			continue
		}
		searchableByType[objType.ConceptID] = findSemanticSearchableFields(objType)
	}

	// Query the object types concurrently, bounded. Serially, latency was the sum of every object
	// type's round trip: each one issues its channel queries and, where a vector channel applies,
	// waits on its own embedding call for the same query string.
	//
	// Results are collected per position, not appended as they arrive, so the response does not depend
	// on which object type happened to answer first.
	perType := make([][]*interfaces.KnSearchNode, len(objectTypes))
	concurrency := instanceConfig.ObjectTypeConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(objectTypes) {
		concurrency = len(objectTypes)
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	for i, objType := range objectTypes {
		wg.Add(1)
		go func(idx int, objType *interfaces.KnSearchObjectType) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			nodes, err := s.retrieveInstancesForObjectType(ctx, req, objType, instanceConfig, knnAllowedFor(objType, instanceConfig))
			if err != nil {
				// One object type failing keeps the others: a single bad index or a knn 400 on a field
				// whose declared operators lie must not empty the whole result.
				s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Failed to retrieve instances for %s: %v",
					objType.ConceptID, err)
				return
			}
			perType[idx] = nodes
		}(i, objType)
	}
	wg.Wait()

	var allNodes []*interfaces.KnSearchNode
	var maxScore float64
	for _, nodes := range perType {
		for _, node := range nodes {
			if node.Score > maxScore {
				maxScore = node.Score
			}
		}
		allNodes = append(allNodes, nodes...)
	}

	s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Retrieved %d instances from %d object types, max_score=%.4f",
		len(allNodes), len(objectTypes), maxScore)

	// Order across object types, not just inside each one. Until now the rows were returned in the
	// order the object types happened to be recalled in, so a row scoring 2.0 could sit below a 1.94
	// from an object type that was simply processed earlier -- and the caller had no way to tell that
	// the sequence carried no meaning. The RRF anchor (first place in one channel = 1.0, in both = 2.0)
	// is what makes rows from different object types comparable in the first place.
	sortNodesByScore(allNodes)

	// Global score filtering.
	if boolValue(instanceConfig.EnableGlobalFinalScoreRatioFilter) && maxScore > 0 && len(allNodes) > 0 {
		threshold := maxScore * instanceConfig.GlobalFinalScoreRatio
		var topNode *interfaces.KnSearchNode
		for _, n := range allNodes {
			if topNode == nil || n.Score > topNode.Score {
				topNode = n
			}
		}
		allNodes = s.filterNodesByScore(allNodes, threshold)
		if len(allNodes) == 0 && topNode != nil {
			allNodes = []*interfaces.KnSearchNode{topNode}
			s.logger.WithContext(ctx).Debugf("[SemanticInstanceRetrieval] Global score filter kept at least one (top score=%.4f)", topNode.Score)
		}
		s.logger.WithContext(ctx).Debugf("[SemanticInstanceRetrieval] After global score filter (threshold=%.4f): %d nodes",
			threshold, len(allNodes))
	}

	// Fine ranking (default off). Placed before attribute filtering: filterNodeProperties will reduce the number of attributes and truncate the value.
	// Sending truncated text to the model would judge relevance from incomplete text.
	allNodes, rerank := s.rerankInstances(ctx, req.Query, allNodes, instanceConfig, searchableByType)

	var gateMessage string
	if minRerankerScore > 0 {
		allNodes, gateMessage = s.applyRelevanceGate(ctx, allNodes, minRerankerScore, rerank)
	}

	// Property filtering.
	if boolValue(propertyConfig.EnablePropertyFilter) {
		allNodes = s.filterNodeProperties(allNodes, propertyConfig)
	}

	// Stamp the final order last, after every step that can reorder or drop rows. rank is what the
	// caller sorts by; which score produced it (fusion, heuristic, or the reranker) is evidence.
	for i, node := range allNodes {
		node.Rank = i + 1
	}

	result := &interfaces.KnSearchSemanticInstanceResult{
		Nodes: allNodes,
	}

	switch {
	case gateMessage != "":
		// The gate has more to say than "nothing matched": it either rejected a batch it did judge, or
		// could not judge at all. Both readings change what the caller should do next.
		result.Message = gateMessage
	case len(allNodes) == 0:
		result.Message = infraErr.LocalizedDetail(ctx, "NoMatchingInstances")
	}

	return result, nil
}

// filterObjectTypesByScore drops object types concept recall scored far below its best one.
//
// Every object type kept here costs at least one downstream query, and one embedding call where a
// vector channel applies, so the cheapest instance query is the one never issued. The scores come
// from BKN's concept search and have no fixed scale, which is why the threshold is a fraction of the
// best score of the same recall rather than an absolute number: that is the one comparison this
// signal supports.
//
// Two guards keep the filter from turning a narrow answer into no answer:
//   - no scores at all (every object type at 0) means concept recall had no signal to give, not that
//     nothing is relevant, so nothing is dropped;
//   - the best-scoring object type is always kept, so the filter can narrow a query, never empty it.
//
// It logs what it saw either way: the spread between the best and worst object type is what a
// deployment needs in order to choose a ratio, and it is not visible from the response.
func (s *localSearchImpl) filterObjectTypesByScore(
	ctx context.Context,
	objectTypes []*interfaces.KnSearchObjectType,
	ratio float64,
) []*interfaces.KnSearchObjectType {
	if len(objectTypes) <= 1 {
		return objectTypes
	}

	best, worst := 0.0, math.Inf(1)
	for _, objType := range objectTypes {
		if objType == nil {
			continue
		}
		if objType.Score > best {
			best = objType.Score
		}
		if objType.Score < worst {
			worst = objType.Score
		}
	}
	if best <= 0 {
		s.logger.WithContext(ctx).Debugf(
			"[ObjectTypePreFilter] Concept recall scored no object type, keeping all %d", len(objectTypes))
		return objectTypes
	}
	s.logger.WithContext(ctx).Debugf(
		"[ObjectTypePreFilter] Object type scores across %d types: best=%.4f worst=%.4f ratio=%.4f",
		len(objectTypes), best, worst, ratio)
	if ratio <= 0 {
		return objectTypes
	}

	threshold := best * ratio
	kept := make([]*interfaces.KnSearchObjectType, 0, len(objectTypes))
	dropped := make([]string, 0)
	unscored := 0
	for _, objType := range objectTypes {
		if objType == nil {
			continue
		}
		// Zero is not a low score, it is no score. An object type reaches the candidate set without
		// ever being scored in several ordinary ways: concept search only scores what it matched, the
		// endpoints of a selected relation are completed in afterwards, and object_types pinned by the
		// caller are applied before scoring runs at all. Dropping those would silently skip the very
		// object type a caller named by hand.
		if objType.Score <= 0 {
			unscored++
			kept = append(kept, objType)
			continue
		}
		// Keep the best one whatever the threshold says: a query that recalled something should not
		// come back empty because every candidate sat just under the bar.
		if objType.Score >= threshold || objType.Score == best {
			kept = append(kept, objType)
			continue
		}
		dropped = append(dropped, objType.ConceptID)
	}
	if unscored > 0 {
		s.logger.WithContext(ctx).Debugf(
			"[ObjectTypePreFilter] Kept %d object types concept recall never scored", unscored)
	}
	if len(dropped) > 0 {
		s.logger.WithContext(ctx).Infof(
			"[ObjectTypePreFilter] Skipped %d object types below %.4f (best=%.4f): %v",
			len(dropped), threshold, best, dropped)
	}
	return kept
}

// conceptTopK reports how many object types the caller allowed into instance recall, or 0 when the
// request carries no concept retrieval config to read it from.
func conceptTopK(config *interfaces.KnSearchRetrievalConfig) int {
	if config == nil || config.ConceptRetrieval == nil {
		return 0
	}
	return config.ConceptRetrieval.TopK
}

// applyRelevanceGate drops the instances the reranker scored below the threshold, and returns the
// message the caller has to be told when the outcome is not a plain list of results.
//
// Three outcomes, and they must stay distinguishable:
//   - the model judged and nothing cleared the bar: return empty, and say the batch was rejected;
//   - the model never ran, or scored every candidate identically: do not filter, and say the gate did
//     not run. Filtering on a judgement we do not have would be inventing one;
//   - rows the model never saw (past the rerank window) are dropped when the gate is on: the gate
//     means "only what the model vouched for", and an unjudged row is not that.
func (s *localSearchImpl) applyRelevanceGate(
	ctx context.Context,
	nodes []*interfaces.KnSearchNode,
	threshold float64,
	rerank rerankOutcome,
) ([]*interfaces.KnSearchNode, string) {
	if len(nodes) == 0 {
		return nodes, ""
	}
	if !rerank.scored || rerank.unavailable || rerank.degenerate {
		reason := "unavailable"
		if rerank.degenerate {
			reason = "degenerate score distribution"
		}
		s.logger.WithContext(ctx).Warnf(
			"[RelevanceGate] Skipped (%s): returning %d unfiltered instances", reason, len(nodes))
		return nodes, infraErr.LocalizedDetail(ctx, "InstanceRelevanceGateSkipped",
			formatScore(threshold))
	}

	kept := make([]*interfaces.KnSearchNode, 0, len(nodes))
	for _, node := range nodes {
		if node.RerankerScore >= threshold {
			kept = append(kept, node)
		}
	}
	s.logger.WithContext(ctx).Infof(
		"[RelevanceGate] threshold=%.4f top=%.4f kept=%d/%d", threshold, rerank.top, len(kept), len(nodes))

	if len(kept) == 0 {
		return nil, infraErr.LocalizedDetail(ctx, "InstanceRelevanceGateRejected",
			formatScore(threshold), formatScore(rerank.top))
	}
	return kept, ""
}

// formatScore renders a score for a message: short enough to read, precise enough to compare with
// the reranker_score values in the same response.
func formatScore(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}

// retrieveInstancesForObjectType performs semantic retrieval of individual object types.
//
// By default, two channels are used: knn and match each send one query, then RRF fusion is performed by rank. Splitting the requests is required.
// When combined in an OR, OpenSearch directly adds the two clauses, and the knn score falls between 0 and 1, while BM25 has no upper bound.
// Vector hits will be squeezed out of InitialCandidateCount candidates, and no amount of repairs in the sorting stage can save them.
// However, the separated clause scores cannot be obtained: named queries only answer whether it is hit or not, and the clause scores can only be obtained by explain.
func (s *localSearchImpl) retrieveInstancesForObjectType(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objType *interfaces.KnSearchObjectType,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	allowKnn bool,
) ([]*interfaces.KnSearchNode, error) {
	searchable := findSemanticSearchableFields(objType)
	if len(searchable) == 0 {
		s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Object type %s has no semantic-searchable properties, skip", objType.ConceptID)
		return nil, nil
	}

	if boolValue(config.EnableRRFFusion) {
		return s.retrieveInstancesFused(ctx, req, objType, config, searchable, allowKnn)
	}
	return s.retrieveInstancesSingleQuery(ctx, req, objType, config, searchable, allowKnn)
}

// retrieveInstancesFused dual-channel recall + RRF fusion.
func (s *localSearchImpl) retrieveInstancesFused(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objType *interfaces.KnSearchObjectType,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	searchable []searchableField,
	allowKnn bool,
) ([]*interfaces.KnSearchNode, error) {
	channels := make([]retrievalChannel, 0, 2)
	if cond := buildKnnOnlyCondition(req.Query, searchable, config, allowKnn); cond != nil {
		channels = append(channels, retrievalChannel{name: channelKnn, cond: cond})
	}
	if cond := buildMatchOnlyCondition(req.Query, searchable, config); cond != nil {
		channels = append(channels, retrievalChannel{name: channelMatch, cond: cond})
	}
	if len(channels) == 0 {
		s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Object type %s has no index-backed condition to issue, skip", objType.ConceptID)
		return nil, nil
	}

	outcomes := make([]channelOutcome, len(channels))
	var wg sync.WaitGroup
	for i := range channels {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			outcomes[idx] = s.fetchChannel(ctx, req, objType, config, channels[idx])
		}(i)
	}
	wg.Wait()

	live := make([]channelOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		if o.err != nil {
			// Failure of a single channel does not destroy the entire object type. Knn returns 400 when hitting a field without vector mapping.
			// (condition_operations is declared by the network builder and stored as it is, so it is not trustworthy). In the past, this 400.
			// It will fail together with match, and no instance of the object type will be recalled.
			s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Channel %s failed for %s: %v",
				o.name, objType.ConceptID, o.err)
			continue
		}
		live = append(live, o)
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("all retrieval channels failed for object type %s", objType.ConceptID)
	}

	anyScored := false
	for _, o := range live {
		if o.scored {
			anyScored = true
			break
		}
	}

	var nodes []*interfaces.KnSearchNode
	if anyScored {
		nodes = fuseByRRF(live, rrfK(config), channelWeights(config))
	} else {
		// The source-store direct query path has no _score; response order is the store's natural order and rank is meaningless.
		// Fall back to local scoring. The 0/0.3/0.5/0.85 tiers were designed for this path.
		nodes = mergeChannelNodes(live)
		s.scoreNodes(req.Query, nodes, searchable, config)
	}

	sortNodesByScore(nodes)

	if config.PerTypeInstanceLimit > 0 && len(nodes) > config.PerTypeInstanceLimit {
		nodes = nodes[:config.PerTypeInstanceLimit]
	}

	// MinDirectRelevance is an absolute threshold and is only meaningful for local bottom-line scoring. A score higher than RRF will result in.
	// Filtering out all results (RRF component level at 0.0x), hitting the original _score is another kind of bad: BM25 line.
	// If the value is always greater than the threshold, all will be let go, and pure vector rows will be randomly dropped at the edge.
	if !anyScored {
		nodes = s.filterLowRelevanceNodes(nodes, config.MinDirectRelevance)
	}

	return nodes, nil
}

// fetchChannel issues a query and converts it into a node, and records whether the query contains _score.
func (s *localSearchImpl) fetchChannel(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objType *interfaces.KnSearchObjectType,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	ch retrievalChannel,
) channelOutcome {
	queryReq := &interfaces.QueryObjectInstancesReq{
		KnID:               req.KnID,
		OtID:               objType.ConceptID,
		IncludeTypeInfo:    true,
		IncludeLogicParams: false,
		Limit:              config.InitialCandidateCount,
		Cond:               ch.cond,
	}

	resp, err := s.ontologyQuery.QueryObjectInstances(ctx, queryReq)
	if err != nil {
		return channelOutcome{name: ch.name, err: fmt.Errorf("query instances failed: %w", err)}
	}

	out := channelOutcome{name: ch.name, nodes: make([]*interfaces.KnSearchNode, 0, len(resp.Data))}
	for _, data := range resp.Data {
		dataMap, ok := data.(map[string]any)
		if !ok {
			continue
		}
		if _, has := dataMap["_score"]; has {
			out.scored = true
		}
		node := s.convertToKnSearchNode(objType, dataMap)
		node.RecallScore = node.Score
		// Record which channel produced this number while that is still known: after fusion the row
		// carries scores from both channels and one raw float can no longer say where it came from.
		switch ch.name {
		case channelMatch:
			node.BM25Score = node.Score
		case channelKnn:
			node.KnnScore = node.Score
		}
		out.nodes = append(out.nodes, node)
	}

	// Relative score filtering is placed within the channel, before fusion - this is the only place where the original scores are comparable: the same object type,
	// The same index, the same query, and the same operator. After fusion, there is nothing you can do: RRF points only express rankings.
	// The first place is always 1.0, even if it is actually irrelevant. The message "the whole is not relevant" cannot be expressed in the ranking.
	if out.scored && boolValue(config.EnableGlobalFinalScoreRatioFilter) && config.GlobalFinalScoreRatio > 0 {
		out.nodes = pruneChannelByScoreRatio(out.nodes, config.GlobalFinalScoreRatio)
	}
	return out
}

// pruneChannelByScoreRatio discards the rows that are too far apart from the highest score of this channel, and the row with the highest score is always retained.
func pruneChannelByScoreRatio(nodes []*interfaces.KnSearchNode, ratio float64) []*interfaces.KnSearchNode {
	if len(nodes) <= 1 {
		return nodes
	}
	maxScore := 0.0
	for _, n := range nodes {
		if n.RecallScore > maxScore {
			maxScore = n.RecallScore
		}
	}
	if maxScore <= 0 {
		return nodes
	}
	threshold := maxScore * ratio
	kept := make([]*interfaces.KnSearchNode, 0, len(nodes))
	for _, n := range nodes {
		if n.RecallScore >= threshold {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return nodes[:1]
	}
	return kept
}

// retrieveInstancesSingleQuery Old path: single OR query. Only go when enable_rrf_fusion=false,
// Serves as an escape door when something goes wrong with the new fusion logic.
func (s *localSearchImpl) retrieveInstancesSingleQuery(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objType *interfaces.KnSearchObjectType,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	searchable []searchableField,
	allowKnn bool,
) ([]*interfaces.KnSearchNode, error) {
	cond := s.buildSemanticSearchConditionStruct(req.Query, searchable, config, allowKnn)
	if cond == nil {
		s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Object type %s has no index-backed condition to issue, skip", objType.ConceptID)
		return nil, nil
	}

	// Call ontology-query to retrieve instances.
	queryReq := &interfaces.QueryObjectInstancesReq{
		KnID:               req.KnID,
		OtID:               objType.ConceptID,
		IncludeTypeInfo:    true,
		IncludeLogicParams: false,
		Limit:              config.InitialCandidateCount,
		Cond:               cond,
	}

	resp, err := s.ontologyQuery.QueryObjectInstances(ctx, queryReq)
	if err != nil {
		return nil, fmt.Errorf("query instances failed: %w", err)
	}

	// Convert to KnSearchNode format.
	nodes := make([]*interfaces.KnSearchNode, 0, len(resp.Data))
	for _, data := range resp.Data {
		if dataMap, ok := data.(map[string]any); ok {
			node := s.convertToKnSearchNode(objType, dataMap)
			node.RecallScore = node.Score
			// This path issues one OR query carrying both operators, so a scored row's number is a
			// BM25 score and a similarity added together by OpenSearch — attributable to neither
			// channel. Only when no vector condition went out can the score be named.
			if !allowKnn {
				node.BM25Score = node.Score
			}
			nodes = append(nodes, node)
		}
	}

	// Calculate relevance score.
	s.scoreNodes(req.Query, nodes, searchable, config)

	// Sort by score descending.
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Score > nodes[j].Score
	})

	// Take Top-K.
	if len(nodes) > config.PerTypeInstanceLimit {
		nodes = nodes[:config.PerTypeInstanceLimit]
	}

	// Filter low relevance nodes.
	nodes = s.filterLowRelevanceNodes(nodes, config.MinDirectRelevance)

	return nodes, nil
}

// buildSemanticSearchConditionStruct builds the semantic search condition structure.
// buildSemanticSearchConditionStruct spells a natural language sentence into an OR condition.
//
// Only use knn and match: knn eats the entire sentence (the sentence vector should enter the entire sentence), match eats the entire sentence and then the analyzer segments the words.
// Hit by word. Equivalent values do not participate - use the entire sentence to do == with a certain field, it will always be false, and one will be occupied in vain.
// max_sub_conditions quota, squeeze out the sub-conditions that can really hit.
func (s *localSearchImpl) buildSemanticSearchConditionStruct(
	query string,
	searchable []searchableField,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	allowKnn bool,
) *interfaces.KnCondition {
	if len(searchable) == 0 {
		return nil
	}
	maxSub := config.MaxSemanticSubConditions
	if maxSub <= 0 {
		maxSub = 10
	}
	knnLimit := config.PerTypeInstanceLimit
	if knnLimit <= 0 {
		knnLimit = 5
	}

	var subConditions []*interfaces.KnCondition

	knnBudget := 0
	if allowKnn {
		knnBudget = config.MaxKnnSubConditionsPerType
		if knnBudget <= 0 {
			knnBudget = 1
		}
	}
	for i := range searchable {
		if len(subConditions) >= maxSub || knnBudget <= 0 {
			break
		}
		f := &searchable[i]
		if !f.HasKnn {
			continue
		}
		knnBudget--
		subConditions = append(subConditions, &interfaces.KnCondition{
			Field:      f.Name,
			Operation:  interfaces.KnOperationTypeKnn,
			Value:      query,
			ValueFrom:  interfaces.CondValueFromConst,
			LimitKey:   interfaces.CondLimitKeyK,
			LimitValue: knnLimit,
		})
	}
	for i := range searchable {
		if len(subConditions) >= maxSub {
			break
		}
		f := &searchable[i]
		if f.HasMatch {
			subConditions = append(subConditions, &interfaces.KnCondition{
				Field:     f.Name,
				Operation: interfaces.KnOperationTypeMatch,
				Value:     query,
				ValueFrom: interfaces.CondValueFromConst,
			})
		}
	}

	if len(subConditions) > maxSub {
		subConditions = subConditions[:maxSub]
	}

	// When the field only supports equal values, a sub-condition cannot be spelled out. Empty OR condition ontology-query will directly evaluate 400.
	// ("sub condition size is 0"), so nil is returned here to let the caller skip the object type.
	if len(subConditions) == 0 {
		return nil
	}

	return &interfaces.KnCondition{
		Operation:     interfaces.KnOperationTypeOr,
		SubConditions: subConditions,
	}
}

// buildKnnOnlyCondition contains only channels with vector subconditions. The object type does not have a vector field, or is not allowed to be sent in this round.
// Returns nil for vector conditions, and the caller skips this path accordingly.
//
// k follows PerTypeInstanceLimit instead of InitialCandidateCount: the cost of vector retrieval grows with k,
// The function of this path is to "guarantee quota" - send the most relevant ones into the fusion pool, as long as it is enough. The benefit of increasing k.
// It needs support from recall experiments and falls within the scope of #708.
func buildKnnOnlyCondition(
	query string,
	searchable []searchableField,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	allowKnn bool,
) *interfaces.KnCondition {
	if !allowKnn || len(searchable) == 0 {
		return nil
	}
	budget := config.MaxKnnSubConditionsPerType
	if budget <= 0 {
		budget = 1
	}
	knnLimit := config.PerTypeInstanceLimit
	if knnLimit <= 0 {
		knnLimit = 5
	}

	var subConditions []*interfaces.KnCondition
	for i := range searchable {
		if budget <= 0 {
			break
		}
		f := &searchable[i]
		if !f.HasKnn {
			continue
		}
		budget--
		subConditions = append(subConditions, &interfaces.KnCondition{
			Field:      f.Name,
			Operation:  interfaces.KnOperationTypeKnn,
			Value:      query,
			ValueFrom:  interfaces.CondValueFromConst,
			LimitKey:   interfaces.CondLimitKeyK,
			LimitValue: knnLimit,
		})
	}
	if len(subConditions) == 0 {
		return nil
	}
	return &interfaces.KnCondition{
		Operation:     interfaces.KnOperationTypeOr,
		SubConditions: subConditions,
	}
}

// buildMatchOnlyCondition Passes containing only full-text subconditions. Equivalence does not participate: take the entire sentence and do == with a certain field.
// It is always false and will occupy a max_sub_conditions quota in vain.
func buildMatchOnlyCondition(
	query string,
	searchable []searchableField,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
) *interfaces.KnCondition {
	if len(searchable) == 0 {
		return nil
	}
	maxSub := config.MaxSemanticSubConditions
	if maxSub <= 0 {
		maxSub = 10
	}

	var subConditions []*interfaces.KnCondition
	for i := range searchable {
		if len(subConditions) >= maxSub {
			break
		}
		f := &searchable[i]
		if f.HasMatch {
			subConditions = append(subConditions, &interfaces.KnCondition{
				Field:     f.Name,
				Operation: interfaces.KnOperationTypeMatch,
				Value:     query,
				ValueFrom: interfaces.CondValueFromConst,
			})
		}
	}
	if len(subConditions) == 0 {
		return nil
	}
	return &interfaces.KnCondition{
		Operation:     interfaces.KnOperationTypeOr,
		SubConditions: subConditions,
	}
}

// rrfK takes the fusion constant, and non-positive values ​​fall back to the default value of 60.
func rrfK(config *interfaces.KnSearchSemanticInstanceRetrievalConfig) int {
	if config != nil && config.RRFK > 0 {
		return config.RRFK
	}
	return 60
}

// channelWeights parses the weight of each channel. knn takes knn_weight, match takes 1-knn_weight,
// The remaining channels (if any in the future) are treated with equal weight.
//
// Clamp the out-of-bounds value to [0,1] instead of reporting an error: mismatching one number should not cause the entire search chain to fail, let alone this knob.
// There is currently no recall experimental support (same as #708), and there should be no hard failure path.
func channelWeights(config *interfaces.KnSearchSemanticInstanceRetrievalConfig) map[string]float64 {
	w := 0.5
	if config != nil && config.KnnWeight != nil {
		w = *config.KnnWeight
	}
	if w < 0 {
		w = 0
	}
	if w > 1 {
		w = 1
	}
	return map[string]float64{
		channelKnn:   w,
		channelMatch: 1 - w,
	}
}

// weightOf takes the weight of a certain channel; unregistered channels are treated with an equal weight of 0.5.
func weightOf(weights map[string]float64, channel string) float64 {
	if v, ok := weights[channel]; ok {
		return v
	}
	return 0.5
}

// fuseByRRF fuses multi-channel recall according to Reciprocal Rank Fusion:
//
//	score = Σ w_i/(k + rank_i) × 2(k+1)
//
// The reason for using rankings instead of scores is that the scores of the two routes are fundamentally different dimensions: knn scores are between 0 and 1, and BM25 has no upper bound and spans indexes.
// Not comparable. The normalized weighted sum can also make do, but min-max is very noisy when there are few candidates and the scores are concentrated (top1 is always.
// 1.0), and the weight must be tuned manually for each knowledge network; RRF has only one constant and does not need cross-network tuning.
//
// Multiplying by 2(k+1) just changes the dimensions: **When the weights are equal (default 0.5)** the first place in any way will get exactly 1.0, and both ways will get exactly 1.0.
// 1st gets 2.0, exactly matching the old unweighted Σ1/(k+rank)×(k+1). This gives cross-object-type comparison
// a stable anchor: "rank 1 in any channel it can send" is 1.0 in every object type, regardless of how many channels
// that type sends. Instances hit by both paths score higher, which is a real signal. The magnitude is also comparable
// with local fallback scores (0~0.85) from paths without _score, so both path results can enter the same global ratio filter pool.
// They won't erase each other.
//
// Don't divide by the number of channels: doing so will push "instances of a dual-channel object type that are only hit by one channel" to single-channel.
// Half of the sub-instances of the object type with the same name (VM measured 0.5 vs 1.0), the offset just changes the direction. missing another way.
// It has already been reflected by "one less item added" and does not need to be punished again.
//
// After the weight deviates from 0.5, the above cross-category anchor point will follow the tilt: increase the vector weight, there is no vector field.
// Object classes as a whole are suppressed. That's a result of the caller's declared preference, not a bug - but that's why,
// The default value must be left at 0.5.
func fuseByRRF(outcomes []channelOutcome, k int, weights map[string]float64) []*interfaces.KnSearchNode {
	if len(outcomes) == 0 {
		return nil
	}
	if k <= 0 {
		k = 60
	}

	type entry struct {
		node  *interfaces.KnSearchNode
		score float64
	}
	byKey := make(map[string]*entry)
	order := make([]string, 0)

	for _, o := range outcomes {
		for rank, node := range o.nodes {
			key := instanceKey(node)
			if key == "" {
				// There is neither a unique identifier nor an instance name: it cannot be safely determined whether it is the same instance as other rows.
				// Rather than duplicating it by mistake and merging two different instances, give it a key that won't collide.
				key = fmt.Sprintf("%s|anon:%s:%d", node.ObjectTypeID, o.name, rank)
			}
			e, ok := byKey[key]
			if !ok {
				e = &entry{node: node}
				byKey[key] = e
				order = append(order, key)
			} else if node.RecallScore > e.node.RecallScore {
				// The same instance is recalled in both ways: the attribute content is consistent (the two ways request the same set of fields),
				// Only the larger original recall score is retained for observation.
				e.node.RecallScore = node.RecallScore
			}
			// Both channels' raw scores survive the merge, each under its own name. They live on
			// different scales, so keeping the larger one (what RecallScore does) drops the vector
			// evidence every time BM25 also hit.
			if node.BM25Score > e.node.BM25Score {
				e.node.BM25Score = node.BM25Score
			}
			if node.KnnScore > e.node.KnnScore {
				e.node.KnnScore = node.KnnScore
			}
			e.score += weightOf(weights, o.name) / float64(k+rank+1)
		}
	}

	fused := make([]*interfaces.KnSearchNode, 0, len(order))
	// 2(k+1): When equal-weighted, bring "No. 1 on a certain road" back to 1.0, aligning with the old formula without weights.
	norm := 2 * float64(k+1)
	for _, key := range order {
		e := byKey[key]
		e.node.Score = e.score * norm
		fused = append(fused, e.node)
	}
	return fused
}

// mergeChannelNodes only merges overlaps and does not score. Source-store direct query paths have no _score:
// their ranks are meaningless, so scores are decided by the local fallback scorer.
func mergeChannelNodes(outcomes []channelOutcome) []*interfaces.KnSearchNode {
	seen := make(map[string]struct{})
	merged := make([]*interfaces.KnSearchNode, 0)
	for _, o := range outcomes {
		for rank, node := range o.nodes {
			key := instanceKey(node)
			if key == "" {
				key = fmt.Sprintf("%s|anon:%s:%d", node.ObjectTypeID, o.name, rank)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, node)
		}
	}
	return merged
}

// instanceIDProperties is the identity column carried by the index row, sorted by reliability.
//
// `unique_identities` / `instance_name` on this link are often empty - VM actual measurement (#818)
// The identity returned by the object type instance falls in the `_instance_id` of properties. If only the top-level fields are recognized,
// The same row recalled in both ways will be treated as two anonymous instances, each occupying one place.
var instanceIDProperties = []string{"_instance_id", "_instance_identity"}

// instanceKey generates an instance ID that is stable across channels. Press Unique Identification → Identity Column → Instance Name → Attribute Content.
// Give in one by one; only when no one can get it, an empty string is returned and handed over to the caller as an anonymous line for processing.
func instanceKey(node *interfaces.KnSearchNode) string {
	if node == nil {
		return ""
	}
	if len(node.UniqueIdentities) > 0 {
		keys := make([]string, 0, len(node.UniqueIdentities))
		for k := range node.UniqueIdentities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, node.UniqueIdentities[k]))
		}
		return node.ObjectTypeID + "|" + strings.Join(parts, "&")
	}
	for _, name := range instanceIDProperties {
		if v, ok := node.Properties[name]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return node.ObjectTypeID + "|" + name + "=" + s
			}
		}
	}
	if node.InstanceName != "" {
		return node.ObjectTypeID + "|name=" + node.InstanceName
	}
	// Fingerprints are taken based on attribute content: the fields obtained by the same row through two-way recall are exactly the same (two-way requests for the same group.
	// field), the fingerprints must be consistent. Two different instances with exactly the same properties will be merged, but such two lines will be.
	// The output is inherently indistinguishable, and merging is no worse than duplication. If the attribute is empty, there is no way to judge, leaving the anonymous branch.
	if len(node.Properties) > 0 {
		return node.ObjectTypeID + "|fingerprint=" + propertiesFingerprint(node.Properties)
	}
	return ""
}

// propertiesFingerprint Fingerprints properties independently of map traversal order.
func propertiesFingerprint(props map[string]any) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%v\x00", k, props[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sortNodesByScore is sorted in descending order by score, and at the same time, it is sorted in descending order by the original recall score, and then in ascending order by instance name——.
// When there is no bottom column, the order of the same branches will drift with map traversal, and the results of the same query will be inconsistent twice.
func sortNodesByScore(nodes []*interfaces.KnSearchNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Score != nodes[j].Score {
			return nodes[i].Score > nodes[j].Score
		}
		// Ties are the common case, not the exception: first place in one channel is exactly 1.0 for
		// every object type, so two rows from different object types tie all the time.
		//
		// The raw recall score may only break a tie between rows of the same object type, where it
		// came from the same index, the same query and the same operator. Across object types it is a
		// BM25 magnitude on one side and a cosine similarity on the other, and BM25 also drifts with
		// corpus and document length — ordering by it would dress an artefact up as relevance.
		if nodes[i].ObjectTypeID != nodes[j].ObjectTypeID {
			// Neither row outranks the other. The stable sort then keeps them in the order instance
			// recall produced, which follows concept recall's own ranking of the object types.
			return false
		}
		if nodes[i].RecallScore != nodes[j].RecallScore {
			return nodes[i].RecallScore > nodes[j].RecallScore
		}
		return nodes[i].InstanceName < nodes[j].InstanceName
	})
}

// convertToKnSearchNode converts raw data to KnSearchNode format.
func (s *localSearchImpl) convertToKnSearchNode(objType *interfaces.KnSearchObjectType, data map[string]any) *interfaces.KnSearchNode {
	node := &interfaces.KnSearchNode{
		ObjectTypeID:   objType.ConceptID,
		ObjectTypeName: objType.ConceptName,
		Properties:     make(map[string]any),
	}

	// Extract unique identifier.
	if uid, ok := data["unique_identities"]; ok {
		if uidMap, ok := uid.(map[string]any); ok {
			node.UniqueIdentities = uidMap
		}
	}

	// Extract instance name.
	if name, ok := data["instance_name"]; ok {
		if nameStr, ok := name.(string); ok {
			node.InstanceName = nameStr
		}
	}

	// Extract other attributes.
	for key, value := range data {
		if key != "unique_identities" && key != "instance_name" && key != "_score" {
			node.Properties[key] = value
		}
	}

	// Extract the score (if any)
	if score, ok := data["_score"]; ok {
		switch v := score.(type) {
		case float64:
			node.Score = v
		case int:
			node.Score = float64(v)
		case json.Number:
			// Instance bodies are decoded with UseNumber so wide integers survive;
			// see drivenadapters.precisionJSON.
			if parsed, err := v.Float64(); err == nil {
				node.Score = parsed
			}
		}
	}

	return node
}

// scoreNodes calculates the relevance scores of nodes.
// scoreNodes only takes the bottom when the bottom layer does not give a score.
//
// The rows returned by the index query have _score (ontology-query injects hit.Score row by row), which is OpenSearch's.
// Correlation, which is much more reliable than the string comparison here, is always retained. Resources returned to the source store for direct query do not have _score.
// Just use the following caveat: the hit range covers all fields participating in the search, not just the instance name - match is likely to hit.
// Fields such as description and address are only scored as 0 points compared to instance names and then filtered out by relevance.
func (s *localSearchImpl) scoreNodes(query string, nodes []*interfaces.KnSearchNode,
	searchable []searchableField, config *interfaces.KnSearchSemanticInstanceRetrievalConfig) {

	for _, node := range nodes {
		// Keep if there is already a score (from index retrieval)
		if node.Score > 0 {
			continue
		}

		if strings.TrimSpace(query) == "" {
			node.Score = 0
			node.HeuristicScore = 0
			continue
		}

		node.Score = fallbackNodeScore(query, node, searchable, config)
		// Also stamped on its own field: Score carries the fusion scale on the index-backed path and the
		// tier scale here, and without this marker a caller could not tell which of the two it is reading.
		node.HeuristicScore = node.Score
	}
}

// fallbackNodeScore takes the highest score in the instance name and each searchable field.
func fallbackNodeScore(query string, node *interfaces.KnSearchNode,
	searchable []searchableField, config *interfaces.KnSearchSemanticInstanceRetrievalConfig) float64 {

	best := textOverlapScore(query, node.InstanceName, config.ExactNameMatchScore)
	for i := range searchable {
		value, ok := node.Properties[searchable[i].Name]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok || text == "" {
			continue
		}
		// The hit on the attribute is not as precise as the instance name. If it is completely equal, only 0.6 will be given. Avoid using addresses and remarks.
		// The entire string of long text is raised to the same level as the instance name.
		if score := textOverlapScore(query, text, 0.6); score > best {
			best = score
		}
	}
	return best
}

// textOverlapScore is scored according to the three levels of "equal > target with query > query with target".
func textOverlapScore(query, target string, exactScore float64) float64 {
	if target == "" {
		return 0
	}
	switch {
	case target == query:
		return exactScore
	case containsFold(target, query):
		return 0.5
	case containsFold(query, target):
		return 0.3
	default:
		return 0
	}
}

// filterLowRelevanceNodes filters low-relevance nodes.
func (s *localSearchImpl) filterLowRelevanceNodes(nodes []*interfaces.KnSearchNode, minRelevance float64) []*interfaces.KnSearchNode {
	var filtered []*interfaces.KnSearchNode
	for _, node := range nodes {
		if node.Score >= minRelevance {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// filterNodesByScore filters nodes by score threshold.
func (s *localSearchImpl) filterNodesByScore(nodes []*interfaces.KnSearchNode, threshold float64) []*interfaces.KnSearchNode {
	var filtered []*interfaces.KnSearchNode
	for _, node := range nodes {
		if node.Score >= threshold {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// filterNodeProperties filter node properties.
func (s *localSearchImpl) filterNodeProperties(nodes []*interfaces.KnSearchNode, config *interfaces.KnSearchPropertyFilterConfig) []*interfaces.KnSearchNode {
	for _, node := range nodes {
		if len(node.Properties) > config.MaxPropertiesPerInstance {
			keys := make([]string, 0, len(node.Properties))
			for key := range node.Properties {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			newProps := make(map[string]any)
			for i, key := range keys {
				if i >= config.MaxPropertiesPerInstance {
					break
				}
				newProps[key] = node.Properties[key]
			}
			node.Properties = newProps
		}

		// Truncate overly long attribute values.
		for key, value := range node.Properties {
			if strVal, ok := value.(string); ok {
				if config.MaxPropertyValueLength > 0 {
					runes := []rune(strVal)
					if len(runes) > config.MaxPropertyValueLength {
						node.Properties[key] = string(runes[:config.MaxPropertyValueLength]) + "..."
					}
				}
			}
		}
	}
	return nodes
}

// knnAllowedFor determines whether a certain object type sends vector conditions this round.
//
// The only threshold is that it really has a vector field: without it, knn would not be able to be spelled out, and the cost would be wasted. As for "pick only the most relevant.
// "Several object types" - Concept recall does not score object types on the main path (Schema is taken from the knowledge network details, no.
// _score), the ranking is the natural order in the knowledge network rather than the correlation, intercepting according to it will only randomly.
// Vector capabilities are removed from some object types. Actual measurement: After filtering out object types without vector fields, the delay is limited to an unlimited number of objects.
// There is no difference, so this knob is not provided. The cost increases with the "number of object types for which vector indexes are built", so by building the index.
// people decide.
func knnAllowedFor(objType *interfaces.KnSearchObjectType, config *interfaces.KnSearchSemanticInstanceRetrievalConfig) bool {
	if config == nil || !boolValue(config.EnableKnnInstanceRetrieval) {
		return false
	}
	return hasKnnField(objType)
}

// hasKnnField determines whether there is an attribute on the object type that can send vector conditions.
func hasKnnField(objType *interfaces.KnSearchObjectType) bool {
	for _, f := range findSemanticSearchableFields(objType) {
		if f.HasKnn {
			return true
		}
	}
	return false
}
