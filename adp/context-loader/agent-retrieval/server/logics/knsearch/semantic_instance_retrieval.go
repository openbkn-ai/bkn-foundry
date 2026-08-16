// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch（语义实例召回）
// file: semantic_instance_retrieval.go
package knsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// 召回通道名，进日志用于分辨是哪一路出的问题。
const (
	channelKnn   = "knn"
	channelMatch = "match"
)

// retrievalChannel 一路召回通道：一条独立发出的查询。
type retrievalChannel struct {
	name string
	cond *interfaces.KnCondition
}

// channelOutcome 单通道的召回结果。scored 记录这一路的行是否带 _score——
// 没有的话（回落源库直查）响应顺序不代表相关性，名次无意义，不能拿去做 RRF。
type channelOutcome struct {
	name   string
	nodes  []*interfaces.KnSearchNode
	scored bool
	err    error
}

// semanticInstanceRetrieval 语义实例召回主逻辑
// 流程：遍历对象类型 -> 向量检索 -> 打分与排序 -> 全局分数过滤 -> 属性过滤
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

	var allNodes []*interfaces.KnSearchNode
	var maxScore float64

	for _, objType := range objectTypes {
		nodes, err := s.retrieveInstancesForObjectType(ctx, req, objType, instanceConfig, knnAllowedFor(objType, instanceConfig))
		if err != nil {
			s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Failed to retrieve instances for %s: %v",
				objType.ConceptID, err)
			continue
		}

		// 更新最高分
		for _, node := range nodes {
			if node.Score > maxScore {
				maxScore = node.Score
			}
		}

		allNodes = append(allNodes, nodes...)
	}

	s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Retrieved %d instances from %d object types, max_score=%.4f",
		len(allNodes), len(objectTypes), maxScore)

	// 全局分数过滤
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

	// 精排级（默认 off）。放在属性过滤**之前**：filterNodeProperties 会砍属性数并截断值，
	// 截完再送模型就是拿残文本判相关性。
	allNodes = s.rerankInstances(ctx, req.Query, allNodes, instanceConfig)

	// 属性过滤
	if boolValue(propertyConfig.EnablePropertyFilter) {
		allNodes = s.filterNodeProperties(allNodes, propertyConfig)
	}

	result := &interfaces.KnSearchSemanticInstanceResult{
		Nodes: allNodes,
	}

	if len(allNodes) == 0 {
		result.Message = infraErr.LocalizedDetail(ctx, "NoMatchingInstances")
	}

	return result, nil
}

// retrieveInstancesForObjectType 对单个对象类型进行语义检索。
//
// 默认走两通道：knn 与 match 各发一条查询，再按名次做 RRF 融合。拆开发是必需的——
// 合在一条 OR 里时 OpenSearch 把两路子句分直接相加，knn 分落在 0~1 而 BM25 无上界，
// 向量命中会被挤出 InitialCandidateCount 条候选，排序阶段再怎么修都救不回来。
// 而拆出来的子句分拿不到：named queries 只回答命中与否，取子句分只能走 explain。
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

// retrieveInstancesFused 双通道召回 + RRF 融合。
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
			// 单通道失败不打掉整个对象类。knn 打在没有向量映射的字段上时下游回 400
			// （condition_operations 由建网方声明并原样落库，不可信），过去这个 400
			// 会连带 match 一起失败，该对象类一条实例都召不回。
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
		// 源库直查路径没有 _score，响应顺序是库的自然序，名次无意义。
		// 退回本地兜底打分——0/0.3/0.5/0.85 那套分档本来就是为这条路设计的。
		nodes = mergeChannelNodes(live)
		s.scoreNodes(req.Query, nodes, searchable, config)
	}

	sortNodesByScore(nodes)

	if config.PerTypeInstanceLimit > 0 && len(nodes) > config.PerTypeInstanceLimit {
		nodes = nodes[:config.PerTypeInstanceLimit]
	}

	// MinDirectRelevance 是绝对阈值，只对本地兜底打分有意义。打在 RRF 分上会把
	// 全部结果滤掉（RRF 分量级在 0.0x），打在原始 _score 上则是另一种坏：BM25 行
	// 恒大于阈值全部放过、纯向量行卡在边缘随机掉。
	if !anyScored {
		nodes = s.filterLowRelevanceNodes(nodes, config.MinDirectRelevance)
	}

	return nodes, nil
}

