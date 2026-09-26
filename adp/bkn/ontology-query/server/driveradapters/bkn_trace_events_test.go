// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"

	"ontology-query/common"
	"ontology-query/common/bkntrace"
	"ontology-query/interfaces"
)

func TestOntologyTraceRequestContextUsesVerifiedOwnerAndIncomingOperation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("x-bkn-delegation-id", "spoofed")
	traceCtx := common.TraceContext{RequestID: "req_ontology_owner_001", ConversationID: "conv-1", InteractionID: "int-1", OperationID: "op-local"}
	ctx := common.SetTraceContextToCtx(context.Background(), traceCtx)
	visitor := hydra.Visitor{ID: "real-user", ClientID: "openbkn-sdk", Type: hydra.VisitorType_User}
	got := ontologyTraceRequestContext(c, ctx, visitor)
	if got.ApplicationPrincipalID != "openbkn-sdk" || got.EffectiveSubjectID != "real-user" || got.EffectiveSubjectType != "user" || got.DelegationID != "" || got.OperationScopePresent {
		t.Fatalf("owner and local operation = %+v, want verified owner without asserted Core operation", got)
	}
	c.Request.Header.Set(common.HeaderBKNOperationID, "op-local")
	got = ontologyTraceRequestContext(c, ctx, visitor)
	if !got.OperationScopePresent {
		t.Fatal("matching incoming Core operation was not preserved")
	}
}

func TestOntologyEvidenceEmittersReturnBeforeWorkWhenDisabled(t *testing.T) {
	bkntrace.SetEvidencePublisher(nil)

	visitor := hydra.Visitor{ID: "acct_demo", Type: hydra.VisitorType_User}

	emitObjectQueryEvidence(nil, context.Background(), visitor, nil, &interfaces.Objects{
		Datas: []map[string]any{{"_instance_id": "obj_1"}},
	})
	emitSubgraphEvidence(nil, context.Background(), visitor, "kn_demo", "main", "bkn.relation.query", map[string]any{"unsafe": "would_hash"}, &interfaces.ObjectSubGraph{
		Objects: map[string]interfaces.ObjectInfoInSubgraph{
			"obj_1": {ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceID: "obj_1"}},
		},
	})
	emitSubgraphEntriesEvidence(nil, context.Background(), visitor, "kn_demo", "main", map[string]any{"unsafe": "would_hash"}, interfaces.PathsEntries{
		Entries: []interfaces.ObjectSubGraph{
			{Objects: map[string]interfaces.ObjectInfoInSubgraph{"obj_1": {ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceID: "obj_1"}}}},
		},
	})
	emitMetricEvidence(nil, context.Background(), visitor, "kn_demo", "main", "metric_demo", "bkn.metric.get", map[string]any{"unsafe": "would_hash"}, &interfaces.MetricData{
		Datas: []interfaces.Data{{Labels: map[string]string{"pii": "value"}}},
	})
}
