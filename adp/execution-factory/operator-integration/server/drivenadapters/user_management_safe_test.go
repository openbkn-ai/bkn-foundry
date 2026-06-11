package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

type testLogger struct{}

func (testLogger) Debug(...interface{})                             {}
func (testLogger) Info(...interface{})                              {}
func (testLogger) Warn(...interface{})                              {}
func (testLogger) Error(...interface{})                             {}
func (testLogger) Debugf(string, ...interface{})                    {}
func (testLogger) Infof(string, ...interface{})                     {}
func (testLogger) Warnf(string, ...interface{})                     {}
func (testLogger) Errorf(string, ...interface{})                    {}
func (l testLogger) WithContext(context.Context) interfaces.Logger  { return l }

// fakeDirectory serves the two bkn-safe directory endpoints the adapter uses,
// with one known user (u1/Alice) and one known app (app1/MES).
func fakeDirectory(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/directory/users-detail", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserIDs []string `json:"user_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		users := []map[string]any{}
		for _, id := range req.UserIDs {
			if id == "u1" {
				users = append(users, map[string]any{
					"id": "u1", "name": "Alice", "account": "alice", "roles": []string{"role-x"},
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
	})
	mux.HandleFunc("/api/safe/v1/directory/names", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AppIDs []string `json:"app_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		apps := []map[string]string{}
		for _, id := range req.AppIDs {
			if id == "app1" {
				apps = append(apps, map[string]string{"id": "app1", "name": "MES"})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"app_names": apps})
	})
	return httptest.NewServer(mux)
}

func TestSafeUserManagement(t *testing.T) {
	srv := fakeDirectory(t)
	defer srv.Close()
	ctx := context.Background()
	s := newSafeUserManagement(srv.URL, testLogger{})

	t.Run("GetUsersInfo returns only existing users", func(t *testing.T) {
		infos, err := s.GetUsersInfo(ctx, []string{"u1", "ghost"}, nil)
		if err != nil {
			t.Fatalf("GetUsersInfo: %v", err)
		}
		if len(infos) != 1 || infos[0].UserID != "u1" || infos[0].DisplayName != "Alice" ||
			infos[0].Account != "alice" || len(infos[0].Roles) != 1 {
			t.Fatalf("infos = %+v", infos)
		}
	})

	t.Run("GetUserInfo errors on missing user", func(t *testing.T) {
		if _, err := s.GetUserInfo(ctx, "ghost"); err == nil {
			t.Fatal("expected error for missing user")
		}
		info, err := s.GetUserInfo(ctx, "u1")
		if err != nil || info.DisplayName != "Alice" {
			t.Fatalf("info = %+v err = %v", info, err)
		}
	})

	t.Run("GetUsersName degrades missing ids and keeps SystemUser", func(t *testing.T) {
		m, err := s.GetUsersName(ctx, []string{"u1", "ghost", interfaces.SystemUser})
		if err != nil {
			t.Fatalf("GetUsersName: %v", err)
		}
		if m["u1"] != "Alice" || m["ghost"] != interfaces.UnknownUser || m[interfaces.SystemUser] != interfaces.SystemUser {
			t.Fatalf("map = %v", m)
		}
	})

	t.Run("GetAppInfo resolves name with id fallback", func(t *testing.T) {
		a, err := s.GetAppInfo(ctx, "app1")
		if err != nil || a.Name != "MES" {
			t.Fatalf("app = %+v err = %v", a, err)
		}
		a, err = s.GetAppInfo(ctx, "unknown-app")
		if err != nil || a.Name != "unknown-app" {
			t.Fatalf("fallback app = %+v err = %v", a, err)
		}
	})
}

func TestSelectUserManagement(t *testing.T) {
	isf := func() interfaces.UserManagement { return &noopUserManagementClient{} }

	t.Setenv("AUTHZ_PROVIDER", "")
	if _, ok := selectUserManagement(isf, testLogger{}).(*noopUserManagementClient); !ok {
		t.Fatal("unset provider should keep ISF client")
	}

	t.Setenv("AUTHZ_PROVIDER", "bkn-safe")
	t.Setenv("BKN_SAFE_URL", "")
	if _, ok := selectUserManagement(isf, testLogger{}).(*noopUserManagementClient); !ok {
		t.Fatal("empty BKN_SAFE_URL should fall back to ISF client")
	}

	t.Setenv("BKN_SAFE_URL", "http://bkn-safe:3000")
	if _, ok := selectUserManagement(isf, testLogger{}).(*safeUserManagement); !ok {
		t.Fatal("bkn-safe provider should select the directory adapter")
	}
}