// fetchChannel 发出一路查询并转成节点，同时记录这一路是否带 _score。
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
		out.nodes = append(out.nodes, node)
	}

	// 相对分数过滤放在通道内、融合之前——这是原始分唯一可比的地方：同一对象类、
	// 同一索引、同一查询、同一种算子。融合之后就没法做了：RRF 分只表达名次，
	// 第一名恒为 1.0，哪怕它其实毫不相关，"整体都不相关"这个信息在名次里表达不出来。
	if out.scored && boolValue(config.EnableGlobalFinalScoreRatioFilter) && config.GlobalFinalScoreRatio > 0 {
		out.nodes = pruneChannelByScoreRatio(out.nodes, config.GlobalFinalScoreRatio)
	}
	return out
}

// pruneChannelByScoreRatio 丢掉与本通道最高分差距过大的行，最高分那条始终保留。
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

// retrieveInstancesSingleQuery 旧路径：单条 OR 查询。仅 enable_rrf_fusion=false 时走，
// 作为新融合逻辑出问题时的逃生门。
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

	// 调用 ontology-query 进行实例检索
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

	// 转换为 KnSearchNode 格式
	nodes := make([]*interfaces.KnSearchNode, 0, len(resp.Data))
	for _, data := range resp.Data {
		if dataMap, ok := data.(map[string]any); ok {
			node := s.convertToKnSearchNode(objType, dataMap)
			node.RecallScore = node.Score
			nodes = append(nodes, node)
		}
	}

	// 计算相关性分数
	s.scoreNodes(req.Query, nodes, searchable, config)

	// 按分数降序排序
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Score > nodes[j].Score
	})

	// 取 Top-K
	if len(nodes) > config.PerTypeInstanceLimit {
		nodes = nodes[:config.PerTypeInstanceLimit]
	}

	// 过滤低相关性节点
	nodes = s.filterLowRelevanceNodes(nodes, config.MinDirectRelevance)

	return nodes, nil
}

// buildSemanticSearchConditionStruct 构建语义检索条件结构体
// buildSemanticSearchConditionStruct 把一句自然语言拼成 OR 条件。
//
// 只用 knn 与 match：knn 吃整句（句向量本就该整句进），match 吃整句后由分析器分词
// 逐词命中。等值不参与——拿整句去和某个字段做 == 永远为假，还要白占一个
// max_sub_conditions 名额，把真正能命中的子条件挤掉。
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

	// 字段只支持等值时一个子条件都拼不出来。空的 OR 条件 ontology-query 会直接判 400
	// （"sub condition size is 0"），所以这里返回 nil 让调用方跳过该对象类。
	if len(subConditions) == 0 {
		return nil
	}

	return &interfaces.KnCondition{
		Operation:     interfaces.KnOperationTypeOr,
		SubConditions: subConditions,
	}
}

// buildKnnOnlyCondition 只含向量子条件的通道。对象类没有向量字段、或本轮不允许发
// 向量条件时返回 nil，调用方据此跳过这一路。
//
// k 沿用 PerTypeInstanceLimit 而不是 InitialCandidateCount：向量检索的成本随 k 增长，
// 而这一路的作用是「保底名额」——把最相关的几条送进融合池，够用即可。调大 k 的收益
// 需要召回率实验支撑，属于 #708 的范围。
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

// buildMatchOnlyCondition 只含全文子条件的通道。等值不参与：拿整句去和某个字段做 ==
// 永远为假，还要白占一个 max_sub_conditions 名额。
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

// rrfK 取融合常数，非正值回落默认 60。
func rrfK(config *interfaces.KnSearchSemanticInstanceRetrievalConfig) int {
	if config != nil && config.RRFK > 0 {
		return config.RRFK
	}
	return 60
}

// channelWeights 解析各通道权重。knn 取 knn_weight，match 取 1-knn_weight，
// 其余通道（将来若有）按等权处理。
//
// 越界值钳制到 [0,1] 而不是报错：配错一个数不该让整条检索链失败，何况这个旋钮
// 目前没有召回率实验支撑（同 #708），更不该有硬性失败路径。
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

// weightOf 取某通道的权重；未登记的通道按等权 0.5 处理。
func weightOf(weights map[string]float64, channel string) float64 {
	if v, ok := weights[channel]; ok {
		return v
	}
	return 0.5
}

