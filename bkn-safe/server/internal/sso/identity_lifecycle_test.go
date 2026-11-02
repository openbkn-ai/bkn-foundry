package sso

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func identityFixture(t *testing.T) (*Service, *model.SSOProvider, map[string]any) {
	return identityFixtureWithAPI(t, nil)
}

func identityFixtureWithAPI(t *testing.T, api http.HandlerFunc) (*Service, *model.SSOProvider, map[string]any) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{"sub": "user1-subject", "name": "user1", "groups": []string{"organization_openbkn/department-1"}}
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/discovery":
			_ = json.NewEncoder(w).Encode(metadata{Issuer: baseURL, Authorize: baseURL + "/authorize",
				Token: baseURL + "/token", UserInfo: baseURL + "/userinfo", Introspect: baseURL + "/introspect", JWKS: baseURL + "/jwks"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer user-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(claims)
		default:
			if api != nil {
				api(w, r)
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	t.Cleanup(srv.Close)
	baseURL = srv.URL
	provider := &model.SSOProvider{ID: "casdoor", Issuer: baseURL, DiscoveryURL: baseURL + "/discovery",
		DepartmentClaim: "groups", AllowInsecureHTTP: true}
	return New(db, srv.Client()), provider, claims
}

func TestResolveUserAfterDeleteRecreatesIdentityAndGroups(t *testing.T) {
	s, provider, _ := identityFixture(t)
	ctx := t.Context()
	first, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatal(err)
	}
	other := model.User{ID: "other-user", Account: "other-user", Enabled: true, Source: model.SourceSSO}
	if err := s.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	otherIdentity := model.SSOIdentity{ID: "other-identity", ProviderID: provider.ID,
		Issuer: provider.Issuer, Subject: "other-subject", UserID: other.ID}
	if err := s.db.Create(&otherIdentity).Error; err != nil {
		t.Fatal(err)
	}
	if err := auth.NewUserStore(s.db).DeleteUser(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := s.db.Model(&model.SSOIdentity{}).Where("user_id = ?", first.ID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("deleted user's SSO identity remains: %d", remaining)
	}
	if err := s.db.First(&model.SSOIdentity{}, "id = ?", otherIdentity.ID).Error; err != nil {
		t.Fatalf("another user's identity was removed: %v", err)
	}
	second, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatalf("SSO login after deleting user: %v", err)
	}
	if first.ID == second.ID || first.Account != second.Account {
		t.Fatalf("expected a fresh user ID with stable account: first=%s second=%s", first.ID, second.ID)
	}
	var membership model.UserDepartment
	if err := s.db.First(&membership, "user_id = ?", second.ID).Error; err != nil {
		t.Fatalf("group membership not recreated: %v", err)
	}
	var dept model.Department
	if err := s.db.First(&dept, "id = ?", membership.DepartmentID).Error; err != nil {
		t.Fatal(err)
	}
	if dept.Name != "organization_openbkn/department-1" {
		t.Fatalf("unexpected department: %q", dept.Name)
	}
}

func TestResolveUserSyncGroupsPreservesLocalMembership(t *testing.T) {
	s, provider, claims := identityFixture(t)
	ctx := t.Context()
	u, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.Department{ID: "local-dept", Name: "Local department"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.UserDepartment{UserID: u.ID, DepartmentID: "local-dept"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		groups  any
		present bool
		want    int64
	}{
		{"changed groups", []string{"organization_openbkn/department-2", "organization_openbkn/department-2"}, true, 2},
		{"missing groups preserves memberships", nil, false, 2},
		{"empty groups clears only provider memberships", []string{}, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delete(claims, "groups")
			if tc.present {
				claims["groups"] = tc.groups
			}
			if _, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject"); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := s.db.Model(&model.UserDepartment{}).Where("user_id = ?", u.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != tc.want {
				t.Fatalf("membership count = %d, want %d", count, tc.want)
			}
			if err := s.db.First(&model.UserDepartment{}, "user_id = ? AND department_id = ?", u.ID, "local-dept").Error; err != nil {
				t.Fatalf("local membership changed: %v", err)
			}
		})
	}
}

func TestResolveUserDoesNotRecreateDisabledUser(t *testing.T) {
	s, provider, _ := identityFixture(t)
	ctx := t.Context()
	u, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(u).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveUser(ctx, provider, "user-token", "user1-subject"); !errors.Is(err, auth.ErrUserDisabled) {
		t.Fatalf("disabled user must remain rejected: %v", err)
	}
}
