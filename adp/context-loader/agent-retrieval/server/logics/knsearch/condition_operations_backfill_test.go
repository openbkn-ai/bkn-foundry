// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func backfillTestObjectType(id string, ops []interfaces.KnOperationType) *interfaces.KnSearchObjectType {
	return &interfaces.KnSearchObjectType{
		ConceptID: id,
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "team_name", Type: "string", ConditionOperations: ops},
			{Name: "team_code", Type: "string"},
		},
	}
}

func TestBackfillConditionOperations_FillsFromDetail(t *testing.T) {
	backend := &mockBknBackend{
		objectDetailResp: []*interfaces.ObjectType{
			{
				ID: "teams",
				DataProperties: []*interfaces.DataProperty{
					{Name: "team_name", ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeEqual, interfaces.KnOperationTypeMatch,
					}},
					{Name: "team_code", ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeEqual,
					}},
				},
			},
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := backfillTestObjectType("teams", nil)

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	if backend.objectDetailCalls != 1 {
		t.Fatalf("expected exactly one batched detail call, got %d", backend.objectDetailCalls)
	}
	if len(objType.DataProperties[0].ConditionOperations) != 2 {
		t.Fatalf("team_name should be backfilled, got %+v", objType.DataProperties[0].ConditionOperations)
	}
	if len(objType.DataProperties[1].ConditionOperations) != 1 {
		t.Fatalf("team_code should be backfilled, got %+v", objType.DataProperties[1].ConditionOperations)
	}

	// 补齐后必须能被识别成可检索字段，否则等于没补。
	searchable := findSemanticSearchableFields(objType)
	if len(searchable) != 2 || !searchable[0].HasMatch {
		t.Fatalf("expected team_name to be match-searchable after backfill, got %+v", searchable)
	}
}

func TestBackfillConditionOperations_SkipsWhenAlreadyPresent(t *testing.T) {
	backend := &mockBknBackend{}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := backfillTestObjectType("teams", []interfaces.KnOperationType{interfaces.KnOperationTypeMatch})

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	if backend.objectDetailCalls != 0 {
		t.Fatalf("object types that already carry operations must not trigger a detail call, got %d", backend.objectDetailCalls)
	}
}

func TestBackfillConditionOperations_DegradesOnError(t *testing.T) {
	backend := &mockBknBackend{objectDetailError: context.DeadlineExceeded}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := backfillTestObjectType("teams", nil)

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	if len(objType.DataProperties[0].ConditionOperations) != 0 {
		t.Fatalf("failed backfill must leave the object type untouched, got %+v", objType.DataProperties[0].ConditionOperations)
	}
}

// 概念召回的 Schema 取自知识网络导出视图，属性上不带算子。补齐必须发生在这一阶段，
// 否则 search_schema 的响应里没有能力信息——Agent 就无从知道字段能不能 match / knn，
// 而这正是它规划查询的唯一依据。
func TestBackfillConditionOperations_MakesCapabilityVisibleInSchema(t *testing.T) {
	backend := &mockBknBackend{
		objectDetailResp: []*interfaces.ObjectType{
			{
				ID: "stadiums",
				DataProperties: []*interfaces.DataProperty{
					{Name: "stadium_name", ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeEqual,
						interfaces.KnOperationTypeMatch,
						interfaces.KnOperationTypeKnn,
					}},
				},
			},
		},
	}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "stadiums",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "stadium_name", Type: "string"},
		},
	}

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, false)

	ops := objType.DataProperties[0].ConditionOperations
	var hasMatch, hasKnn bool
	for _, op := range ops {
		switch op {
		case interfaces.KnOperationTypeMatch:
			hasMatch = true
		case interfaces.KnOperationTypeKnn:
			hasKnn = true
		}
	}
	if !hasMatch || !hasKnn {
		t.Fatalf("schema must expose what the field can actually do, got %+v", ops)
	}
}

// 精简只在出响应前做：实例召回要靠算子挑可检索字段，提前裁掉会让只支持等值的字段
// 整个消失——那是响应开关改了召回口径。实测全量比索引类多 10,453 字节（69 倍）。
func TestTrimToIndexBackedOperations(t *testing.T) {
	full := []interfaces.KnOperationType{
		interfaces.KnOperationTypeEqual,
		interfaces.KnOperationTypeIn,
		interfaces.KnOperationTypeLike,
		interfaces.KnOperationTypeMatch,
		interfaces.KnOperationTypeMultiMatch,
		interfaces.KnOperationTypeKnn,
	}
	newObjType := func() *interfaces.KnSearchObjectType {
		return &interfaces.KnSearchObjectType{
			ConceptID: "stadiums",
			DataProperties: []*interfaces.KnSearchDataProperty{
				{Name: "stadium_name", Type: "string", ConditionOperations: append([]interfaces.KnOperationType(nil), full...)},
				{Name: "stadium_id", Type: "string", ConditionOperations: []interfaces.KnOperationType{
					interfaces.KnOperationTypeEqual, interfaces.KnOperationTypeIn,
				}},
			},
		}
	}

	brief := newObjType()
	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{brief}, true)
	if len(brief.DataProperties[0].ConditionOperations) != 3 {
		t.Fatalf("brief keeps only index-backed operations, got %+v", brief.DataProperties[0].ConditionOperations)
	}
	if len(brief.DataProperties[1].ConditionOperations) != 0 {
		t.Fatalf("a field with no index capability goes bare, got %+v", brief.DataProperties[1].ConditionOperations)
	}

	kept := newObjType()
	trimToIndexBackedOperations([]*interfaces.KnSearchObjectType{kept}, false)
	if len(kept.DataProperties[0].ConditionOperations) != len(full) {
		t.Fatalf("full mode must keep every operation, got %+v", kept.DataProperties[0].ConditionOperations)
	}

	// 裁剪之前，只支持等值的字段必须仍然是可检索的——否则实例召回会漏掉它。
	searchable := findSemanticSearchableFields(newObjType())
	if len(searchable) != 2 {
		t.Fatalf("retrieval must see every field before trimming, got %+v", searchable)
	}
}
