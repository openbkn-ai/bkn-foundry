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
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

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
			Message: "没有可检索的对象类型",
		}, nil
	}

	instanceConfig := config.SemanticInstanceRetrieval
	propertyConfig := config.PropertyFilter

	s.backfillConditionOperations(ctx, req.KnID, objectTypes)

	var allNodes []*interfaces.KnSearchNode
	var maxScore float64

	// 遍历每个对象类型进行语义检索。rank 是概念召回给出的顺序，向量条件只发给靠前的几个。
	for rank, objType := range objectTypes {
		nodes, err := s.retrieveInstancesForObjectType(ctx, req, objType, instanceConfig, rank)
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

	// 属性过滤
	if boolValue(propertyConfig.EnablePropertyFilter) {
		allNodes = s.filterNodeProperties(allNodes, propertyConfig)
	}

	result := &interfaces.KnSearchSemanticInstanceResult{
		Nodes: allNodes,
	}

	if len(allNodes) == 0 {
		result.Message = "未检索到符合条件的实例数据"
	}

	return result, nil
}

// retrieveInstancesForObjectType 对单个对象类型进行语义检索
func (s *localSearchImpl) retrieveInstancesForObjectType(
	ctx context.Context,
	req *interfaces.KnSearchLocalRequest,
	objType *interfaces.KnSearchObjectType,
	config *interfaces.KnSearchSemanticInstanceRetrievalConfig,
	rank int,
) ([]*interfaces.KnSearchNode, error) {
	searchable := findSemanticSearchableFields(objType)
	if len(searchable) == 0 {
		s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Object type %s has no semantic-searchable properties, skip", objType.ConceptID)
		return nil, nil
	}

	cond := s.buildSemanticSearchConditionStruct(req.Query, searchable, config, knnAllowed(config, rank))
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

// knnAllowed 判断某个排名的对象类这一轮是否发向量条件。
//
// 概念召回已经按相关度排过序，尾部对象类基本进不了最终结果；为它们向量化查询词
// 是纯成本。默认只放行前几个，配置成 0 表示不限制。
func knnAllowed(config *interfaces.KnSearchSemanticInstanceRetrievalConfig, rank int) bool {
	if config == nil || !boolValue(config.EnableKnnInstanceRetrieval) {
		return false
	}
	if config.KnnObjectTypeLimit <= 0 {
		return true
	}
	return rank < config.KnnObjectTypeLimit
}
