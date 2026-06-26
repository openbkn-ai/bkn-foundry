// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knretrieval

import (
	"context"

	o11y "github.com/kweaver-ai/kweaver-go-lib/observability"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// AgentIntentRetrieval 语义检索。
// 原依赖「概念意图分析智能体」+「概念召回策略智能体」做意图粗识别与召回规划；
// 二者随 decision-agent(agent-factory) 退役后，本路径降级为基于 Query 的关键词召回（longtail），
// 再经业务知识网络执行查询策略并重排。需要完整 Schema 召回的接入方应改用 search_schema。
func (k *knRetrievalServiceImpl) AgentIntentRetrieval(ctx context.Context, req *interfaces.SemanticSearchRequest) (resp *interfaces.SemanticSearchResponse, err error) {
	// 记录可观测
	ctx, _ = o11y.StartInternalSpan(ctx)
	defer o11y.EndSpan(ctx, err)

	queryUnderstanding := &interfaces.QueryUnderstanding{}
	// 基于用户 Query 构建关键词查询策略
	queryStrategys := k.longtailRecallByKnowledgeNetwork(req.Query)
	// 筛选查询策略
	queryStrategys = k.filterQueryStrategysBySearchScope(queryStrategys, req.SearchScope)

	// 概念结果候选集
	conceptResults := []*interfaces.ConceptResult{}
	if len(queryStrategys) > 0 {
		// 并发执行查询策略
		var queryConceptResults []*interfaces.ConceptResult
		queryConceptResults, err = k.parallelExecSemanticQueryStrategy(ctx, req.KnID, queryStrategys)
		if err != nil {
			k.logger.WithContext(ctx).Warnf("[SemanticSearchV2] parallelExecSemanticQueryStrategy failed. knId:%s, queryStrategys:%v, err:%v", req.KnID, queryStrategys, err)
			return
		}
		if len(queryConceptResults) > 0 {
			conceptResults = append(conceptResults, queryConceptResults...)
		}
		// 返回执行的策略
		queryUnderstanding.QueryStrategys = queryStrategys
	}
	// 排序：按概念类型排序, 去重
	rerankConceptResults, err := k.rerankConcepts(ctx, queryUnderstanding, conceptResults, req.RerankAction, req.MaxConcepts, req.RerankLLMModel, req.RerankVectorModel)
	if err != nil {
		return
	}
	// 组装结果
	resp = &interfaces.SemanticSearchResponse{
		QueryUnderstanding: queryUnderstanding,
		KnowledgeConcepts:  rerankConceptResults,
		HitsTotal:          len(conceptResults),
	}
	return
}

// 长尾召回策略:基于业务知识网络做关键词匹配 -- 构建查询策略
func (k *knRetrievalServiceImpl) longtailRecallByKnowledgeNetwork(query string) (queryStrategys []*interfaces.SemanticQueryStrategy) {
	// 根据用户数据的原始Query生成查询策略
	var empty []*interfaces.QueryStrategyCondition
	objectTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeObject, query, empty)
	if objectTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, objectTypeDiscoveryStrategy)
	}
	releationTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeRelation, query, empty)
	if releationTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, releationTypeDiscoveryStrategy)
	}
	actionTypeDiscoveryStrategy := k.buildConceptDiscoveryStrategy(interfaces.KnConceptTypeAction, query, empty)
	if actionTypeDiscoveryStrategy != nil {
		queryStrategys = append(queryStrategys, actionTypeDiscoveryStrategy)
	}
	return
}
