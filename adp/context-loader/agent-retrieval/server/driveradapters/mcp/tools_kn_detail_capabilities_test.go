// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// stubKnAuthz answers the network read check. The zero value allows.
type stubKnAuthz struct{ err error }

func (s stubKnAuthz) AuthorizeRead(_ context.Context, _ string) error { return s.err }

func knDetailWithCapabilities(t *testing.T, bkn *stubMetricBknBackend, level string) map[string]any {
	t.Helper()
	return knDetailAs(t, bkn, level, stubKnAuthz{})
}

func knDetailAs(t *testing.T, bkn *stubMetricBknBackend, level string, authz stubKnAuthz) map[string]any {
	t.Helper()
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), &mcpObjectSchemaAccessStub{}, authz)
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": level,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	return resultToMap(t, result)
}

func capabilityRefs(kind string, ids ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, &interfaces.CapabilityRef{CapabilityType: kind, CapabilityID: id, BoxID: "box-1"})
	}
	return refs
}

// TestKnDetailCountsWhatTheNetworkMounted is the reason this changed: the tool answers "what is
// this network", and it used to stop at the concept model. A network with three Skills and
// twenty one tools read exactly like one that had mounted nothing, so an agent had no reason to
// call search_capabilities at all.
func TestKnDetailCountsWhatTheNetworkMounted(t *testing.T) {
	bkn := &stubMetricBknBackend{
		detail: &interfaces.KnowledgeNetworkDetail{
			ID: "kn-001", ObjectTypes: []*interfaces.ObjectType{{ID: "ot-001", Name: "产品"}},
		},
		capabilities: append(
			append(capabilityRefs(interfaces.CapabilityTypeSkill, "s1", "s2", "s3"),
				capabilityRefs(interfaces.CapabilityTypeFunction, "t1", "t2")...),
			capabilityRefs(interfaces.CapabilityTypeMCPTool, "m1")...),
	}

	for _, level := range []string{interfaces.DetailLevelSummary, interfaces.DetailLevelFull} {
		m := knDetailWithCapabilities(t, bkn, level)
		counts, ok := m["mounted_capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("%s: 挂载计数该在, got %v", level, m["mounted_capabilities"])
		}
		for field, want := range map[string]float64{"total": 6, "skill": 3, "function": 2, "mcp_tool": 1} {
			if counts[field] != want {
				t.Fatalf("%s: %s want %v got %v", level, field, want, counts[field])
			}
		}
	}
}

// TestKnDetailSaysUnknownRatherThanZero keeps a failed lookup from reading as an empty network.
//
// Absent means the bindings could not be read; a present zero means the network really mounted
// nothing. Collapsing them would answer "this network has no capabilities" because one call
// failed — and the caller would have no way to tell.
func TestKnDetailSaysUnknownRatherThanZero(t *testing.T) {
	unreadable := &stubMetricBknBackend{
		detail: &interfaces.KnowledgeNetworkDetail{ID: "kn-001"},
		capErr: errors.New("bkn-backend unreachable"),
	}
	if got, present := knDetailWithCapabilities(t, unreadable, interfaces.DetailLevelSummary)["mounted_capabilities"]; present {
		t.Fatalf("读不到绑定时该缺席，不该报成 0, got %v", got)
	}

	empty := &stubMetricBknBackend{detail: &interfaces.KnowledgeNetworkDetail{ID: "kn-001"}}
	counts, ok := knDetailWithCapabilities(t, empty, interfaces.DetailLevelSummary)["mounted_capabilities"].(map[string]any)
	if !ok {
		t.Fatal("真的没挂时该给出 0，而不是缺席")
	}
	if counts["total"] != float64(0) {
		t.Fatalf("空网络的 total 该是 0, got %v", counts["total"])
	}
}

// TestKnDetailIgnoresUnusableBindings keeps the count matched to what search_capabilities can
// return: a binding with no capability id, or of a kind this platform does not execute, is not
// something a caller can act on, and counting it would advertise a capability that never appears.
func TestKnDetailIgnoresUnusableBindings(t *testing.T) {
	bkn := &stubMetricBknBackend{
		detail: &interfaces.KnowledgeNetworkDetail{ID: "kn-001"},
		capabilities: []*interfaces.CapabilityRef{
			{CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: "s1"},
			{CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: "   "},
			{CapabilityType: "operator", CapabilityID: "o1"},
			nil,
		},
	}
	counts := knDetailWithCapabilities(t, bkn, interfaces.DetailLevelSummary)["mounted_capabilities"].(map[string]any)
	if counts["total"] != float64(1) || counts["skill"] != float64(1) {
		t.Fatalf("只有那条可用的该被计入, got %v", counts)
	}
}

// TestKnDetailWithholdsCountsFromAnUnauthorizedCaller keeps this field from being the one place
// the bindings answer without a per-caller check.
//
// The metric counts beside it inherit their scope from object types FilterObjectTypes already
// filtered. The bindings have no such upstream filter and are read with the service's identity,
// so every other reader of them authorizes first. Withheld, not zero: the caller is not told the
// network is empty, only that this call did not establish the count.
func TestKnDetailWithholdsCountsFromAnUnauthorizedCaller(t *testing.T) {
	bkn := &stubMetricBknBackend{
		detail:       &interfaces.KnowledgeNetworkDetail{ID: "kn-001"},
		capabilities: capabilityRefs(interfaces.CapabilityTypeSkill, "s1", "s2"),
	}

	denied := knDetailAs(t, bkn, interfaces.DetailLevelSummary, stubKnAuthz{err: errors.New("forbidden")})
	if got, present := denied["mounted_capabilities"]; present {
		t.Fatalf("无权读该网络时不该给出挂载计数, got %v", got)
	}
	// The rest of the answer is unchanged: the concept model is filtered per caller already, and
	// failing the whole call would be a behaviour change this field does not justify.
	if denied["id"] != "kn-001" {
		t.Fatalf("其余部分该照常返回, got %v", denied["id"])
	}

	allowed := knDetailAs(t, bkn, interfaces.DetailLevelSummary, stubKnAuthz{})
	if allowed["mounted_capabilities"] == nil {
		t.Fatal("有权时该给出计数")
	}
}
