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

// TestListingDoesNotLetTheIndexDecideMembership is the parity control for retiring find_skills.
//
// find_skills listed the bindings and used the index only to describe them, so a Skill that was
// mounted but not yet indexed still appeared. The index is built asynchronously and a fresh
// install has none (#1323); if it decided membership, mounting a capability and listing it right
// afterwards would come back empty and look like the mount had failed.
func TestListingDoesNotLetTheIndexDecideMembership(t *testing.T) {
	op := &fakeOperator{
		// The index knows one of the three. The other two were mounted more recently than the
		// last build.
		hits: []interfaces.CapabilityHit{hit("box-1", "t1")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1", "t2"),
		},
		skillNames: map[string]string{"s-new": "刚挂上的技能"},
	}
	bkn := &fakeBkn{refs: append(functionRefs("box-1/t1", "box-1/t2"), skillRefs("s-new")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 3 {
		t.Fatalf("挂了三个就该列三个，索引落后不该让它们消失, got %+v", resp.Capabilities)
	}
	ids := []string{
		resp.Capabilities[0].CapabilityID,
		resp.Capabilities[1].CapabilityID,
		resp.Capabilities[2].CapabilityID,
	}
	// Binding order, so the answer does not reorder itself as the index catches up.
	want := []string{"t1", "t2", "s-new"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("该按绑定顺序返回, want %v got %v", want, ids)
		}
	}
	// The index missed t2, but the catalogue still describes it: a listed tool has to be callable.
	if resp.Capabilities[1].InputSchema == nil {
		t.Fatalf("索引没收录的工具也该从目录补上 input_schema, got %+v", resp.Capabilities[1])
	}
	if resp.Capabilities[2].Name != "刚挂上的技能" {
		t.Fatalf("索引没收录的技能该从注册表补名, got %q", resp.Capabilities[2].Name)
	}
}

// TestMetadataFilterIsNotSilentlyDropped guards the one filter the bindings cannot answer.
//
// metadata_type distinguishes an API tool from a function and lives only in the index document.
// Listing from the bindings when the index is unreachable would return every Function tool while
// the caller had asked for one kind — worse than an error, because nothing in the answer says the
// filter did not run.
func TestMetadataFilterIsNotSilentlyDropped(t *testing.T) {
	op := &fakeOperator{
		hitsErr: errors.New("dataset resource has no available local index"),
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1"),
		},
	}
	bkn := &fakeBkn{refs: functionRefs("box-1/t1")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	if _, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", MetadataTypes: []string{"openapi"},
	}); err == nil {
		t.Fatal("索引不可用时 metadata_types 无从判定，必须报错而不是当没传")
	}
}

// TestListingAsksTheIndexAboutThePageItReturns catches a mismatch that only shows up under a
// small limit.
//
// A listing pages the mounted set in binding order, but the ranking pages by relevance. Asking it
// for `limit` hits and then keeping the first `limit` bindings selects two different subsets, so
// most of the returned page has no index entry to describe it and comes back unnamed — and how
// much of it is named depends on limit, which is how this got past a large-limit check.
func TestListingAsksTheIndexAboutThePageItReturns(t *testing.T) {
	op := &fakeOperator{
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-1": tools("box-1", "t1", "t2", "t3", "t4", "t5"),
		},
	}
	bkn := &fakeBkn{refs: functionRefs("box-1/t1", "box-1/t2", "box-1/t3", "box-1/t4", "box-1/t5")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", Limit: 2,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// The page plus the one extra used to detect a next page — nothing wider.
	want := []string{"box-1/t1", "box-1/t2", "box-1/t3"}
	if len(op.gotRefs) != len(want) {
		t.Fatalf("索引该只被问这一页, want %v got %v", want, op.gotRefs)
	}
	for i := range want {
		if op.gotRefs[i] != want[i] {
			t.Fatalf("问索引的正是要返回的那一页, want %v got %v", want, op.gotRefs)
		}
	}
	if len(resp.Capabilities) != 2 || !resp.Truncated {
		t.Fatalf("该返回一页并报截断, got %d truncated=%v", len(resp.Capabilities), resp.Truncated)
	}
}

