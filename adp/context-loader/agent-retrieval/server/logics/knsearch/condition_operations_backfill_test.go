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

// 精简 Schema 只补索引带来的算子：比较算子按属性类型可推导，逐个下发纯属浪费体积。
// 实测一次检索差 69 倍（+10,453 字节 vs +151 字节）。
func TestBackfillConditionOperations_BriefKeepsOnlyIndexBackedOps(t *testing.T) {
	backend := &mockBknBackend{
		objectDetailResp: []*interfaces.ObjectType{
			{
				ID: "stadiums",
				DataProperties: []*interfaces.DataProperty{
					{Name: "stadium_name", ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeEqual,
						interfaces.KnOperationTypeIn,
						interfaces.KnOperationTypeLike,
						interfaces.KnOperationTypeMatch,
						interfaces.KnOperationTypeMultiMatch,
						interfaces.KnOperationTypeKnn,
					}},
					{Name: "stadium_id", ConditionOperations: []interfaces.KnOperationType{
						interfaces.KnOperationTypeEqual,
						interfaces.KnOperationTypeIn,
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
			{Name: "stadium_id", Type: "string"},
		},
	}

	svc.backfillConditionOperations(context.Background(), "kn1", []*interfaces.KnSearchObjectType{objType}, true)

	got := objType.DataProperties[0].ConditionOperations
	if len(got) != 3 {
		t.Fatalf("brief mode keeps only the index-backed operations, got %+v", got)
	}
	for _, op := range got {
		switch op {
		case interfaces.KnOperationTypeMatch, interfaces.KnOperationTypeMultiMatch, interfaces.KnOperationTypeKnn:
		default:
			t.Fatalf("derivable comparison operator leaked into brief mode: %s", op)
		}
	}
	// 没有索引能力的属性在精简模式下整个字段都不出现，省掉一份重复的比较算子清单。
	if len(objType.DataProperties[1].ConditionOperations) != 0 {
		t.Fatalf("a field with no index capability should stay bare, got %+v", objType.DataProperties[1].ConditionOperations)
	}
}
