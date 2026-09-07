// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knfindskills

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeBkn answers the capability listing and nothing else.
type fakeBkn struct {
	interfaces.BknBackendAccess

	refs []*interfaces.CapabilityRef
	err  error

	gotKN   string
	gotType string
}

func (f *fakeBkn) ListKNCapabilities(_ context.Context, knID, _, capabilityType string,
) ([]*interfaces.CapabilityRef, error) {
	f.gotKN, f.gotType = knID, capabilityType
	return f.refs, f.err
}

// fakeOperator answers the two execution-factory reads this layer makes.
type fakeOperator struct {
	interfaces.DrivenOperatorIntegration

	hits     []interfaces.SkillHit
	hitsErr  error
	names    map[string]string
	namesErr error

	gotWhitelist []string
	gotQuery     string
	nameCalls    int
	gotNameIDs   []string
}

func (f *fakeOperator) SearchBoundSkills(_ context.Context,
	req *interfaces.SearchBoundSkillsRequest) ([]interfaces.SkillHit, error) {
	f.gotWhitelist = req.SkillIDs
	f.gotQuery = req.Query
	return f.hits, f.hitsErr
}

func (f *fakeOperator) GetSkillNamesByIDs(_ context.Context, ids []string) (map[string]string, error) {
	f.nameCalls++
	f.gotNameIDs = ids
	return f.names, f.namesErr
}

func newService(bkn *fakeBkn, op *fakeOperator) interfaces.IFindSkillsService {
	return newServiceWithAuthz(bkn, op, &fakeKnAuthz{})
}

func newServiceWithAuthz(bkn *fakeBkn, op *fakeOperator,
	authz *fakeKnAuthz) interfaces.IFindSkillsService {
	cfg := &config.Config{}
	cfg.FindSkills.TotalTimeoutMs = 5000
	return NewFindSkillsServiceWith(testLogger{}, cfg, bkn, op, authz)
}

// fakeKnAuthz stands in for the per-caller knowledge-network check.
type fakeKnAuthz struct {
	err     error
	gotKNID string
	calls   int
}

func (f *fakeKnAuthz) AuthorizeRead(_ context.Context, knID string) error {
	f.calls++
	f.gotKNID = knID
	return f.err
}

func skillRefs(ids ...string) []*interfaces.CapabilityRef {
	refs := make([]*interfaces.CapabilityRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, &interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill,
			CapabilityID:   id,
		})
	}
	return refs
}

// TestUnboundNetworkRecallsNothing is the whole point of the switch. The object-type path treated
// "nothing is configured" as "the whole network is in scope", which made an unconfigured network
// the most permissive one.
func TestUnboundNetworkRecallsNothing(t *testing.T) {
	bkn := &fakeBkn{refs: []*interfaces.CapabilityRef{}}
	op := &fakeOperator{}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1", SkillQuery: "交期"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(resp.Entries))
	}
	if resp.Message == "" {
		t.Fatal("an empty result must say why")
	}
	if op.gotWhitelist != nil {
		t.Fatal("an unbound network must not reach the execution factory at all")
	}
	if bkn.gotType != interfaces.CapabilityTypeSkill {
		t.Fatalf("expected the listing to be narrowed to skills, got %q", bkn.gotType)
	}
}

// TestBindingListFailureFailsTheCall pins the fail-closed direction. Degrading to an unfiltered
// listing would hand a network every Skill on the platform exactly when the service cannot tell
// which ones it may show.
func TestBindingListFailureFailsTheCall(t *testing.T) {
	bkn := &fakeBkn{err: errors.New("bkn-backend unreachable")}
	op := &fakeOperator{hits: []interfaces.SkillHit{{SkillID: "s1", Name: "不该出现"}}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"})

	if err == nil {
		t.Fatalf("expected the call to fail, got %+v", resp)
	}
	if op.gotWhitelist != nil {
		t.Fatal("a failed binding lookup must not fall through to a search")
	}
}

// TestQueryPassesTheWhitelistAndKeepsRankOrder checks that the bound ids are the pre-filter and
// that the ranked order coming back is the answer.
func TestQueryPassesTheWhitelistAndKeepsRankOrder(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "s2", "s3")}
	op := &fakeOperator{hits: []interfaces.SkillHit{
		{SkillID: "s3", Name: "第三", Description: "d3", Score: 9},
		{SkillID: "s1", Name: "第一", Description: "d1", Score: 4},
	}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1", SkillQuery: "交期", TopK: 5})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.gotWhitelist) != 3 {
		t.Fatalf("expected all three bound ids in the whitelist, got %v", op.gotWhitelist)
	}
	if op.gotQuery != "交期" {
		t.Fatalf("expected the query to travel, got %q", op.gotQuery)
	}
	if len(resp.Entries) != 2 || resp.Entries[0].SkillID != "s3" || resp.Entries[1].SkillID != "s1" {
		t.Fatalf("expected ranked order s3,s1, got %+v", resp.Entries)
	}
	if resp.Entries[0].Description != "d3" {
		t.Fatal("description must travel with the hit")
	}
}

// TestNoQueryKeepsBindingOrder pins the order of a plain listing to what the network declared.
// The index order is neither stable nor explainable, and a listing has nothing to rank by.
func TestNoQueryKeepsBindingOrder(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "s2", "s3")}
	op := &fakeOperator{hits: []interfaces.SkillHit{
		{SkillID: "s3", Name: "第三"},
		{SkillID: "s1", Name: "第一"},
		{SkillID: "s2", Name: "第二"},
	}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	got := []string{}
	for _, entry := range resp.Entries {
		got = append(got, entry.SkillID)
	}
	if len(got) != 3 || got[0] != "s1" || got[1] != "s2" || got[2] != "s3" {
		t.Fatalf("expected binding order s1,s2,s3, got %v", got)
	}
}

