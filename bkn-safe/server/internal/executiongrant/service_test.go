// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package executiongrant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

// permissions answers whether owner may view network kn-1, as Casbin would.
type permissions struct {
	allowed bool
	failed  bool
	calls   int
}

func (p *permissions) CheckResourceOpsScoped(_ context.Context, actor string, checks []authz.ResourceOperationCheck, scope authz.EvaluationScope) ([]authz.FilteredResource, error) {
	p.calls++
	if p.failed {
		return nil, errors.New("dependency unavailable")
	}
	if len(checks) != 1 || checks[0].Resource.Type != "knowledge_network" || len(checks[0].Operations) != 1 ||
		checks[0].Operations[0] != "view_detail" || scope != authz.ScopeEffective {
		return nil, errors.New("wrong permission check")
	}
	decision := authz.DecisionDeny
	if p.allowed && actor == "owner" && checks[0].Resource.ID == "kn-1" {
		decision = authz.DecisionAllow
	}
	return []authz.FilteredResource{{Type: "knowledge_network", ID: checks[0].Resource.ID,
		Decisions: []authz.OperationDecision{{Operation: "view_detail", Decision: decision}}}}, nil
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func fixture(t *testing.T) (*Service, *gorm.DB, *permissions, *clock, IssueRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Account: "owner", Name: "Owner", Enabled: true, AccountType: model.AccountTypeOther}).Error; err != nil {
		t.Fatal(err)
	}
	p, c := &permissions{allowed: true}, &clock{at: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	s := New(db, p)
	s.now = c.now
	return s, db, p, c, IssueRequest{ID: "0195-run-1", ProjectID: "project-1", RunID: "0195-run-1", DelegatorID: "owner",
		KnID: "kn-1", AgentID: "studio_smart_qa", TTLSeconds: 3600}
}

func TestACredentialActsForItsDelegatorWithinItsScope(t *testing.T) {
	s, db, _, _, r := fixture(t)
	issued, err := s.Issue(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Credential, "bex_0195-run-1_") || issued.ExpiresAt != time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) {
		t.Fatalf("issued %+v", issued)
	}
	v, err := s.Verify(t.Context(), issued.Credential)
	if err != nil {
		t.Fatal(err)
	}
	want := Verified{GrantID: "0195-run-1", DelegatorID: "owner", Name: "Owner", AccountType: "other", ProjectID: "project-1",
		RunID: "0195-run-1", KnID: "kn-1", AgentID: "studio_smart_qa"}
	if *v != want {
		t.Fatalf("verified %+v", v)
	}
	// Only the secret's hash is kept.
	var g model.ExecutionGrant
	if err = db.First(&g, "id = ?", r.ID).Error; err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimPrefix(issued.Credential, "bex_0195-run-1_")
	if g.SecretHash == secret || strings.Contains(g.SecretHash, secret) || g.SecretHash != hash(secret) {
		t.Fatal("the secret is stored")
	}
}

func TestACredentialIsRefusedOnceItsDelegatorMayNoLonger(t *testing.T) {
	s, db, p, c, r := fixture(t)
	issued, err := s.Issue(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	// Permission taken back: refused at the next use, not at the next issue.
	p.allowed = false
	if _, err = s.Verify(t.Context(), issued.Credential); !errors.Is(err, ErrForbidden) {
		t.Fatal("verified without permission", err)
	}
	p.allowed = true
	// A delegator disabled.
	if err = db.Model(&model.User{}).Where("id = ?", "owner").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(t.Context(), issued.Credential); !errors.Is(err, ErrForbidden) {
		t.Fatal("verified for a disabled delegator", err)
	}
	if err = db.Model(&model.User{}).Where("id = ?", "owner").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(t.Context(), issued.Credential); err != nil {
		t.Fatal(err)
	}
	// The authorization dependency down: not verified.
	p.failed = true
	if _, err = s.Verify(t.Context(), issued.Credential); err == nil {
		t.Fatal("verified without a decision")
	}
	p.failed = false
	// Expired.
	c.at = c.at.Add(time.Hour)
	if _, err = s.Verify(t.Context(), issued.Credential); !errors.Is(err, ErrForbidden) {
		t.Fatal("verified after expiry", err)
	}
}

func TestOnlyTheIssuedSecretVerifies(t *testing.T) {
	s, _, _, _, r := fixture(t)
	issued, err := s.Issue(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", "bak_0195-run-1_x", "bex_", "bex_0195-run-1", "bex_0195-run-1_wrong",
		"bex_other_" + strings.TrimPrefix(issued.Credential, "bex_0195-run-1_"), strings.TrimPrefix(issued.Credential, "bex_")} {
		if _, err = s.Verify(t.Context(), credential); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%q verified: %v", credential, err)
		}
	}
}

