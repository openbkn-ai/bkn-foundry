package umcmp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/umcmp/dto/umarg"
)

// newSafeUm builds a Um wired to a fake bkn-safe directory server.
func newSafeUm(t *testing.T, h http.Handler) *Um {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Um{directoryProvider: "bkn-safe", bknSafeURL: srv.URL}
}

func writeJSON(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func TestSafe_GetOsnNames(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/directory/names", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"user_names":       []map[string]string{{"id": "u1", "name": "Alice"}},
			"app_names":        []map[string]string{{"id": "a1", "name": "App"}},
			"department_names": []map[string]string{{"id": "d1", "name": "研发"}},
			"group_names":      []map[string]string{},
		})
	})
	um := newSafeUm(t, mux)

	ret, err := um.GetOsnNames(context.Background(), &umarg.GetOsnArgDto{UserIDs: []string{"u1"}, AppIDs: []string{"a1"}})
	if err != nil {
		t.Fatal(err)
	}
	if ret.UserNameMap["u1"] != "Alice" || ret.AppNameMap["a1"] != "App" || ret.DepartmentNameMap["d1"] != "研发" {
		t.Fatalf("osn maps = %+v", ret)
	}
}

func TestSafe_GetUserInfoAndDept(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/directory/users-detail", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserIDs []string `json:"user_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		users := []map[string]any{}
		for _, id := range req.UserIDs {
			if id != "u1" {
				continue // unknown ids omitted (clean contract)
			}
			users = append(users, map[string]any{
				"id": "u1", "account": "alice", "name": "Alice", "enabled": true,
				"roles": []string{"role-x"},
				"parent_deps": [][]map[string]string{{
					{"id": "d0", "name": "总部", "type": "department"},
					{"id": "d1", "name": "研发", "type": "department"},
				}},
				"groups": []map[string]string{{"id": "g1", "name": "组", "notes": "n"}},
			})
		}
		writeJSON(w, map[string]any{"users": users})
	})
	um := newSafeUm(t, mux)
	ctx := context.Background()

	uim, err := um.GetUserInfo(ctx, &umarg.GetUserInfoArgDto{UserIds: []string{"u1"}})
	if err != nil {
		t.Fatal(err)
	}
	ui := uim["u1"]
	if ui == nil || ui.Name != "Alice" || ui.Account != "alice" || !ui.Enabled {
		t.Fatalf("user = %+v", ui)
	}
	if len(ui.Roles) != 1 || ui.Roles[0] != "role-x" {
		t.Fatalf("roles = %v", ui.Roles)
	}
	if len(ui.ParentDeps) != 1 || len(ui.ParentDeps[0]) != 2 || ui.ParentDeps[0][1].ID != "d1" {
		t.Fatalf("parent_deps = %+v", ui.ParentDeps)
	}
	if len(ui.Groups) != 1 || ui.Groups[0].ID != "g1" {
		t.Fatalf("groups = %+v", ui.Groups)
	}

	// GetUserDept reads the same parent_deps via users-detail.
	depts, err := um.GetUserDept(ctx, "u1")
	if err != nil || len(depts) != 1 || depts[0][0].ID != "d0" {
		t.Fatalf("GetUserDept = %+v err=%v", depts, err)
	}

	// missing user -> empty UserInfoMap, no error.
	uim2, _ := um.GetUserInfo(ctx, &umarg.GetUserInfoArgDto{UserIds: []string{"ghost"}})
	if _, ok := uim2["u1"]; ok {
		t.Fatal("ghost lookup should not contain u1")
	}
}

func TestSafe_DeptIDsAndInfo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/directory/users/u1/department-ids", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"department_ids": []string{"d0", "d1", "d2"}})
	})
	mux.HandleFunc("/api/safe/v1/directory/departments-detail", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"departments": []map[string]any{{
			"id": "d1", "name": "研发",
			"parent_deps": []map[string]string{
				{"id": "d0", "name": "总部", "type": "department"},
				{"id": "d1", "name": "研发", "type": "department"},
			},
		}}})
	})
	um := newSafeUm(t, mux)
	ctx := context.Background()

	ids, err := um.GetUserDeptIDs(ctx, "u1")
	if err != nil || len(ids) != 3 || ids[2] != "d2" {
		t.Fatalf("dept ids = %v err=%v", ids, err)
	}
	dim, err := um.GetDeptInfoMap(ctx, &umarg.GetDeptInfoArgDto{DeptIds: []string{"d1"}})
	if err != nil || dim["d1"] == nil || len(dim["d1"].ParentDeps) != 2 || dim["d1"].ParentDeps[0].ID != "d0" {
		t.Fatalf("dept info = %+v err=%v", dim, err)
	}
}

func TestSafe_SearchOrgAndGroupMembers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/directory/search-org", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user_ids": []string{"u1"}, "department_ids": []string{"d2"}})
	})
	mux.HandleFunc("/api/safe/v1/directory/groups/g1/members", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user_ids": []string{"u1", "u2"}, "department_ids": []string{"d1"}})
	})
	mux.HandleFunc("/api/safe/v1/directory/groups/g2/members", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user_ids": []string{"u2"}, "department_ids": []string{}})
	})
	um := newSafeUm(t, mux)
	ctx := context.Background()

	so, err := um.SearchOrg(ctx, &umarg.SearchOrgArgDto{UserIDs: []string{"u1"}, Scope: []string{"d1"}})
	if err != nil || len(so.UserIDs) != 1 || so.UserIDs[0] != "u1" || len(so.DepartmentIDs) != 1 {
		t.Fatalf("search-org = %+v err=%v", so, err)
	}

	// merge + dedup across two groups (u2 appears twice -> once).
	gm, err := um.GetGroupMembers(ctx, &umarg.GetGroupMembersArgDto{GroupIDs: []string{"g1", "g2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(gm.UserIDs) != 2 || len(gm.DepartmentIDs) != 1 {
		t.Fatalf("group members = %+v", gm)
	}
}
