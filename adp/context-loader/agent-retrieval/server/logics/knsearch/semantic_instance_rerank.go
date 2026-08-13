// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

// Package knsearch（语义实例召回·精排级）
// file: semantic_instance_rerank.go
package knsearch

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// 精排级的三档。
const (
	// InstanceRerankModeOff 不调用模型，返回第一级的融合序。
	InstanceRerankModeOff = "off"
	// InstanceRerankModeShadow 照常返回融合序，但额外调一次模型并记录两个序列的差异。
	// 翻默认之前用它取证：改动到底值不值那 100~400ms。
	InstanceRerankModeShadow = "shadow"
	// InstanceRerankModeOn 用模型分覆盖排序。
	InstanceRerankModeOn = "on"
)

// 进入精排文本的内部字段前缀。_score / _instance_id / _display 这类是链路自带的
// 元数据，喂给 cross-encoder 只会稀释真正的业务文本。
const internalPropertyPrefix = "_"

// rerankDocumentCharLimit 单个文档的总长上限。
//
// mf-model-api 的适配层把单文档静默截到 4000 字符（external_small_model_utils.py），
// 留出余量避免踩在边界上——被截掉的尾部字段等于没参与打分，而且不报错。
const rerankDocumentCharLimit = 3600

// normalizeRerankMode 归一化模式字符串；无法识别时返回空串，交由调用方决定回落。
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

// rerankInstances 用 cross-encoder 对融合后的候选做精排。
//
// 位置很重要：必须在属性过滤之前。filterNodeProperties 会砍属性数并截断值，
// 截完再送模型就是拿残文本判相关性。
//
// 任何一步出问题都退回入参顺序——降级路径不能把结果打空，也不能塞进无关项（#788）。
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
		// reranker 未注册（NameNotExist）在客户环境是常态，不是异常路径。
		s.logger.WithContext(ctx).Warnf("[InstanceRerank] Rerank unavailable (%v), keeping fusion order", err)
		return nodes
	}

	applied := 0
	for _, result := range resp.Results {
		// 必须按 index 回填：厂商行为不一致，有的按分数降序返、有的按原序返。
		if result.Index < 0 || result.Index >= len(head) {
			continue
		}
		head[result.Index].RerankScore = result.RelevanceScore
		applied++
	}
	if applied == 0 {
		s.logger.WithContext(ctx).Warnf("[InstanceRerank] Rerank returned no usable index, keeping fusion order")
		return nodes
	}

	fusionOrder := append([]*interfaces.KnSearchNode(nil), head...)
	reranked := append([]*interfaces.KnSearchNode(nil), head...)
	sort.SliceStable(reranked, func(i, j int) bool {
		if reranked[i].RerankScore != reranked[j].RerankScore {
			return reranked[i].RerankScore > reranked[j].RerankScore
		}
		return reranked[i].Score > reranked[j].Score
	})

	spearman, movedInTop5 := orderDelta(fusionOrder, reranked, 5)
	s.logger.WithContext(ctx).Infof(
		"[InstanceRerank] mode=%s candidates=%d scored=%d spearman=%.4f top5_changed=%d",
		mode, len(head), applied, spearman, movedInTop5)

	if mode == InstanceRerankModeShadow {
		// 只取证，不改序：rerank 分已经写进节点，调用方能对比，排序仍是融合序。
		return nodes
	}

	for _, node := range reranked {
		node.Score = node.RerankScore
	}
	return append(reranked, tail...)
}

// instanceRerankDocument 把一条实例拼成送 rerank 的文本。
//
// 字段按名字排序保证同一行每次拼出的文本一致——否则 map 遍历顺序会让同一个
// query 两次拿到不同的模型分。
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
		// 内部元数据（_score / _instance_id / _display …）不进文本。
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

// truncateRunes 按字符而不是字节截断，避免把一个中文字劈成两半。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// orderDelta 比较两个序列：Spearman 等级相关系数，以及前 K 名里换了几个。
//
// 这两个数是 shadow 模式的全部产出：相关系数说明整体重排幅度，top-K 变动说明
// 调用方真正能感知到的差异——Agent 通常只看前几条。
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