// TestEmptyKindFilterDoesNotClaimAnEmptyNetwork separates the two ways a whitelist ends up empty.
//
// Filters are applied to the bindings before the search runs, so asking a well-stocked network for
// a kind it lacks empties the whitelist just like an unmounted network does. Reporting both as
// "this network has mounted nothing" sends the caller to mount capabilities that are already
// there, instead of to the filter they set.
func TestEmptyKindFilterDoesNotClaimAnEmptyNetwork(t *testing.T) {
	svc := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})

	filtered, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{
		KnID: "kn1", Types: []string{interfaces.CapabilityTypeMCPTool},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(filtered.Capabilities) != 0 {
		t.Fatalf("这个用例要的是空结果, got %+v", filtered.Capabilities)
	}

	empty := NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{refs: nil}, &fakeKnAuthz{})
	unmounted, err := empty.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if filtered.Message == unmounted.Message {
		t.Fatalf("挂了能力但被类型筛空，和压根没挂，不该给同一句话: %q", filtered.Message)
	}
}

// TestEmptyResultNamesTheFilterTheCallerSet pins the whole family of empty-result advice.
//
// The fix for an empty result is to drop the filter that caused it, so naming a different one is
// advice that cannot be followed. This has been wrong three ways already: a pinned kinds filter
// the caller could not unset, a network with capabilities reported as empty, and a caller who
// narrowed by owner told to remove types.
func TestEmptyResultNamesTheFilterTheCallerSet(t *testing.T) {
	// Mounted, so nothing here can be blamed on an empty network.
	newSvc := func() KnToolsService {
		return NewKnToolsServiceWith(&fakeOperator{}, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
	}
	ask := func(t *testing.T, req *SearchCapabilitiesReq) string {
		t.Helper()
		resp, err := newSvc().SearchCapabilities(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(resp.Capabilities) != 0 {
			t.Fatalf("这些用例要的都是空结果, got %+v", resp.Capabilities)
		}
		return resp.Message
	}

	byKind := ask(t, &SearchCapabilitiesReq{KnID: "kn1", Types: []string{interfaces.CapabilityTypeMCPTool}})
	byOwner := ask(t, &SearchCapabilitiesReq{KnID: "kn1", OwnerID: "box-does-not-exist"})
	byBoth := ask(t, &SearchCapabilitiesReq{
		KnID: "kn1", Types: []string{interfaces.CapabilityTypeMCPTool}, OwnerID: "box-does-not-exist",
	})

	if strings.Contains(byOwner, "types") {
		t.Fatalf("只按 owner_id 收窄时，不该让调用方去掉它没传的 types: %q", byOwner)
	}
	if !strings.Contains(byOwner, "owner_id") {
		t.Fatalf("该点名调用方设的那个过滤器: %q", byOwner)
	}
	if !strings.Contains(byKind, "types") || strings.Contains(byKind, "owner_id") {
		t.Fatalf("只按类型收窄时该只点名类型: %q", byKind)
	}
	if !strings.Contains(byBoth, "owner_id") || !strings.Contains(byBoth, "types") {
		t.Fatalf("两个都设了就该都点名: %q", byBoth)
	}
}

// The lifecycle gate (#1443). The index can hold a tool for up to one reconcile after its owner
// was withdrawn, and the ranking will return it; these pin that such a hit is never offered, that
// "could not confirm" counts as withdrawn, and that the answer says why.

func mcpRefs(mcpID string, names ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(names))
	for _, name := range names {
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeMCPTool, BoxID: mcpID, CapabilityID: name,
		})
	}
	return refs
}

func TestWithdrawnToolBoxIsNotOffered(t *testing.T) {
	op := &fakeOperator{
		hits: []interfaces.CapabilityHit{hit("box-gone", "t1"), hit("box-live", "t2")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-gone": tools("box-gone", "t1"), "box-live": tools("box-live", "t2"),
		},
		boxUnpublished: map[string]bool{"box-gone": true},
	}
	bkn := &fakeBkn{refs: functionRefs("box-gone/t1", "box-live/t2")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].OwnerID != "box-live" {
		t.Fatalf("下线工具箱的工具不该出现,在线的照常, got %+v", resp.Capabilities)
	}
}