// fuseByRRF 按 Reciprocal Rank Fusion 融合多路召回：
//
//	score = Σ w_i/(k + rank_i) × 2(k+1)
//
// 用名次而不是分数，是因为两路的分根本不同量纲：knn 分在 0~1，BM25 无上界且跨索引
// 不可比。归一化加权和也能凑合，但 min-max 在候选少、分数集中时噪声很大（top1 恒为
// 1.0），且权重要按知识网络手调；RRF 只有一个常数，跨网不用调。
//
// 乘 2(k+1) 只是换量纲：**等权（默认 0.5）时**任意一路的第 1 名恰好得 1.0，两路都
// 第 1 得 2.0——与不带权重的旧式 Σ1/(k+rank)×(k+1) 逐位相同。这样跨对象
// 类比较有一个稳定的锚——「在自己能发的通道里排第 1」在哪个对象类都是 1.0，不受
// 该类发了几路影响；两路都命中的实例高出一截，那是真信号。同时量级与无 _score
// 路径的本地兜底打分（0~0.85）相当，两条路径的结果汇进同一个池子做全局比例过滤时
// 不会互相抹掉。
//
// 不要再除以通道数：那样做会把「双通道对象类里只被一路命中的实例」压到单通道
// 对象类同名次实例的一半（VM 实测 0.5 vs 1.0），偏置只是换了个方向。缺席另一路
// 本身已经通过「少加一项」体现了，不需要再罚一次。
//
// 权重偏离 0.5 之后，上面那个跨类锚点会跟着倾斜：调高向量权重，没有向量字段的
// 对象类整体被压低。那是调用方声明的偏好带来的结果，不是缺陷——但也正因如此，
// 默认值必须留在 0.5。
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
				// 既无唯一标识又无实例名：无法安全判定与其他行是否同一实例。
				// 宁可重复也不误合并两个不同实例，给它一个不会碰撞的键。
				key = fmt.Sprintf("%s|anon:%s:%d", node.ObjectTypeID, o.name, rank)
			}
			e, ok := byKey[key]
			if !ok {
				e = &entry{node: node}
				byKey[key] = e
				order = append(order, key)
			} else if node.RecallScore > e.node.RecallScore {
				// 同一实例两路都召回：属性内容一致（两路请求同一组字段），
				// 只把原始召回分取较大者留作观测。
				e.node.RecallScore = node.RecallScore
			}
			e.score += weightOf(weights, o.name) / float64(k+rank+1)
		}
	}

	fused := make([]*interfaces.KnSearchNode, 0, len(order))
	// 2(k+1)：等权时把「某一路第 1 名」拉回 1.0，与不带权重的老公式对齐。
	norm := 2 * float64(k+1)
	for _, key := range order {
		e := byKey[key]
		e.node.Score = e.score * norm
		fused = append(fused, e.node)
	}
	return fused
}

// mergeChannelNodes 只做去重合并、不打分。用于无 _score 的源库直查路径：
// 那条路上名次无意义，分数交给本地兜底打分算。
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

// instanceIDProperties 是索引行携带的身份列，按可靠性排序。
//
// 这条链路上 `unique_identities` / `instance_name` 经常都是空的——VM 实测（#818）
// 对象类实例返回的身份落在 properties 的 `_instance_id` 里。只认顶层字段的话，
// 两路召回的同一行会被当成两个匿名实例各占一个名额。
var instanceIDProperties = []string{"_instance_id", "_instance_identity"}

// instanceKey 生成跨通道稳定的实例标识。按 唯一标识 → 身份列 → 实例名 → 属性内容
// 依次退让；全都拿不到才返回空串交由调用方当匿名行处理。
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
	// 兜底按属性内容取指纹：同一行经两路召回拿到的字段完全相同（两路请求同一组
	// 字段），指纹必然一致。两个属性完全相同的不同实例会被合并，但那样的两行在
	// 输出里本就无法区分，合并不比重复更糟。属性为空则无从判断，留给匿名分支。
	if len(node.Properties) > 0 {
		return node.ObjectTypeID + "|fingerprint=" + propertiesFingerprint(node.Properties)
	}
	return ""
}

// propertiesFingerprint 对属性做与 map 遍历顺序无关的指纹。
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

// sortNodesByScore 按分数降序，同分时按原始召回分降序、再按实例名升序——
// 没有兜底列时同分行的次序会随 map 遍历漂移，同一查询两次结果不一致。
func sortNodesByScore(nodes []*interfaces.KnSearchNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Score != nodes[j].Score {
			return nodes[i].Score > nodes[j].Score
		}
		if nodes[i].RecallScore != nodes[j].RecallScore {
			return nodes[i].RecallScore > nodes[j].RecallScore
		}
		return nodes[i].InstanceName < nodes[j].InstanceName
	})
}

