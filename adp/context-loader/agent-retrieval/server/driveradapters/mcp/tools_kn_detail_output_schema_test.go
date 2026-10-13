// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

func networkAtConceptCounts(objectTypes, relationTypes int) *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{
		ID:          "kn-contract",
		Name:        "contract network",
		ActionTypes: []*interfaces.ActionType{{ID: "at-1", Name: "act"}},
		ConceptGroups: []*interfaces.ConceptGroup{
			{ID: "cg-1", Name: "group", Comment: "contract fixture"},
		},
	}
	for i := range objectTypes {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID:             fmt.Sprintf("ot-%04d", i),
			Name:           fmt.Sprintf("object-%04d", i),
			DataSource:     &interfaces.ResourceInfo{Type: "resource", ID: "res"},
			DataProperties: []*interfaces.DataProperty{{Name: "id", Type: "string"}},
		})
	}
	for i := range relationTypes {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID:   fmt.Sprintf("rt-%04d", i),
			Name: fmt.Sprintf("relation-%04d", i),
		})
	}
	return detail
}

// The wire contract has two legitimate shapes: ordinary concept arrays at the
// limits, and a navigation shell with null arrays immediately past either one.
// Validate the handler's structuredContent against the same embedded schema
// advertised through tools/list and /mcp/info so prose and runtime cannot drift.
func TestGetKnDetailOutputSchemaAcceptsBothShapesAtTheBoundaries(t *testing.T) {
	_, rawSchema := loadToolSchemas(toolKeyGetKnDetail)
	schema, err := compileExecutableSchema(rawSchema)
	if err != nil {
		t.Fatalf("compile get_kn_detail output schema: %v", err)
	}

	tests := []struct {
		name          string
		objectTypes   int
		relationTypes int
		wantShell     bool
	}{
		{name: "object types at limit", objectTypes: interfaces.MaxSummaryObjectTypes, relationTypes: 1},
		{name: "object types past limit", objectTypes: interfaces.MaxSummaryObjectTypes + 1, relationTypes: 1, wantShell: true},
		{name: "relation types at limit", objectTypes: 1, relationTypes: interfaces.MaxSummaryRelationTypes},
		{name: "relation types past limit", objectTypes: 1, relationTypes: interfaces.MaxSummaryRelationTypes + 1, wantShell: true},
	}

	for _, level := range []string{interfaces.DetailLevelSummary, interfaces.DetailLevelFull} {
		for _, tt := range tests {
			t.Run(level+"/"+tt.name, func(t *testing.T) {
				access := &countingSchemaAccess{}
				bkn := &stubMetricBknBackend{detail: networkAtConceptCounts(tt.objectTypes, tt.relationTypes)}
				handler := handleGetKnDetail(
					bkn,
					knmetrics.NewKnMetricsServiceWith(nil, bkn, nil),
					access,
					stubKnAuthz{},
				)
				result, err := handler(context.Background(), mcpReq(map[string]any{
					"kn_id": "kn-contract", "response_format": "json", "detail_level": level,
				}))
				if err != nil || result.IsError {
					t.Fatalf("get_kn_detail failed: %v %+v", err, result)
				}
				structured := resultToMap(t, result)
				if err := schema.Validate(structured); err != nil {
					t.Fatalf("structuredContent does not match advertised output schema: %v", err)
				}

				for _, field := range []string{"object_types", "relation_types", "action_types"} {
					value, present := structured[field]
					if !present {
						t.Errorf("%s is absent", field)
						continue
					}
					if tt.wantShell && value != nil {
						t.Errorf("navigation shell %s = %v, want null", field, value)
					}
					if !tt.wantShell {
						if _, ok := value.([]any); !ok {
							t.Errorf("ordinary response %s = %T, want array", field, value)
						}
					}
				}
				if tt.wantShell {
					if structured["notice"] == nil || structured["object_type_count"] == nil || structured["relation_type_count"] == nil || structured["action_type_count"] == nil {
						t.Errorf("navigation shell lacks counts or notice: %v", structured)
					}
				} else if structured["notice"] != nil || structured["object_type_count"] != nil || structured["relation_type_count"] != nil || structured["action_type_count"] != nil {
					t.Errorf("ordinary response unexpectedly carries navigation-shell fields: %v", structured)
				}
			})
		}
	}
}
