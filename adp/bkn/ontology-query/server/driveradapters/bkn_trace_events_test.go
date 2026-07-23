// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"os"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/hydra"

	"ontology-query/interfaces"
)

func TestOntologyEvidenceEmittersReturnBeforeWorkWhenDisabled(t *testing.T) {
	t.Setenv("BKN_TRACE_EVIDENCE_INGEST_URL", "")
	_ = os.Unsetenv("BKN_TRACE_EVIDENCE_INGEST_URL")

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
