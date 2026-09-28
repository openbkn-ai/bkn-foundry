// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// countingSchemaAccess answers like the ordinary stub and says how often it was asked,
// which is what "the shell costs nothing per object type" has to mean.
type countingSchemaAccess struct{ calls atomic.Int64 }

func (c *countingSchemaAccess) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	c.calls.Add(1)
	return &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"id": interfaces.PropertyAccessFull},
	}, nil
}

func networkOfSize(n int) *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{
		ID:   "kn-001",
		Name: "medical",
		ConceptGroups: []*interfaces.ConceptGroup{
			{ID: "cg-clinical", Name: "临床", Comment: "就诊、医嘱与检验"},
		},
	}
	for i := range n {
		detail.ObjectTypes = append(detail.ObjectTypes, &interfaces.ObjectType{
			ID:             fmt.Sprintf("ot-%04d", i),
			Name:           fmt.Sprintf("对象类-%04d", i),
			DataSource:     &interfaces.ResourceInfo{Type: "resource", ID: "res"},
			DataProperties: []*interfaces.DataProperty{{Name: "id", Type: "string"}},
		})
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{ID: fmt.Sprintf("rt-%04d", i)})
	}
	return detail
}

func knDetailWithSchemaCounter(t *testing.T, n int, level string) (map[string]any, int64) {
	t.Helper()
	access := &countingSchemaAccess{}
	bkn := &stubMetricBknBackend{
		detail:       networkOfSize(n),
		capabilities: capabilityRefs(interfaces.CapabilityTypeSkill, "s1"),
	}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), access, stubKnAuthz{})
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": level,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	return resultToMap(t, result), access.calls.Load()
}

// The #1877 regression: a 1000-object network cost one authorization read per object
// type to build an answer of over a megabyte, so it timed out at 30s and would have
// been unreadable had it returned. Past the cap the arrays are not built at all --
// which is also why the reads that would have filled them must not happen.
func TestKnDetailNarrowsAnOversizedNetworkToItsNavigationShell(t *testing.T) {
	// The size the report measured, stated outright rather than derived from the cap:
	// a cap raised past it would put the regression back, and a test written in terms
	// of the cap would follow it up and keep passing.
	const reportedNetwork = 1000
	if interfaces.MaxSummaryObjectTypes >= reportedNetwork {
		t.Fatalf("MaxSummaryObjectTypes = %d no longer narrows the %d-object network of #1877",
			interfaces.MaxSummaryObjectTypes, reportedNetwork)
	}

	for _, level := range []string{interfaces.DetailLevelSummary, interfaces.DetailLevelFull} {
		m, reads := knDetailWithSchemaCounter(t, reportedNetwork, level)

		if reads != 0 {
			t.Fatalf("%s: read %d object type schemas for an answer that carries none", level, reads)
		}
		// null, not absent: the field has no omitempty, and an empty network under the
		// cap already answers with null, so the shell says "none here" the same way.
		// The documented contract states null, and this is what pins it.
		for _, field := range []string{"object_types", "relation_types", "action_types"} {
			got, present := m[field]
			if !present {
				t.Fatalf("%s: %s must be present as null, not dropped", level, field)
			}
			if got != nil {
				t.Fatalf("%s: %s should be null, got %v", level, field, got)
			}
		}
		if got := m["object_type_count"]; got != float64(reportedNetwork) {
			t.Fatalf("%s: object_type_count = %v, want %d", level, got, reportedNetwork)
		}
		if got := m["relation_type_count"]; got != float64(reportedNetwork) {
			t.Fatalf("%s: relation_type_count = %v", level, got)
		}
		notice, _ := m["notice"].(string)
		if notice == "" {
			t.Fatalf("%s: empty arrays without a notice read as a network that has no concepts", level)
		}
		// The shell is only useful if it carries what the caller narrows by next.
		groups, ok := m["concept_groups"].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("%s: concept_groups must survive the shell, got %v", level, m["concept_groups"])
		}
		if m["mounted_capabilities"] == nil {
			t.Fatalf("%s: mounted_capabilities must survive the shell", level)
		}
	}
}

// At the cap the answer is unchanged, so networks that work today keep working.
func TestKnDetailAtTheCapStillCarriesTheWholeModel(t *testing.T) {
	m, reads := knDetailWithSchemaCounter(t, interfaces.MaxSummaryObjectTypes, interfaces.DetailLevelSummary)

	if reads != int64(interfaces.MaxSummaryObjectTypes) {
		t.Fatalf("read %d object type schemas, want %d", reads, interfaces.MaxSummaryObjectTypes)
	}
	objectTypes, ok := m["object_types"].([]any)
	if !ok || len(objectTypes) != interfaces.MaxSummaryObjectTypes {
		t.Fatalf("got %v object types, want %d", len(objectTypes), interfaces.MaxSummaryObjectTypes)
	}
	if got, present := m["object_type_count"]; present {
		t.Fatalf("the array is the count here; object_type_count should be absent, got %v", got)
	}
	if got, present := m["notice"]; present {
		t.Fatalf("nothing was narrowed, so there is nothing to notice, got %v", got)
	}
}

// A network with no concept groups gets a notice that points somewhere it can
// actually go. #1877's network is one: 1000 object types, zero groups.
func TestKnDetailShellWithoutConceptGroupsPointsAtSearchSchema(t *testing.T) {
	access := &countingSchemaAccess{}
	detail := networkOfSize(1000)
	detail.ConceptGroups = nil
	bkn := &stubMetricBknBackend{detail: detail}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), access, stubKnAuthz{})
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": interfaces.DetailLevelSummary,
	}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: %v %+v", err, result)
	}
	notice, _ := resultToMap(t, result)["notice"].(string)
	if !strings.Contains(notice, "search_schema") {
		t.Fatalf("notice must name where to go instead, got %q", notice)
	}
	if strings.Contains(notice, "concept_groups") {
		t.Fatalf("notice must not send the caller to an empty field, got %q", notice)
	}
	if access.calls.Load() != 0 {
		t.Fatalf("read %d schemas for a shell", access.calls.Load())
	}
}

// A relation-heavy network narrows on its relation count alone. The reported
// network carried 2925 relation types over 1000 object types -- a quarter of its
// bytes -- so a model with few object types and many relations is just as
// unreadable while passing the object type cap.
func TestKnDetailNarrowsARelationHeavyNetwork(t *testing.T) {
	access := &countingSchemaAccess{}
	detail := networkOfSize(20)
	for i := range interfaces.MaxSummaryRelationTypes + 1 {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{ID: fmt.Sprintf("extra-rt-%04d", i)})
	}
	relations := len(detail.RelationTypes)
	bkn := &stubMetricBknBackend{detail: detail}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), access, stubKnAuthz{})
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": interfaces.DetailLevelSummary,
	}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: %v %+v", err, result)
	}
	m := resultToMap(t, result)
	if got := m["relation_type_count"]; got != float64(relations) {
		t.Fatalf("relation_type_count = %v, want %d", got, relations)
	}
	got, present := m["relation_types"]
	if !present || got != nil {
		t.Fatalf("relation_types should be present and null, got %v (present=%v)", got, present)
	}
	if got := m["object_type_count"]; got != float64(20) {
		t.Fatalf("object_type_count = %v, want 20 -- the object types are under their own cap", got)
	}
	if access.calls.Load() != 0 {
		t.Fatalf("read %d object type schemas for a shell", access.calls.Load())
	}
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "20") || !strings.Contains(notice, fmt.Sprint(relations)) {
		t.Fatalf("the notice must say what this network actually holds, got %q", notice)
	}
}
