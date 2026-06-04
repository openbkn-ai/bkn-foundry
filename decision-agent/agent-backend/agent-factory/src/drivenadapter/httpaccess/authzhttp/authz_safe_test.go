package authzhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdapmsenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp/authzhttpreq"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cenum"
)

// noopLogger satisfies icmp.Logger for tests (all methods discard).
type noopLogger struct{}

func (noopLogger) Infof(string, ...interface{})  {}
func (noopLogger) Infoln(...interface{})         {}
func (noopLogger) Debugf(string, ...interface{}) {}
func (noopLogger) Debugln(...interface{})        {}
func (noopLogger) Errorf(string, ...interface{}) {}
func (noopLogger) Errorln(...interface{})        {}
func (noopLogger) Warnf(string, ...interface{})  {}
func (noopLogger) Warnln(...interface{})         {}
func (noopLogger) Panicf(string, ...interface{}) {}
func (noopLogger) Panicln(...interface{})        {}
func (noopLogger) Fatalf(string, ...interface{}) {}
func (noopLogger) Fatalln(...interface{})        {}

// fakeSafe is a minimal bkn-safe authz server: "use" is allowed on agent a1
// for any accessor; everything else is denied. Records the last DELETE.
type fakeSafe struct {
	lastDeleteType string
	lastDeleteID   string
	posted         int
}

func (f *fakeSafe) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/v1/authz/check", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Resource  struct{ Type, ID string } `json:"resource"`
			Operation string                    `json:"operation"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		allowed := req.Resource.Type == "agent" && req.Resource.ID == "a1" && req.Operation == "use"
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": allowed})
	})
	mux.HandleFunc("/api/safe/v1/authz/operations", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Resource struct{ Type, ID string } `json:"resource"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		ops := []string{}
		if req.Resource.Type == "agent" {
			ops = []string{"use"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"operations": ops})
	})
	mux.HandleFunc("/api/safe/v1/authz/policies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": []map[string]any{
				{"accessor_id": "u-1", "operations": []string{"use", "publish"}},
			}})
		case http.MethodPost:
			f.posted++
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			var req struct {
				Resource struct{ Type, ID string } `json:"resource"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.lastDeleteType, f.lastDeleteID = req.Resource.Type, req.Resource.ID
			w.WriteHeader(http.StatusNoContent)
		}
	})
	return mux
}

func newSafeAdapter(t *testing.T) (*safeAuthZHttpAcc, *fakeSafe) {
	t.Helper()
	fs := &fakeSafe{}
	srv := httptest.NewServer(fs.handler())
	t.Cleanup(srv.Close)
	return &safeAuthZHttpAcc{safeURL: srv.URL, http: srv.Client(), logger: noopLogger{}}, fs
}

func TestSafeAdapter_OperationCheck_AND(t *testing.T) {
	a, _ := newSafeAdapter(t)
	ctx := context.Background()

	// single allowed op
	res, err := a.OperationCheck(ctx, authzhttpreq.NewSingleUserAgentUseCheckReq("u-1", "a1"))
	if err != nil || res == nil || !res.Result {
		t.Fatalf("use on a1 want allowed; res=%+v err=%v", res, err)
	}
	// AND: one denied op makes the whole check false
	multi := authzhttpreq.NewSingleUserCheckReq("u-1", "a1", cdaenum.ResourceTypeDataAgent,
		[]cdapmsenum.Operator{cdapmsenum.AgentUse, cdapmsenum.AgentPublish})
	res, err = a.OperationCheck(ctx, multi)
	if err != nil || res == nil || res.Result {
		t.Fatalf("use+publish want denied (AND); res=%+v err=%v", res, err)
	}
}

func TestSafeAdapter_ResourceFilterAndAgentUse(t *testing.T) {
	a, _ := newSafeAdapter(t)
	ctx := context.Background()

	got, err := a.FilterCanUseAgentIDs(ctx, "u-1", []string{"a1", "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a1" {
		t.Fatalf("FilterCanUseAgentIDs = %v, want [a1]", got)
	}

	ok, err := a.SingleAgentUseCheck(ctx, "u-1", cenum.PmsTargetObjTypeUser, "a1")
	if err != nil || !ok {
		t.Fatalf("SingleAgentUseCheck a1 want true; ok=%v err=%v", ok, err)
	}
}

func TestSafeAdapter_ListPolicyAll(t *testing.T) {
	a, _ := newSafeAdapter(t)
	ctx := context.Background()

	req := authzhttpreq.NewListPolicyReq("a1", cdaenum.ResourceTypeDataAgent)
	res, err := a.ListPolicyAll(ctx, req, "tok")
	if err != nil || res == nil {
		t.Fatalf("ListPolicyAll err=%v res=%v", err, res)
	}
	if res.TotalCount != 1 || len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %+v", res)
	}
	e := res.Entries[0]
	if e.Accessor.ID != "u-1" || e.Resource.ID != "a1" || e.ExpiresAt != neverExpire {
		t.Fatalf("entry mapping wrong: %+v / %+v", e.Accessor, e.Resource)
	}
	// caller filters; AgentUse must survive (it's in allow [use, publish])
	if err := res.FilterByOperation(cdapmsenum.AgentUse); err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 1 {
		t.Fatalf("AgentUse should survive filter, got %d", res.TotalCount)
	}
}

func TestSafeAdapter_DeleteAndNoops(t *testing.T) {
	a, fs := newSafeAdapter(t)
	ctx := context.Background()

	if err := a.DeleteAgentPolicy(ctx, "a9"); err != nil {
		t.Fatal(err)
	}
	if fs.lastDeleteType != "agent" || fs.lastDeleteID != "a9" {
		t.Fatalf("delete sent %s:%s, want agent:a9", fs.lastDeleteType, fs.lastDeleteID)
	}

	// init-time setup methods are no-ops (seed covers them): no HTTP, no error.
	for _, err := range []error{
		a.GrantAgentUsePmsForAppAdmin(ctx),
		a.GrantMgmtPmsForAppAdmin(ctx),
		a.DenyAgentUsePmsForAllAccessor(ctx, "a1", "n"),
		a.SetResourceType(ctx, cdaenum.ResourceTypeDataAgent, nil),
	} {
		if err != nil {
			t.Fatalf("no-op returned error: %v", err)
		}
	}
	if fs.posted != 0 {
		t.Fatalf("no-op methods must not POST; posted=%d", fs.posted)
	}
}