// TestMissingIndexFallsBackToTheRegistry covers the failure this fallback exists for: the skill
// index is built asynchronously and a fresh install has none, which used to make every bound Skill
// disappear from find_skills with nothing to explain it.
func TestMissingIndexFallsBackToTheRegistry(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "s2")}
	op := &fakeOperator{
		hits:  []interfaces.SkillHit{},
		names: map[string]string{"s1": "第一", "s2": "第二"},
	}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected both bound skills listed by name, got %+v", resp.Entries)
	}
	if resp.Entries[0].Name != "第一" || resp.Entries[1].Name != "第二" {
		t.Fatalf("expected registry names, got %+v", resp.Entries)
	}
	if op.nameCalls != 1 {
		t.Fatalf("expected one batched name lookup, got %d", op.nameCalls)
	}
}

// TestSkillGoneFromFactoryIsDropped keeps a binding whose target no longer exists out of the
// answer: reporting it would send the caller after a Skill that cannot run.
func TestSkillGoneFromFactoryIsDropped(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "gone")}
	op := &fakeOperator{
		hits:  []interfaces.SkillHit{{SkillID: "s1", Name: "第一"}},
		names: map[string]string{},
	}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].SkillID != "s1" {
		t.Fatalf("expected only the live skill, got %+v", resp.Entries)
	}
}

// TestObjectTypeIDIsAcceptedAndIgnored keeps the MCP contract working for callers that still send
// it, without inventing an object-type scope the bindings do not carry.
func TestObjectTypeIDIsAcceptedAndIgnored(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1")}
	op := &fakeOperator{hits: []interfaces.SkillHit{{SkillID: "s1", Name: "第一"}}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1", ObjectTypeID: "whatever_does_not_exist"})

	if err != nil {
		t.Fatalf("an ignored object_type_id must not fail the call, got %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("expected the bound skill, got %+v", resp.Entries)
	}
}

// TestTopKBoundsAListing checks the cap applies before the metadata is fetched, so a network with
// hundreds of bound Skills does not pay for names it will not return.
func TestTopKBoundsAListing(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "s2", "s3", "s4")}
	op := &fakeOperator{hits: []interfaces.SkillHit{}, names: map[string]string{
		"s1": "一", "s2": "二", "s3": "三", "s4": "四",
	}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1", TopK: 2})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(resp.Entries))
	}
	if len(op.gotNameIDs) != 2 {
		t.Fatalf("expected the name lookup to be capped too, got %v", op.gotNameIDs)
	}
}

// TestDuplicateBindingsCollapse guards the whitelist against a duplicated id, which would
// otherwise be sent twice and listed twice.
func TestDuplicateBindingsCollapse(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1", "s1", "s2")}
	op := &fakeOperator{hits: []interfaces.SkillHit{
		{SkillID: "s1", Name: "第一"}, {SkillID: "s2", Name: "第二"},
	}}

	resp, err := newService(bkn, op).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(op.gotWhitelist) != 2 {
		t.Fatalf("expected a deduplicated whitelist, got %v", op.gotWhitelist)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(resp.Entries))
	}
}

// testLogger swallows output; these cases assert on returned values, not on logs.
type testLogger struct{}

func (testLogger) Debug(...interface{})                            {}
func (testLogger) Info(...interface{})                             {}
func (testLogger) Warn(...interface{})                             {}
func (testLogger) Error(...interface{})                            {}
func (testLogger) Debugf(string, ...interface{})                   {}
func (testLogger) Infof(string, ...interface{})                    {}
func (testLogger) Warnf(string, ...interface{})                    {}
func (testLogger) Errorf(string, ...interface{})                   {}
func (l testLogger) WithContext(context.Context) interfaces.Logger { return l }

// TestUnauthorizedNetworkIsRefusedBeforeAnythingIsRead is the check the object-type path used to
// get for free: every old route ended in an ontology-query call that authorized the caller. This
// one reads bindings and the execution factory, so a missing check would scope the answer by
// nothing but the kn_id the caller typed.
func TestUnauthorizedNetworkIsRefusedBeforeAnythingIsRead(t *testing.T) {
	bkn := &fakeBkn{refs: skillRefs("s1")}
	op := &fakeOperator{hits: []interfaces.SkillHit{{SkillID: "s1", Name: "不该看到"}}}
	authz := &fakeKnAuthz{err: errors.New("forbidden")}

	_, err := newServiceWithAuthz(bkn, op, authz).FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "someone-elses-kn"})

	if err == nil {
		t.Fatal("expected an unauthorized network to be refused")
	}
	if bkn.gotKN != "" {
		t.Fatal("nothing may be read before the caller is authorized")
	}
	if op.gotWhitelist != nil {
		t.Fatal("the execution factory must not be reached either")
	}
	if authz.gotKNID != "someone-elses-kn" {
		t.Fatalf("expected the requested kn_id to be checked, got %q", authz.gotKNID)
	}
}

// TestMissingAuthorizerFailsClosed keeps a service that was wired without the check from
// answering as though the check had passed.
func TestMissingAuthorizerFailsClosed(t *testing.T) {
	cfg := &config.Config{}
	cfg.FindSkills.TotalTimeoutMs = 5000
	svc := NewFindSkillsServiceWith(testLogger{}, cfg, &fakeBkn{refs: skillRefs("s1")},
		&fakeOperator{}, nil)

	if _, err := svc.FindSkills(context.Background(),
		&interfaces.FindSkillsReq{KnID: "kn1"}); err == nil {
		t.Fatal("expected a service without an authorizer to refuse")
	}
}
