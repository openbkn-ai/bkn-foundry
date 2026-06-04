package perm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// fakeSafe is a tiny bkn-safe authz server driven by a fixed grant set:
//
//	admin -> data_flow:* all Operations + data_flow:dataflow_page:o11y display
//	u1    -> data_flow:d1 {view,list}, data_flow:d2 {view}
//
// It serves /check, /operations, /resources and records /policies writes.
type fakeSafe struct {
	posted, deleted int
}

func grantsFor(acc string) map[string]map[string]bool {
	switch acc {
	case "admin":
		all := map[string]bool{}
		for _, op := range Operations {
			all[op] = true
		}
		return map[string]map[string]bool{
			"data_flow:*":                 all,
			"data_flow:" + O11yResourceID: {DisplayOperation: true},
		}
	case "u1":
		return map[string]map[string]bool{
			"data_flow:d1": {ViewOperation: true, ListOperation: true},
			"data_flow:d2": {ViewOperation: true},
		}
	}
	return nil
}

// allow resolves a single (acc,type:id,op), honoring the "*" instance pattern.
func allow(acc, key, op string) bool {
	g := grantsFor(acc)
	if ops, ok := g[key]; ok && ops[op] {
		return true
	}
	// type:* pattern
	star := key[:len(key)-len(idOf(key))] + "*"
	if ops, ok := g[star]; ok && ops[op] {
		return true
	}
	return false
}

func idOf(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[i+1:]
		}
	}
	return key
}

func (f *fakeSafe) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/authz/check", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccessorID string                    `json:"accessor_id"`
			Resource   struct{ Type, ID string } `json:"resource"`
			Operation  string                    `json:"operation"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		key := req.Resource.Type + ":" + req.Resource.ID
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": allow(req.AccessorID, key, req.Operation)})
	})
	mux.HandleFunc("/api/safe/v1/authz/operations", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccessorID string                    `json:"accessor_id"`
			Resource   struct{ Type, ID string } `json:"resource"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		key := req.Resource.Type + ":" + req.Resource.ID
		ops := []string{}
		for _, op := range append(append([]string{}, Operations...), DisplayOperation) {
			if allow(req.AccessorID, key, op) {
				ops = append(ops, op)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"operations": ops})
	})
	mux.HandleFunc("/api/safe/v1/authz/resources", func(w http.ResponseWriter, r *http.Request) {
		acc := r.URL.Query().Get("accessor_id")
		op := r.URL.Query().Get("operation")
		ids := []string{}
		for key, ops := range grantsFor(acc) {
			if id := idOf(key); id != "*" && ops[op] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		_ = json.NewEncoder(w).Encode(map[string]any{"ids": ids})
	})
	mux.HandleFunc("/api/safe/v1/authz/policies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			f.posted++
		case http.MethodDelete:
			f.deleted++
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func newSafe(t *testing.T) (*safePermPolicy, *fakeSafe) {
	t.Helper()
	fs := &fakeSafe{}
	srv := httptest.NewServer(fs.handler())
	t.Cleanup(srv.Close)
	return &safePermPolicy{safeURL: srv.URL, http: srv.Client()}, fs
}

func TestSafePerm_OperationCheckAND(t *testing.T) {
	s, _ := newSafe(t)
	ctx := context.Background()
	// u1 has view+list on d1
	if ok, err := s.OperationCheck(ctx, "u1", "user", "d1", ViewOperation, ListOperation); err != nil || !ok {
		t.Fatalf("d1 view+list want true; ok=%v err=%v", ok, err)
	}
	// d2 has view but not list -> AND false
	if ok, _ := s.OperationCheck(ctx, "u1", "user", "d2", ViewOperation, ListOperation); ok {
		t.Fatal("d2 view+list want false (no list)")
	}
}

func TestSafePerm_IsDataAdmin(t *testing.T) {
	s, _ := newSafe(t)
	ctx := context.Background()
	if ok, err := s.IsDataAdmin(ctx, "admin", "user"); err != nil || !ok {
		t.Fatalf("admin want data-admin; ok=%v err=%v", ok, err)
	}
	if ok, _ := s.IsDataAdmin(ctx, "u1", "user"); ok {
		t.Fatal("u1 must not be data-admin")
	}
}

func TestSafePerm_ResourceFilterAndCheckPerm(t *testing.T) {
	s, _ := newSafe(t)
	ctx := context.Background()
	got, err := s.ResourceFilter(ctx, "u1", "user", []string{"d1", "d2", "d9"}, ViewOperation)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("view filter = %v, want [d1 d2]", got)
	}
	// CheckPerm: list op only d1 qualifies -> d2 missing -> forbidden error
	if _, err := s.CheckPerm(ctx, "u1", "user", []string{"d1", "d2"}, ListOperation); err == nil {
		t.Fatal("CheckPerm want forbidden (d2 lacks list)")
	}
	// admin short-circuits to allowed
	if ok, err := s.CheckPerm(ctx, "admin", "user", []string{"d1", "d2"}, ListOperation); err != nil || !ok {
		t.Fatalf("admin CheckPerm want true; ok=%v err=%v", ok, err)
	}
}

func TestSafePerm_MinPermListAndListResource(t *testing.T) {
	s, _ := newSafe(t)
	ctx := context.Background()
	// admin -> all Operations + display
	perms, err := s.MinPermList(ctx, "admin", "user", []string{"d1"})
	if err != nil || len(perms) != len(Operations)+1 {
		t.Fatalf("admin MinPermList = %v err=%v", perms, err)
	}
	// u1 intersection of d1{view,list} and d2{view} -> {view}
	perms, err = s.MinPermList(ctx, "u1", "user", []string{"d1", "d2"})
	if err != nil || len(perms) != 1 || perms[0] != ViewOperation {
		t.Fatalf("u1 MinPermList = %v, want [view]", perms)
	}
	// ListResource: u1 can list d1 only
	ids, err := s.ListResource(ctx, "u1", "user", DataFlowResourceType, ListOperation)
	if err != nil || len(*ids) != 1 || (*ids)[0] != "d1" {
		t.Fatalf("u1 ListResource(list) = %v, want [d1]", ids)
	}
}

func TestSafePerm_WriteRoutingAndNoops(t *testing.T) {
	s, fs := newSafe(t)
	ctx := context.Background()
	if err := s.CreatePolicy(ctx, "u1", "user", "n", "d5", "dag5", []string{"view"}, []string{"delete"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePolicy(ctx, "d5", "d6"); err != nil {
		t.Fatal(err)
	}
	if fs.posted != 1 || fs.deleted != 2 {
		t.Fatalf("posted=%d deleted=%d, want 1/2", fs.posted, fs.deleted)
	}
	// no-ops: no HTTP, no panic
	if err := s.UpdatePolicy(ctx, []string{"p1"}, []string{"view"}, nil); err != nil {
		t.Fatal(err)
	}
	s.HandlePolicyNameChange("d5", "name", DataFlowResourceType)
	if fs.posted != 1 {
		t.Fatalf("no-op must not POST; posted=%d", fs.posted)
	}
}