// convertToKnSearchNode 将原始数据转换为 KnSearchNode 格式
func (s *localSearchImpl) convertToKnSearchNode(objType *interfaces.KnSearchObjectType, data map[string]any) *interfaces.KnSearchNode {
	node := &interfaces.KnSearchNode{
		ObjectTypeID:   objType.ConceptID,
		ObjectTypeName: objType.ConceptName,
		Properties:     make(map[string]any),
	}

	// 提取唯一标识
	if uid, ok := data["unique_identities"]; ok {
		if uidMap, ok := uid.(map[string]any); ok {
			node.UniqueIdentities = uidMap
		}
	}

	// 提取实例名称
	if name, ok := data["instance_name"]; ok {
		if nameStr, ok := name.(string); ok {
			node.InstanceName = nameStr
		}
	}

	// 提取其他属性
	for key, value := range data {
		if key != "unique_identities" && key != "instance_name" && key != "_score" {
			node.Properties[key] = value
		}
	}

	// 提取分数（如果有）
	if score, ok := data["_score"]; ok {
		switch v := score.(type) {
		case float64:
			node.Score = v
		case int:
			node.Score = float64(v)
		}
	}

	return node
}

// scoreNodes 计算节点的相关性分数
// scoreNodes 只在底层没给出分数时兜底。
//
// 索引查询回来的行带 _score（ontology-query 逐行注入 hit.Score），那是 OpenSearch 的
// 相关性，比这里的字符串比对可靠得多，一律保留。回落到源库直查的资源没有 _score，
// 才用下面的兜底：命中范围覆盖参与检索的全部字段而不只是实例名——match 很可能命中
// 的是描述、地址一类字段，只比实例名会把它们判成 0 分再被相关性过滤掉。
func (s *localSearchImpl) scoreNodes(query string, nodes []*interfaces.KnSearchNode,
	searchable []searchableField, config *interfaces.KnSearchSemanticInstanceRetrievalConfig) {

	for _, node := range nodes {
		// 已有分数（来自索引检索）时保留
		if node.Score > 0 {
			continue
		}

		if strings.TrimSpace(query) == "" {
			node.Score = 0
			continue
		}

		node.Score = fallbackNodeScore(query, node, searchable, config)
	}
}

// fallbackNodeScore 取实例名与各可检索字段里的最高分。
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
		// 属性上的命中不如实例名精确，完全相等也只给 0.6，避免把地址、备注一类
		// 长文本的整串相等抬到与实例名同级。
		if score := textOverlapScore(query, text, 0.6); score > best {
			best = score
		}
	}
	return best
}

// textOverlapScore 按「相等 > 目标含查询 > 查询含目标」三档给分。
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

// filterLowRelevanceNodes 过滤低相关性节点
func (s *localSearchImpl) filterLowRelevanceNodes(nodes []*interfaces.KnSearchNode, minRelevance float64) []*interfaces.KnSearchNode {
	var filtered []*interfaces.KnSearchNode
	for _, node := range nodes {
		if node.Score >= minRelevance {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// filterNodesByScore 按分数阈值过滤节点
func (s *localSearchImpl) filterNodesByScore(nodes []*interfaces.KnSearchNode, threshold float64) []*interfaces.KnSearchNode {
	var filtered []*interfaces.KnSearchNode
	for _, node := range nodes {
		if node.Score >= threshold {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// filterNodeProperties 过滤节点属性
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

		// 截断过长的属性值
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

// knnAllowedFor 判断某个对象类这一轮是否发向量条件。
//
// 唯一的门槛是它真有向量字段：没有的话本来也拼不出 knn，白占成本。至于「只挑最相关
// 的几个对象类」——概念召回在主路径上不给对象类打分（Schema 取自知识网络详情，没有
// _score），排出来的名次是知识网络里的自然顺序而不是相关度，按它截取只会随机地把
// 向量能力从某些对象类上拿掉。实测在筛掉无向量字段的对象类之后，限不限个数的延迟
// 没有差别，因此不设这个旋钮。成本随「建了向量索引的对象类数量」增长，那由建索引
// 的人决定。
func knnAllowedFor(objType *interfaces.KnSearchObjectType, config *interfaces.KnSearchSemanticInstanceRetrievalConfig) bool {
	if config == nil || !boolValue(config.EnableKnnInstanceRetrieval) {
		return false
	}
	return hasKnnField(objType)
}

// hasKnnField 判断对象类上有没有可以发向量条件的属性。
func hasKnnField(objType *interfaces.KnSearchObjectType) bool {
	for _, f := range findSemanticSearchableFields(objType) {
		if f.HasKnn {
			return true
		}
	}
	return false
}
