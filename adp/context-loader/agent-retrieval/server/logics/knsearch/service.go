// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (本地检索主服务实现)
// file: service.go
package knsearch

import (
	"context"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Search 知识网络检索本地主入口
func (s *localSearchImpl) Search(ctx context.Context, req *interfaces.KnSearchLocalRequest) (*interfaces.KnSearchLocalResponse, error) {
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Start Search, kn_id=%s, query=%s, only_schema=%v",
		req.KnID, req.Query, req.OnlySchema)

	// 1. 合并配置
	mergedConfig := MergeRetrievalConfig(req.RetrievalConfig)
	s.logger.WithContext(ctx).Debugf("[KnSearchLocal] Merged config: concept_top_k=%d, schema_brief=%v, enable_coarse_recall=%v",
		mergedConfig.ConceptRetrieval.TopK,
		boolValue(mergedConfig.ConceptRetrieval.SchemaBrief),
		boolValue(mergedConfig.ConceptRetrieval.EnableCoarseRecall))

	// 2. 概念召回（Schema Recall）
	conceptResult, err := s.conceptRetrieval(ctx, req, mergedConfig.ConceptRetrieval)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnSearchLocal] Concept retrieval failed: %v", err)
		return nil, err
	}

	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Concept retrieval completed: object_types=%d, relation_types=%d, action_types=%d",
		len(conceptResult.ObjectTypes), len(conceptResult.RelationTypes), len(conceptResult.ActionTypes))

	// 概念召回默认走知识网络导出视图，那条路不做数据源富化，属性上的
	// condition_operations 一律为空。调用方（尤其是 Agent）正是靠它判断字段能不能做
	// match / knn，拿到空的就只能靠猜——能力再准，取不到等于没有。在这里补齐，
	// Schema 响应与后续实例召回共用同一份结果。
	s.backfillConditionOperations(ctx, req.KnID, conceptResult.ObjectTypes)

	// 3. 构建响应
	response := &interfaces.KnSearchLocalResponse{
		ObjectTypes:   conceptResult.ObjectTypes,
		RelationTypes: conceptResult.RelationTypes,
		ActionTypes:   conceptResult.ActionTypes,
	}

	// 4. 语义实例召回：只在调用方明确要实例时做（search_schema 恒为 schema-only）
	if req.OnlySchema {
		s.logger.WithContext(ctx).Infof("[KnSearchLocal] only_schema=true, skip semantic instance retrieval")
		return response, nil
	}

	instanceResult, instanceErr := s.semanticInstanceRetrieval(ctx, req, conceptResult.ObjectTypes, mergedConfig)
	if instanceErr != nil {
		// 实例召回失败不拖垮整条检索：Schema 本身已经是有用的结果，降级返回。
		s.logger.WithContext(ctx).Warnf("[KnSearchLocal] Semantic instance retrieval failed, degrade to schema-only: %v", instanceErr)
		return response, nil
	}

	response.Nodes = instanceResult.Nodes
	response.Message = instanceResult.Message
	s.logger.WithContext(ctx).Infof("[KnSearchLocal] Semantic instance retrieval completed: nodes=%d", len(response.Nodes))

	return response, nil
}
