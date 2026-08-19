// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

// Package knsearch (semantic instance recall·fine ranking)
// file: semantic_instance_rerank.go
package knsearch

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Precision-grade third gear.
const (
	// InstanceRerankModeOff does not call the model and returns the first-level fusion order.
	InstanceRerankModeOff = "off"
	// InstanceRerankModeShadow returns the fusion sequence as usual, but adjusts the model one more time and records the difference between the two sequences.
	// Use it to collect evidence before changing the default: is the change worth the 100~400ms?.
	InstanceRerankModeShadow = "shadow"
	// InstanceRerankModeOn uses model points to override sorting.
	InstanceRerankModeOn = "on"
)

// Enter the internal field prefix for finely typed text. _score / _instance_id / _display This type comes with the link.
// Metadata, fed to the cross-encoder only dilutes the real business text.
const internalPropertyPrefix = "_"

// rerankDocumentCharLimit The maximum total length of a single document.
//
// The adaptation layer of mf-model-api silently cuts a single document to 4000 characters (external_small_model_utils.py).
// Leave a margin to avoid stepping on the boundary - the truncated tail fields are equal to not participating in scoring, and no error will be reported.
const rerankDocumentCharLimit = 3600

// normalizeRerankMode Normalize mode string; if it cannot be recognized, it returns an empty string and leaves it to the caller to decide how to fall back.
func normalizeRerankMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case InstanceRerankModeOff:
		return InstanceRerankModeOff
	case InstanceRerankModeShadow:
		return InstanceRerankModeShadow
	case InstanceRerankModeOn:
		return InstanceRerankModeOn
	default:
		return ""
	}
}

// rerankInstances uses cross-encoder to refine the fused candidates.
//
// Position is important: must precede attribute filtering. filterNodeProperties will cut the number of properties and truncate the value.
// Sending truncated text to the model would judge relevance from incomplete text.
//
// If there is a problem at any step, the input order will be returned - the downgrade path cannot empty the result, nor can it insert irrelevant items (#788).
func (s *localSearchImpl) rerankInstances(
	ctx context.Context,
	query string,
	nodes []*interfaces.KnSearchNode,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
) []*interfaces.KnSearchNode {
	mode := normalizeRerankMode(config.InstanceRerankMode)
	if mode == "" {
		if raw := strings.TrimSpace(config.InstanceRerankMode); raw != "" {
			s.logger.WithContext(ctx).Warnf("[InstanceRerank] Unknown mode %q, falling back to off", raw)
		}
		return nodes
	}
	if mode == InstanceRerankModeOff {
		return nodes
	}
	if strings.TrimSpace(query) == "" || len(nodes) < 2 {
		return nodes
	}
	if s.rerankClient == nil {
		s.logger.WithContext(ctx).Warnf("[InstanceRerank] No rerank client configured, keeping fusion order")
		return nodes
	}

	topN := config.RerankTopN
	if topN <= 0 {
		topN = 50
	}
	head := nodes
	var tail []*interfaces.KnSearchNode
	if len(nodes) > topN {
		head, tail = nodes[:topN], nodes[topN:]
	}

	documents := make([]string, 0, len(head))
	for _, node := range head {
		documents = append(documents, instanceRerankDocument(node, config.RerankFieldCharLimit))
	}

	resp, err := s.rerankClient.Rerank(ctx, query, documents, config.InstanceRerankModel)
	if err != nil || resp == nil {
		// The reranker is not registered (NameNotExist), which is normal in the customer environment and is not an abnormal path.
		s.logger.WithContext(ctx).Warnf("[InstanceRerank] Rerank unavailable (%v), keeping fusion order", err)
		return nodes
	}

	applied := 0
	for _, result := range resp.Results {
		// Must be backfilled by index: The behavior of manufacturers is inconsistent, some return in descending order of scores, and some return in original order.
		if result.Index < 0 || result.Index >= len(head) {
			continue
		}
		head[result.Index].RerankerScore = result.RelevanceScore
		applied++
	}
	if applied == 0 {
		s.logger.WithContext(ctx).Warnf("[InstanceRerank] Rerank returned no usable index, keeping fusion order")
		return nodes
	}

	fusionOrder := append([]*interfaces.KnSearchNode(nil), head...)
	reranked := append([]*interfaces.KnSearchNode(nil), head...)
	sort.SliceStable(reranked, func(i, j int) bool {
		if reranked[i].RerankerScore != reranked[j].RerankerScore {
			return reranked[i].RerankerScore > reranked[j].RerankerScore
		}
		return reranked[i].Score > reranked[j].Score
	})

	spearman, movedInTop5 := orderDelta(fusionOrder, reranked, 5)
	s.logger.WithContext(ctx).Infof(
		"[InstanceRerank] mode=%s candidates=%d scored=%d spearman=%.4f top5_changed=%d",
		mode, len(head), applied, spearman, movedInTop5)

	if mode == InstanceRerankModeShadow {
		// Only obtain evidence, do not change the order: the rerank score has been written into the node, and the caller can compare it, and the sorting is still the fusion order.
		return nodes
	}

	// The on mode only changes the order and does not overwrite the Score.
	//
	// Coverage is wrong: Candidates that are not backfilled by the model (which will happen if the manufacturer returns one less) will be assigned 0, and those beyond top_n will be assigned a value of 0.
	// The tail has never entered the model, so two rulers, fusion score and model score, appear simultaneously in one response - press score.
	// The caller of secondary sorting will get the wrong order, and when Score contains omitempty, 0 points will make the entire field disappear.
	//
	// Fusion points are a single dimension earned by PR1 that is comparable across object types and should not be destroyed by refinement. What Jingpai wants to express is.
	// "Who should be ranked first" is reflected in the return order; the rerank_score is brought out separately as to how many points the model has judged.
	return append(reranked, tail...)
}

