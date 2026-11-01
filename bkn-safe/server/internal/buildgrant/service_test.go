// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package buildgrant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

type permissions struct {
	deniedKB string
	allowed  bool
	failed   bool
	calls    int
}

func (p *permissions) CheckResourceOpsScoped(_ context.Context, actor string, checks []authz.ResourceOperationCheck, scope authz.EvaluationScope) ([]authz.FilteredResource, error) {
	p.calls++
	if actor != "owner" || scope != authz.ScopeEffective {
		return nil, errors.New("wrong permission target")
	}
	if p.failed {
		return nil, errors.New("dependency unavailable")
	}
	result := []authz.FilteredResource{}
	for _, c := range checks {
		if c.Resource.Type != "knowledge_base" || len(c.Operations) != 1 || c.Operations[0] != "query_data" {
			return nil, errors.New("wrong permission operation")
		}
		operations := []string{}
		if p.allowed && c.Resource.ID != p.deniedKB {
			operations = append(operations, "query_data")
		}
		result = append(result, authz.FilteredResource{Type: c.Resource.Type, ID: c.Resource.ID, Operations: operations})
	}
	return result, nil
}

func fixture(t *testing.T) (*Service, *gorm.DB, *permissions, CreateRequest) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Account: "owner", Enabled: true, AccountType: model.AccountTypeOther}).Error; err != nil {
		t.Fatal(err)
	}
	check := &permissions{allowed: true}
	req := CreateRequest{ID: "grant-1", ProjectID: "project-1", BuildID: "build-1", DelegatorID: "owner", ModelFingerprint: strings.Repeat("a", 64), Versions: []Version{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8"}, {KnowledgeBaseID: "1", FileID: "42", ContentVersionID: "9"}}}
	return New(db, check), db, check, req
}
func checkFor(g *Grant, r CreateRequest) CheckRequest {
	return CheckRequest{GrantID: g.ID, ProjectID: r.ProjectID, BuildID: r.BuildID, PrincipalID: g.PrincipalID, Revision: g.Revision, Versions: r.Versions, Action: ActionRead}
}
func TestDelegationIsIdempotentScopedAndCredentialFree(t *testing.T) {
	s, db, p, r := fixture(t)
	g, created, err := s.Create(t.Context(), r)
	if err != nil || !created {
		t.Fatal(g, created, err)
	}
	replay, created, err := s.Create(t.Context(), r)
	if err != nil || created || replay.PrincipalID != g.PrincipalID {
		t.Fatal("replay changed identity", err)
	}
	if err = s.Check(t.Context(), checkFor(g, r)); err != nil {
		t.Fatal(err)
	}
	var user model.User
	if err = db.First(&user, "id = ?", g.PrincipalID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.NewAPIKeyStore(db).Issue(t.Context(), g.PrincipalID, "forbidden", nil); !errors.Is(err, managedproxy.ErrManagedAccount) {
		t.Fatal("build identity issued credentials", err)
	}
	if user.AccountType != model.AccountTypeApp || !user.Enabled || user.PasswordHash != "" || user.MustChangePassword {
		t.Fatal("delegation identity can carry credentials")
	}
	if protected, err := managedproxy.IsManaged(t.Context(), db, g.PrincipalID); err != nil || !protected {
		t.Fatal("identity bypassed credential/directory protection", err)
	}
	if _, err = managedproxy.New(db).Get(t.Context(), g.PrincipalID); err == nil {
		t.Fatal("build identity accepted as KN proxy")
	}
	req := checkFor(g, r)
	for _, mutate := range []func(*CheckRequest){func(c *CheckRequest) { c.ProjectID = "other" }, func(c *CheckRequest) { c.BuildID = "other" }, func(c *CheckRequest) { c.PrincipalID = "owner" }, func(c *CheckRequest) { c.Revision++ }, func(c *CheckRequest) {
		c.Versions = []Version{{KnowledgeBaseID: "2", FileID: "41", ContentVersionID: "8"}}
	}, func(c *CheckRequest) {
		c.Versions = []Version{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "99"}}
	}} {
		changed := req
		mutate(&changed)
		if !errors.Is(s.Check(t.Context(), changed), ErrForbidden) {
			t.Fatal("scope escape accepted", changed)
		}
	}
	p.allowed = false
	if !errors.Is(s.Check(t.Context(), req), ErrForbidden) {
		t.Fatal("revocation cached")
	}
	p.failed = true
	if err = s.Check(t.Context(), req); err == nil || errors.Is(err, ErrForbidden) {
		t.Fatal("dependency failure confused with denial", err)
	}
}
func TestRevocationAndCleanupFenceLateCreate(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{true: "release_before_create", false: "existing_grant"}[early], func(t *testing.T) {
			s, db, p, r := fixture(t)
			var g *Grant
			if !early {
				var err error
				g, _, err = s.Create(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Revoke(t.Context(), r.ID, r.ProjectID, r.BuildID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Create(t.Context(), r); !errors.Is(err, ErrConflict) {
				t.Fatal("late create resurrected grant", err)
			}
			if early {
				return
			}
			if err := s.Check(t.Context(), checkFor(g, r)); !errors.Is(err, ErrForbidden) {
				t.Fatal("revoked grant can read", err)
			}
			p.allowed = false
			cleanup := checkFor(g, r)
			cleanup.Action = ActionCleanup
			cleanup.Versions = nil
			if err := s.Check(t.Context(), cleanup); err != nil {
				t.Fatal("cleanup requires revoked read permission", err)
			}
			if err := s.CompleteCleanup(t.Context(), r.ID, r.ProjectID, r.BuildID); err != nil {
				t.Fatal(err)
			}
			if err := s.Check(t.Context(), cleanup); !errors.Is(err, ErrForbidden) {
				t.Fatal("cleaned identity still usable", err)
			}
			var u model.User
			db.First(&u, "id = ?", g.PrincipalID)
			if u.Enabled {
				t.Fatal("cleaned identity remains enabled")
			}
		})
	}
}
func TestDisabledDelegatorAndChangedManifestFailClosed(t *testing.T) {
	s, db, _, r := fixture(t)
	g, _, err := s.Create(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	changed := r
	changed.Versions = append([]Version(nil), r.Versions...)
	changed.Versions[0].ContentVersionID = "99"
	if _, _, err = s.Create(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("immutable manifest changed", err)
	}
	db.Model(&model.User{}).Where("id = ?", "owner").Update("enabled", false)
	if err = s.Check(t.Context(), checkFor(g, r)); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled owner retained delegation", err)
	}
}

func TestAnyManifestKnowledgeBaseRevocationStopsTheTask(t *testing.T) {
	s, _, check, req := fixture(t)
	req.Versions = append(req.Versions, Version{KnowledgeBaseID: "2", FileID: "77", ContentVersionID: "10"})
	grant, _, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	check.deniedKB = "2"
	read := checkFor(grant, req)
	read.Versions = req.Versions[:1]
	if !errors.Is(s.Check(t.Context(), read), ErrForbidden) {
		t.Fatal("task continued after one source knowledge base lost permission")
	}
}

func TestCleanupCannotRetireAnUnknownOrActiveGrant(t *testing.T) {
	s, _, _, r := fixture(t)
	if err := s.CompleteCleanup(t.Context(), r.ID, r.ProjectID, r.BuildID); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown grant cleanup must be a state conflict", err)
	}
	if _, _, err := s.Create(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteCleanup(t.Context(), r.ID, r.ProjectID, r.BuildID); !errors.Is(err, ErrConflict) {
		t.Fatal("active identity retired before revocation", err)
	}
}

func TestProjectsMayReuseLocalBuildNames(t *testing.T) {
	s, _, _, req := fixture(t)
	first, _, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.ID = "grant-2"
	req.ProjectID = "project-2"
	second, created, err := s.Create(t.Context(), req)
	if err != nil || !created || second.PrincipalID == first.PrincipalID {
		t.Fatal("different projects collided on a local build name", err)
	}
	if err = s.Check(t.Context(), checkFor(second, req)); err != nil {
		t.Fatal("second project's scope rejected", err)
	}
}

func TestPermissionChecksUseOneCurrentBatchPerRequest(t *testing.T) {
	s, _, permissions, req := fixture(t)
	first, _, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	before := permissions.calls
	if err = s.Check(t.Context(), checkFor(first, req)); err != nil {
		t.Fatal(err)
	}
	if permissions.calls != before+1 {
		t.Fatal("read did not use one current batched permission evaluation")
	}
	if err = s.Check(t.Context(), checkFor(first, req)); err != nil {
		t.Fatal(err)
	}
	if permissions.calls != before+2 {
		t.Fatal("permission decision cached across requests")
	}
}

func TestNetworkProxyLifecycleCannotMutateABuildIdentity(t *testing.T) {
	s, _, _, req := fixture(t)
	grant, _, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	proxies := managedproxy.New(s.db)
	for _, operation := range []func(context.Context, string) (*managedproxy.Account, error){proxies.Get, proxies.Disable, proxies.Archive, proxies.Restore} {
		if _, err = operation(t.Context(), grant.PrincipalID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatal("KN lifecycle accepted a build identity", err)
		}
		if err = s.Check(t.Context(), checkFor(grant, req)); err != nil {
			t.Fatal("wrong lifecycle endpoint changed a build identity", err)
		}
	}
}
