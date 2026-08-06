// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch（属性算子补齐）
// file: condition_operations_backfill.go
package knsearch

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// backfillConditionOperations 为待检索的对象类补齐属性算子。
//
// 概念召回拿到的 Schema 来自知识网络导出视图（GET /knowledge-networks/{kn}?mode=export），
// 那条路只列对象类，不做数据源富化，因此属性上一律没有 condition_operations。
// 而「这个属性能不能发 match」正是由 bkn-backend 在对象类详情里按资源实况派生的。
// 检索前按需补一次详情，实例召回才有判断依据；不补的话每个对象类都会因为「无可检索属性」
// 被跳过，语义实例召回永远返回空。
//
// 只补真正缺算子的对象类，且合并成一次批量请求。
func (s *localSearchImpl) backfillConditionOperations(
	ctx context.Context,
	knID string,
	objectTypes []*interfaces.KnSearchObjectType,
) {
	if knID == "" || len(objectTypes) == 0 {
		return
	}

	pending := map[string][]*interfaces.KnSearchObjectType{}
	ids := make([]string, 0, len(objectTypes))
	for _, objType := range objectTypes {
		if objType == nil || conditionOperationsPresent(objType) {
			continue
		}
		id := strings.TrimSpace(objType.ConceptID)
		if id == "" {
			continue
		}
		if _, seen := pending[id]; !seen {
			ids = append(ids, id)
		}
		// 同一个 id 可能出现多次（不同召回路径合并而来），补齐要覆盖到每一份。
		pending[id] = append(pending[id], objType)
	}
	if len(ids) == 0 {
		return
	}

	details, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, ids, true)
	if err != nil {
		// 补不到就退回原样：召回可能变空，但 Schema 结果仍然有效，不该整条失败。
		s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Backfill condition_operations failed for %d object types: %v", len(ids), err)
		return
	}

	filled := 0
	for _, detail := range details {
		if detail == nil {
			continue
		}
		targets := pending[strings.TrimSpace(detail.ID)]
		if len(targets) == 0 {
			continue
		}
		ops := map[string][]interfaces.KnOperationType{}
		for _, p := range detail.DataProperties {
			if p == nil || len(p.ConditionOperations) == 0 {
				continue
			}
			ops[p.Name] = p.ConditionOperations
		}
		if len(ops) == 0 {
			continue
		}
		for _, objType := range targets {
			for _, p := range objType.DataProperties {
				if p == nil || len(p.ConditionOperations) > 0 {
					continue
				}
				if o, ok := ops[p.Name]; ok {
					p.ConditionOperations = o
					filled++
				}
			}
		}
	}

	s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Backfilled condition_operations: object_types=%d properties=%d", len(ids), filled)
}

// conditionOperationsPresent 判断对象类的属性上是否已经带了算子。
func conditionOperationsPresent(objType *interfaces.KnSearchObjectType) bool {
	for _, p := range objType.DataProperties {
		if p != nil && len(p.ConditionOperations) > 0 {
			return true
		}
	}
	return false
}