// instanceRerankDocument converts an instance into text for reranking.
//
// Fields are sorted by name to ensure that the text spelled out in the same line is consistent each time - otherwise the map traversal order will make the same.
// query got different model scores twice.
func instanceRerankDocument(node *interfaces.KnSearchNode, fieldCharLimit int) string {
	if node == nil {
		return ""
	}
	if fieldCharLimit <= 0 {
		fieldCharLimit = 200
	}

	var b strings.Builder
	appendField := func(name, value string) bool {
		value = strings.TrimSpace(value)
		if value == "" {
			return true
		}
		value = truncateRunes(value, fieldCharLimit)
		line := value
		if name != "" {
			line = name + ": " + value
		}
		if b.Len()+len(line)+1 > rerankDocumentCharLimit {
			return false
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		return true
	}

	if !appendField("", node.InstanceName) {
		return b.String()
	}

	keys := make([]string, 0, len(node.Properties))
	for k := range node.Properties {
		// Internal metadata (_score / _instance_id / _display …) does not enter the text.
		if strings.HasPrefix(k, internalPropertyPrefix) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		text, ok := node.Properties[k].(string)
		if !ok {
			continue
		}
		if !appendField(k, text) {
			break
		}
	}
	return b.String()
}

// truncateRunes truncate by characters rather than bytes to avoid splitting a Chinese character in half.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// orderDelta compares two sequences: the Spearman rank correlation coefficient, and the number of changes in the top K names.
//
// These two numbers are the total output of the shadow mode: the correlation coefficient explains the overall rearrangement extent, and the top-K changes explain.
// The difference that the caller can really perceive - Agent usually only looks at the first few items.
func orderDelta(before, after []*interfaces.KnSearchNode, k int) (float64, int) {
	n := len(before)
	if n < 2 || len(after) != n {
		return 1, 0
	}

	rankBefore := make(map[*interfaces.KnSearchNode]int, n)
	for i, node := range before {
		rankBefore[node] = i
	}

	var sumSquaredDiff float64
	for i, node := range after {
		d := float64(i - rankBefore[node])
		sumSquaredDiff += d * d
	}
	spearman := 1 - (6*sumSquaredDiff)/float64(n*(n*n-1))
	if math.IsNaN(spearman) || math.IsInf(spearman, 0) {
		spearman = 0
	}

	if k > n {
		k = n
	}
	head := make(map[*interfaces.KnSearchNode]struct{}, k)
	for _, node := range before[:k] {
		head[node] = struct{}{}
	}
	changed := 0
	for _, node := range after[:k] {
		if _, ok := head[node]; !ok {
			changed++
		}
	}
	return spearman, changed
}