func TestIssueChecksTheDelegatorAndTheRequest(t *testing.T) {
	s, db, p, _, r := fixture(t)
	bad := []func(*IssueRequest){
		func(r *IssueRequest) { r.ID = "has_underscore" },
		func(r *IssueRequest) { r.ID = "" },
		func(r *IssueRequest) { r.KnID = "" },
		func(r *IssueRequest) { r.AgentID = "a b" },
		func(r *IssueRequest) { r.TTLSeconds = 0 },
		func(r *IssueRequest) { r.TTLSeconds = int64(MaxTTL/time.Second) + 1 },
	}
	for i, change := range bad {
		req := r
		change(&req)
		if _, err := s.Issue(t.Context(), req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	p.allowed = false
	if _, err := s.Issue(t.Context(), r); !errors.Is(err, ErrForbidden) {
		t.Fatal("issued without permission", err)
	}
	p.allowed = true
	// Only a person delegates: an application account does not.
	if err := db.Create(&model.User{ID: "app", Account: "app", Enabled: true, AccountType: model.AccountTypeApp}).Error; err != nil {
		t.Fatal(err)
	}
	req := r
	req.DelegatorID = "app"
	if _, err := s.Issue(t.Context(), req); !errors.Is(err, ErrForbidden) {
		t.Fatal("issued for an application", err)
	}
	req.DelegatorID = "nobody"
	if _, err := s.Issue(t.Context(), req); !errors.Is(err, ErrForbidden) {
		t.Fatal("issued for nobody", err)
	}
	// An id is issued once: its credential cannot be shown again.
	if _, err := s.Issue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(t.Context(), r); !errors.Is(err, ErrConflict) {
		t.Fatal("issued twice", err)
	}
	// A run has one grant: a new id for the same run is refused too.
	again := r
	again.ID = "0195-run-1b"
	if _, err := s.Issue(t.Context(), again); !errors.Is(err, ErrConflict) {
		t.Fatal("issued a second grant for one run", err)
	}
}

func TestRevokeEndsACredentialAndFencesALateIssue(t *testing.T) {
	s, db, _, _, r := fixture(t)
	issued, err := s.Issue(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(t.Context(), r.ID, "other-project", r.RunID); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked from another project", err)
	}
	if err = s.Revoke(t.Context(), r.ID, r.ProjectID, r.RunID); err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(t.Context(), r.ID, r.ProjectID, r.RunID); err != nil {
		t.Fatal("revoking twice", err)
	}
	if _, err = s.Verify(t.Context(), issued.Credential); !errors.Is(err, ErrForbidden) {
		t.Fatal("verified after revoke", err)
	}
	var g model.ExecutionGrant
	if err = db.First(&g, "id = ?", r.ID).Error; err != nil || g.SecretHash != "" || g.Status != StatusRevoked {
		t.Fatal("revoked grant kept its secret", g, err)
	}
	// Revoked before the issue arrived: the issue is refused.
	late := r
	late.ID, late.RunID = "0195-run-2", "0195-run-2"
	if err = s.Revoke(t.Context(), late.ID, late.ProjectID, late.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Issue(t.Context(), late); !errors.Is(err, ErrConflict) {
		t.Fatal("a late issue restored a revoked grant", err)
	}
}