func TestUnconfirmedOwnerCountsAsWithdrawn(t *testing.T) {
	op := &fakeOperator{
		hits:         []interfaces.CapabilityHit{hit("box-1", "t1")},
		toolsByBox:   map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
		boxStatusErr: map[string]error{"box-1": errors.New("execution factory unreachable")},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 0 {
		t.Fatalf("状态核不到的不能当可用, got %+v", resp.Capabilities)
	}
	// The two empty-result messages point at opposite fixes: one at the owner's publication
	// state, the other at the caller's permission. This must be the former.
	if !strings.Contains(resp.Message, "发布") && !strings.Contains(resp.Message, "publish") {
		t.Fatalf("该说清是发布状态问题, got %q", resp.Message)
	}
	if strings.Contains(resp.Message, "可见权限") || strings.Contains(resp.Message, "cannot see") {
		t.Fatalf("不是可见权限问题,不该用那条文案, got %q", resp.Message)
	}
}

func TestWithdrawnMCPServerIsNotOfferedButAnUnreachableOneIs(t *testing.T) {
	op := &fakeOperator{
		hits:        []interfaces.CapabilityHit{mcpHit("mcp-off", "a"), mcpHit("mcp-quiet", "b")},
		mcpUnusable: map[string]bool{"mcp-off": true},
		// mcp-quiet is published but answers no detail: runtime health, not lifecycle.
		mcpTools: map[string]*interfaces.GetMCPToolDetailResponse{},
	}
	bkn := &fakeBkn{refs: append(mcpRefs("mcp-off", "a"), mcpRefs("mcp-quiet", "b")...)}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].OwnerID != "mcp-quiet" {
		t.Fatalf("下线的 server 不该出现;已发布但暂时没应答的该保留, got %+v", resp.Capabilities)
	}
}

func TestOneBoundedRefillAfterWithdrawal(t *testing.T) {
	// limit=2. First page: two hits, both from a withdrawn box, and more behind. The refill asks
	// once for limit*3 (+1 probe) and finds the live ones.
	first := []interfaces.CapabilityHit{hit("box-gone", "t1"), hit("box-gone", "t2"), hit("box-live", "t3")}
	second := []interfaces.CapabilityHit{
		hit("box-gone", "t1"), hit("box-gone", "t2"), hit("box-live", "t3"), hit("box-live", "t4"),
	}
	op := &fakeOperator{
		hitsByCall: [][]interfaces.CapabilityHit{first, second},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{
			"box-live": tools("box-live", "t3", "t4"), "box-gone": tools("box-gone", "t1", "t2"),
		},
		boxUnpublished: map[string]bool{"box-gone": true},
	}
	bkn := &fakeBkn{refs: functionRefs("box-gone/t1", "box-gone/t2", "box-live/t3", "box-live/t4")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x", Limit: 2})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if op.searchCalls != 2 {
		t.Fatalf("剔除后不足该补召回一次,且只一次, got %d calls", op.searchCalls)
	}
	if op.gotTopKs[1] != 2*refillFactor+1 {
		t.Fatalf("补召回宽度该是 limit*%d+1, got %v", refillFactor, op.gotTopKs)
	}
	if len(resp.Capabilities) != 2 || resp.Capabilities[0].CapabilityID != "t3" || resp.Capabilities[1].CapabilityID != "t4" {
		t.Fatalf("补召回后该拿满 limit 个在线工具, got %+v", resp.Capabilities)
	}
}

