//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Integration test for the bkn-safe authz adapter against a REAL bkn-safe.
// Run: BKN_SAFE_URL=http://127.0.0.1:13000 go test -tags integration ./drivenadapters/ -run SafeAuthz -v
// Skipped unless BKN_SAFE_URL is set. Exercises the full Authorization interface
// exec-factory relies on — grant (CreatePolicy/CreateOwnerPolicy), decide
// (OperationCheck AND-semantics), filter (ResourceFilter), list (ResourceList),
// revoke (DeletePolicy).
package drivenadapters

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

type noopLogger struct{}

func (noopLogger) Debug(...interface{})                            {}
func (noopLogger) Info(...interface{})                            {}
func (noopLogger) Warn(...interface{})                            {}
func (noopLogger) Error(...interface{})                           {}
func (noopLogger) Debugf(string, ...interface{})                  {}
func (noopLogger) Infof(string, ...interface{})                   {}
func (noopLogger) Warnf(string, ...interface{})                   {}
func (noopLogger) Errorf(string, ...interface{})                  {}
func (l noopLogger) WithContext(context.Context) interfaces.Logger { return l }

func TestSafeAuthzEndToEnd(t *testing.T) {
	url := os.Getenv("BKN_SAFE_URL")
	if url == "" {
		t.Skip("BKN_SAFE_URL not set; skipping bkn-safe integration test")
	}
	ctx := context.Background()
	s := newSafeAuthorization(url, noopLogger{})

	// Unique accessor + resource so reruns are independent.
	user := fmt.Sprintf("itest-user-%d", time.Now().UnixNano())
	const rtype = string(interfaces.AuthResourceTypeToolBox) // "tool_box"
	tb1, tb2 := "itest-tb-1", "itest-tb-2"
	acc := &interfaces.AuthAccessor{ID: user, Type: "user"}

	check := func(id string, ops ...interfaces.AuthOperationType) bool {
		resp, err := s.OperationCheck(ctx, &interfaces.AuthOperationCheckRequest{
			Accessor: acc, Resource: &interfaces.AuthResource{ID: id, Type: rtype},
			Operation: ops, Method: interfaces.AuthMethodGet,
		})
		if err != nil {
			t.Fatalf("OperationCheck: %v", err)
		}
		return resp.Result
	}

	// 1. before any grant -> denied
	if check(tb1, interfaces.AuthOperationTypeExecute) {
		t.Fatal("expected execute denied before grant")
	}

	// 2. CreateOwnerPolicy-equivalent: grant the user the owner op set on tb1
	allow := make([]*interfaces.AuthOperation, 0)
	for _, op := range interfaces.OwnerPolicyList {
		allow = append(allow, &interfaces.AuthOperation{ID: string(op)})
	}
	if err := s.CreatePolicy(ctx, []*interfaces.AuthCreatePolicyRequest{{
		Accessor: acc, Resource: &interfaces.AuthResource{ID: tb1, Type: rtype},
		Operation: &interfaces.PolicyOperation{Allow: allow},
	}}); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	// 3. owner can execute/modify tb1; AND-semantics holds; no leak to tb2
	if !check(tb1, interfaces.AuthOperationTypeExecute) {
		t.Error("owner should execute tb1")
	}
	if !check(tb1, interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypeModify) {
		t.Error("owner should pass AND(execute,modify) on tb1")
	}
	if check(tb2, interfaces.AuthOperationTypeExecute) {
		t.Error("grant must not leak to tb2")
	}

	// 4. ResourceFilter keeps only the allowed ones
	res, err := s.ResourceFilter(ctx, &interfaces.AuthResourceFilterRequest{
		Accessor: acc,
		Resources: []*interfaces.AuthResource{
			{ID: tb1, Type: rtype}, {ID: tb2, Type: rtype},
		},
		Operations: []interfaces.AuthOperationType{interfaces.AuthOperationTypeExecute},
		Method:     interfaces.AuthMethodGet,
	})
	if err != nil {
		t.Fatalf("ResourceFilter: %v", err)
	}
	if len(res) != 1 || res[0].ID != tb1 {
		t.Errorf("ResourceFilter = %v, want [tb1]", res)
	}

	// 5. ResourceList returns the granted toolbox for view
	listRes, err := s.ResourceList(ctx, &interfaces.ResourceListRequest{
		Accessor:  acc,
		Resource:  &interfaces.AuthResource{Type: rtype},
		Operation: []interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
		Method:    interfaces.AuthMethodGet,
	})
	if err != nil {
		t.Fatalf("ResourceList: %v", err)
	}
	if len(listRes) != 1 || listRes[0].ID != tb1 {
		t.Errorf("ResourceList = %v, want [%s]", listRes, tb1)
	}

	// 6. revoke -> denied again
	if err := s.DeletePolicy(ctx, &interfaces.AuthDeletePolicyRequest{
		Method: interfaces.AuthMethodGet, Resources: []*interfaces.AuthResource{{ID: tb1, Type: rtype}},
	}); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if check(tb1, interfaces.AuthOperationTypeExecute) {
		t.Error("execute should be denied after DeletePolicy")
	}

	t.Log("bkn-safe authz adapter: grant -> check(AND) -> filter -> list -> revoke all OK")
}
