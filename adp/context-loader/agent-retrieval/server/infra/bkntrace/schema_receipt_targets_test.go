// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestSchemaSnapshotRetainsResolvedReceiptTargets(t *testing.T) {
	previous := currentEvidencePublisher()
	SetEvidencePublisher(nil)
	t.Cleanup(func() { SetEvidencePublisher(previous) })
	for _, tt := range []struct{ kind, refType string }{{"object", "object_type"}, {"relation", "relation_type"}} {
		t.Run(tt.kind, func(t *testing.T) {
			ctx := withEvidenceOutcome(testTraceContext())
			ctx = withRequestDerivedBusinessRefs(ctx, []BusinessRef{{RefType: "knowledge_network", RefID: "kn:kn-demo", Version: "unversioned"}})
			EmitSchemaSnapshotEvents(ctx, nil, tt.kind, "kn-demo", []string{"resolved-id", "resolved-id", "", "bad:id"}, nil, true)
			var finishBody struct {
				BusinessRefs       []BusinessRef `json:"business_refs"`
				EvidenceDurability string        `json:"evidence_durability"`
			}
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(r.Body).Decode(&finishBody); err != nil {
					t.Fatal(err)
				}
				return lifecycleJSONResponse(http.StatusOK, OperationResult{}), nil
			})
			state := GuardState{Result: OperationResult{Operation: Operation{OperationID: "op", Attempt: 1}, Receipt: Receipt{ReceiptID: "receipt"}}}
			if _, apiErr, err := NewGuard(client).Finish(ctx, state, map[string]any{"result": "unchanged"}, false, false); err != nil || apiErr != nil {
				t.Fatalf("finish: api=%v err=%v", apiErr, err)
			}
			if finishBody.EvidenceDurability != "pending" {
				t.Fatalf("retaining targets certified evidence delivery: %s", finishBody.EvidenceDurability)
			}
			got := finishBody.BusinessRefs
			want := []BusinessRef{{RefType: tt.refType, RefID: tt.kind + ":kn-demo:resolved-id", Version: "unversioned"}, {RefType: "knowledge_network", RefID: "kn:kn-demo", Version: "unversioned"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("resolved schema targets lost from receipt: got %+v want %+v", got, want)
			}
		})
	}
}

func TestSchemaSnapshotTargetRetentionPreservesAttemptBoundary(t *testing.T) {
	previous := currentEvidencePublisher()
	SetEvidencePublisher(nil)
	t.Cleanup(func() { SetEvidencePublisher(previous) })
	t.Run("closed attempt", func(t *testing.T) {
		ctx := withEvidenceOutcome(context.Background())
		freezeEvidenceExpectation(ctx)
		EmitSchemaSnapshotEvents(ctx, nil, "object", "kn-demo", []string{"late"}, nil, true)
		if got := retainedBusinessRefs(ctx); len(got) != 0 {
			t.Fatalf("late targets changed frozen attempt: %+v", got)
		}
	})
	t.Run("other network", func(t *testing.T) {
		ctx := withEvidenceOutcome(context.Background())
		ctx = withRequestDerivedBusinessRefs(ctx, []BusinessRef{{RefType: "knowledge_network", RefID: "kn:kn-demo", Version: "unversioned"}})
		EmitSchemaSnapshotEvents(ctx, nil, "object", "other", []string{"wrong"}, nil, true)
		if got := retainedBusinessRefs(ctx); len(got) != 0 {
			t.Fatalf("foreign targets retained: %+v", got)
		}
	})
	t.Run("network snapshot", func(t *testing.T) {
		ctx := withEvidenceOutcome(context.Background())
		EmitSchemaSnapshotEvents(ctx, nil, "network", "kn-demo", []string{"ignored"}, nil, true)
		if got := retainedBusinessRefs(ctx); len(got) != 0 {
			t.Fatalf("network snapshot invented object targets: %+v", got)
		}
	})
	t.Run("unmanaged", func(t *testing.T) {
		ctx := context.Background()
		EmitSchemaSnapshotEvents(ctx, nil, "object", "kn-demo", []string{"object"}, nil, true)
		if got := retainedBusinessRefs(ctx); len(got) != 0 {
			t.Fatalf("unmanaged call acquired attempt state: %+v", got)
		}
	})
}