func TestRefillStillShortIsSaidNotHidden(t *testing.T) {
	// Even the wider page is all withdrawn except one, and the ranking has more: say incomplete.
	page := []interfaces.CapabilityHit{hit("box-gone", "t1"), hit("box-gone", "t2"), hit("box-live", "t3")}
	wider := []interfaces.CapabilityHit{
		hit("box-gone", "t1"), hit("box-gone", "t2"), hit("box-live", "t3"),
		hit("box-gone", "t5"), hit("box-gone", "t6"), hit("box-gone", "t7"), hit("box-gone", "t8"),
	}
	op := &fakeOperator{
		hitsByCall:     [][]interfaces.CapabilityHit{page, wider},
		toolsByBox:     map[string]*interfaces.ListPublishedToolsResponse{"box-live": tools("box-live", "t3")},
		boxUnpublished: map[string]bool{"box-gone": true},
	}
	bkn := &fakeBkn{refs: functionRefs("box-gone/t1", "box-gone/t2", "box-live/t3", "box-gone/t5", "box-gone/t6", "box-gone/t7", "box-gone/t8")}
	svc := NewKnToolsServiceWith(op, bkn, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x", Limit: 2})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if op.searchCalls != 2 {
		t.Fatalf("有界:只补一次, got %d", op.searchCalls)
	}
	if len(resp.Capabilities) != 1 || !resp.Truncated {
		t.Fatalf("仍不足时该返回能拿到的并标记不完整, got n=%d truncated=%v", len(resp.Capabilities), resp.Truncated)
	}
	if !strings.Contains(resp.Message, "不完整") && !strings.Contains(resp.Message, "incomplete") {
		t.Fatalf("该明说结果不完整, got %q", resp.Message)
	}
}

func TestNoRefillWhenNothingWasWithdrawn(t *testing.T) {
	op := &fakeOperator{
		hits:       []interfaces.CapabilityHit{hit("box-1", "t1"), hit("box-1", "t2"), hit("box-1", "t3")},
		toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1", "t2", "t3")},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t1", "box-1/t2", "box-1/t3")}, &fakeKnAuthz{})
	if _, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x", Limit: 2}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if op.searchCalls != 1 {
		t.Fatalf("没有剔除就不该补召回, got %d", op.searchCalls)
	}
}

// TestDisabledToolInAPublishedBoxIsNotOffered is the internal-face case: there the caller-visible
// listing cannot be read (no caller token), so tool-level enablement has to come from the
// execution factory's own records, or a disabled tool in a published box would be offered.
func TestDisabledToolInAPublishedBoxIsNotOffered(t *testing.T) {
	op := &fakeOperator{
		hits:             []interfaces.CapabilityHit{hit("box-1", "t-on"), hit("box-1", "t-off")},
		toolsByBox:       map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t-on", "t-off")},
		boxDisabledTools: map[string]map[string]bool{"box-1": {"t-off": true}},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-1/t-on", "box-1/t-off")}, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].CapabilityID != "t-on" {
		t.Fatalf("停用的工具不该出现,同箱启用的照常, got %+v", resp.Capabilities)
	}
}

// TestUnknownEnablementIsNotReadAsDisabled covers a box larger than the bounded enabled-tools
// walk. The set that came back is a prefix; a tool outside it must be kept, not withheld — the box
// is confirmed published and the index only admits enabled tools, so silently dropping the tail of
// a large box would be a defect of the gate, not a safety margin.
func TestUnknownEnablementIsNotReadAsDisabled(t *testing.T) {
	op := &fakeOperator{
		hits:              []interfaces.CapabilityHit{hit("box-big", "t-beyond")},
		toolsByBox:        map[string]*interfaces.ListPublishedToolsResponse{"box-big": tools("box-big", "t-beyond")},
		boxDisabledTools:  map[string]map[string]bool{"box-big": {"t-beyond": true}}, // absent from the prefix
		boxEnabledUnknown: map[string]bool{"box-big": true},
	}
	svc := NewKnToolsServiceWith(op, &fakeBkn{refs: functionRefs("box-big/t-beyond")}, &fakeKnAuthz{})

	resp, err := svc.SearchCapabilities(context.Background(), &SearchCapabilitiesReq{KnID: "kn1", Query: "x"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Capabilities) != 1 {
		t.Fatalf("启用集只是前缀时,不在前缀里的工具该保留而不是当停用, got %+v", resp.Capabilities)
	}
}
