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

	// After completion, it must be recognized as a searchable field, otherwise it means no completion.
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

// The Schema of concept recall is taken from the knowledge network export view, without operators on the attributes. Completion must occur at this stage,
// Otherwise, there is no capability information in the search_schema response - the Agent has no way of knowing whether the field can match / knn.
// And that's the only basis it plans queries on.
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

// Clipping is only done before the response is issued, and only for the MCP side: instance recall relies on operators to select searchable fields, and pruning in advance will only support fields with equivalent values.
// The whole thing disappeared - that's because the response switch changed the recall semantics. The measured full size is 10,453 bytes (69 times) larger than the index class.
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

	// A field that only supports equal values must still be searchable before pruning - otherwise instance recall will miss it.
	searchable := findSemanticSearchableFields(newObjType())
	if len(searchable) != 2 {
		t.Fatalf("retrieval must see every field before trimming, got %+v", searchable)
	}
}
