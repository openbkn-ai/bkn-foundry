package kntools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func skillHit(id, name string) interfaces.CapabilityHit {
	return interfaces.CapabilityHit{
		SearchCapabilityRef: interfaces.SearchCapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill,
			CapabilityID:   id,
		},
		Name: name,
	}
}

// TestSearchCapabilitiesKeepsOneRanking is the reason this endpoint exists. The three kinds share
// an index and a ranking; asking through two entry points put the merge back on the agent, and the
// two answers had no comparable score to merge on.
func TestSearchCapabilitiesKeepsOneRanking(t *testing.T) {
	op := &fakeOperator{
		// Interleaved by the fused rank: a Skill first, then a tool, then a Skill again.
		hits: []interfaces.CapabilityHit{
			skillHit("s-fx", "汇率换算"),
			hit("box-1", "t1"),
			skillHit("s-report", "周报生成"),
		},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1"),
		},
	}
	bkn := &fakeBkn{refs: append(functionRefs("box-1/t1"), skillRefs("s-fx", "s-report")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", Query: "汇率",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 3 {
		t.Fatalf("三类都该在, got %+v", resp.Capabilities)
	}
	// The order must be the ranking's, not one block per kind.
	got := []string{
		resp.Capabilities[0].CapabilityType,
		resp.Capabilities[1].CapabilityType,
		resp.Capabilities[2].CapabilityType,
	}
	want := []string{
		interfaces.CapabilityTypeSkill,
		interfaces.CapabilityTypeFunction,
		interfaces.CapabilityTypeSkill,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序被按类型重排了: got %v want %v", got, want)
		}
	}
	// A tool is called and needs its schema; a Skill is read and carries none.
	if resp.Capabilities[1].InputSchema == nil {
		t.Fatal("工具类命中该带 input_schema")
	}
	if resp.Capabilities[0].InputSchema != nil {
		t.Fatal("Skill 是读的不是调的，不该带 input_schema")
	}
}

// TestSearchCapabilitiesWhitelistIsTheScope keeps the network's mounts as the only scope, and keeps
// Skills in it — boundRefs drops them, and reusing that here would have silently answered for two
// kinds out of three.
func TestSearchCapabilitiesWhitelistIsTheScope(t *testing.T) {
	op := &fakeOperator{}
	bkn := &fakeBkn{refs: append(functionRefs("box-1/t1"), skillRefs("s-1")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	if _, err := svc.SearchCapabilities(context.Background(),
		&SearchCapabilitiesReq{KnID: "kn1", Query: "汇率"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.gotRefs) != 2 {
		t.Fatalf("白名单该含两类共 2 条, got %v", op.gotRefs)
	}
}

// TestSearchCapabilitiesFailsClosed keeps an unmounted network from being answered with the
// platform.
func TestSearchCapabilitiesFailsClosed(t *testing.T) {
	op := &fakeOperator{}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: nil}, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 0 {
		t.Fatalf("未挂载任何能力时该返回空, got %+v", resp.Capabilities)
	}
	if len(op.gotRefs) != 0 {
		t.Fatal("白名单为空时不该去问排序")
	}
	if resp.Message == "" {
		t.Fatal("空结果该给说法")
	}
}

// TestSearchCapabilitiesRequiresKnID keeps the scope from defaulting to everything.
func TestSearchCapabilitiesRequiresKnID(t *testing.T) {
	svc := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{}, &fakeKnAuthz{})
	if _, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{}); err == nil {
		t.Fatal("缺 kn_id 该被拒绝")
	}
}

// TestKindFilterNarrowsTheWhitelistNotJustTheRanking keeps the scope from depending on the ranking
// to honour a filter.
//
// A kind the caller excluded must never leave this service in the whitelist. Sending types
// downstream is not a substitute: it makes the scope conditional on the index reading a field,
// and a Skill that slipped through would come back where the caller asked for tools only.
func TestKindFilterNarrowsTheWhitelistNotJustTheRanking(t *testing.T) {
	op := &fakeOperator{}
	bkn := &fakeBkn{refs: append(functionRefs("box-1/t1"), skillRefs("s-1")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	if _, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", Query: "汇率",
		Types: []string{interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool},
	}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for _, ref := range op.gotRefs {
		if strings.HasPrefix(ref, "skill") || ref == "/s-1" {
			t.Fatalf("排除掉的类型不该进白名单: %v", op.gotRefs)
		}
	}
	if len(op.gotRefs) != 1 {
		t.Fatalf("白名单该只剩那个函数工具, got %v", op.gotRefs)
	}
}

// TestListingSurvivesAnUnreachableIndex keeps the degradation find_skills had, which this endpoint
// inherited when find_skills was deleted (#1401): with no query there is nothing to rank, so an
// index that is down, behind, or not built yet (#1323) must not stop a network from listing what
// it mounted.
func TestListingSurvivesAnUnreachableIndex(t *testing.T) {
	op := &fakeOperator{
		hitsErr: errors.New("dataset resource has no available local index"),
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1"),
		},
		skillNames: map[string]string{"s-fx": "汇率换算"},
	}
	bkn := &fakeBkn{refs: append(functionRefs("box-1/t1"), skillRefs("s-fx")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("列表不该因索引不可用而失败, got %v", err)
	}
	if len(resp.Capabilities) != 2 {
		t.Fatalf("挂载的两个都该列出, got %+v", resp.Capabilities)
	}
	// Binding order, not index order: with no query there is nothing else to order by.
	if resp.Capabilities[0].CapabilityType != interfaces.CapabilityTypeFunction ||
		resp.Capabilities[1].CapabilityType != interfaces.CapabilityTypeSkill {
		t.Fatalf("该按绑定顺序返回, got %+v", resp.Capabilities)
	}
	// The Skill carries no schema of its own, so a name from the registry is the whole answer —
	// an unnamed entry would be indistinguishable from a dead binding.
	if resp.Capabilities[1].Name != "汇率换算" {
		t.Fatalf("技能名该从注册表补上, got %q", resp.Capabilities[1].Name)
	}
}

// TestRankingStillFailsWhenTheIndexIsDown guards the other half: a query is a ranking request, and
// there is nothing to degrade to. Answering it from the bindings would return the mounted set in
// declaration order and call it a search result.
func TestRankingStillFailsWhenTheIndexIsDown(t *testing.T) {
	op := &fakeOperator{hitsErr: errors.New("dataset resource has no available local index")}
	bkn := &fakeBkn{refs: skillRefs("s-fx")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	if _, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", Query: "汇率",
	}); err == nil {
		t.Fatal("带 query 时索引不可用必须报错,不能悄悄退化成列表")
	}
}

// TestListingDropsSkillsTheRegistryDoesNotKnow covers the binding that outlived its Skill. The
// mount survives deletion in the execution factory, and listing it would send the caller to
// get_skill_content for something that is gone.
func TestListingDropsSkillsTheRegistryDoesNotKnow(t *testing.T) {
	op := &fakeOperator{
		hitsErr:    errors.New("index down"),
		skillNames: map[string]string{"s-alive": "在的技能"},
	}
	bkn := &fakeBkn{refs: skillRefs("s-alive", "s-deleted")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].CapabilityID != "s-alive" {
		t.Fatalf("注册表不认识的技能该被丢掉, got %+v", resp.Capabilities)
	}
}
