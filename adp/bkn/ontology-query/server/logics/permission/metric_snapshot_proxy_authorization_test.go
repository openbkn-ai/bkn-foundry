// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

type snapshotProxyAccessStub struct {
	state                                 interfaces.ManagedProxyAccountState
	stateError                            error
	decisionError                         error
	deny, missing, mismatch, inconsistent bool
	loads, checks                         int
	checked                               map[string]int
}

func (access *snapshotProxyAccessStub) GetManagedProxy(_ context.Context, id string) (*interfaces.ManagedProxyAccountState, error) {
	access.loads++
	state := access.state
	state.ProxyAccountID = id
	return &state, access.stateError
}

func (access *snapshotProxyAccessStub) CheckPermissions(_ context.Context, request interfaces.PermissionChecksRequest) (interfaces.PermissionChecksResponse, error) {
	access.checks++
	if request.EvaluationScope != "" || len(request.Checks) > 100 {
		panic("unexpected permission evaluation scope or batch size")
	}
	if access.checked == nil {
		access.checked = map[string]int{}
	}
	response := interfaces.PermissionChecksResponse{Allowed: !access.deny}
	for _, check := range request.Checks {
		access.checked[request.AccessorID+":"+check.Resource.ID]++
		response.Results = append(response.Results, interfaces.PermissionCheckResult{ResourceType: check.Resource.Type,
			ResourceID: check.Resource.ID, Operation: check.Operation, Allowed: !access.deny})
	}
	if access.missing {
		response.Results = nil
	}
	if access.mismatch {
		response.Results[0].ResourceID = "wrong-resource"
	}
	if access.inconsistent {
		response.Allowed = !response.Allowed
	}
	return response, access.decisionError
}

func activeSnapshotProxyStub() *snapshotProxyAccessStub {
	return &snapshotProxyAccessStub{state: interfaces.ManagedProxyAccountState{AccountType: "app", ManagedBy: "bkn",
		ManagedResourceType: "knowledge_network", ManagedResourceID: "kn", LifecycleStatus: "active", Enabled: true}}
}

func snapshotProxyBinding(id, resource string) *interfaces.TrustedProxyContext {
	return &interfaces.TrustedProxyContext{Proxy: interfaces.AccountInfo{ID: id, Type: "app"},
		Binding: interfaces.TrustedProxyBinding{KNID: "kn", TargetType: "resource", TargetID: resource, Operation: "query_data"}}
}

func TestMetricSnapshotProxyAuthorizationRechecksLivePolicy(t *testing.T) {
	for _, cause := range []string{"disabled", "archived", "owner", "kn", "type", "revoked-grant-or-source", "state-outage", "decision-outage", "missing", "mismatch", "inconsistent"} {
		t.Run(cause, func(t *testing.T) {
			access := activeSnapshotProxyStub()
			service := &metricSnapshotProxyAuthorizationService{access: access}
			bindings := []*interfaces.TrustedProxyContext{snapshotProxyBinding("proxy", "source")}
			if err := service.AuthorizeMetricSnapshotProxies(context.Background(), bindings); err != nil {
				t.Fatal(err)
			}
			status := http.StatusForbidden
			switch cause {
			case "disabled":
				access.state.Enabled = false
			case "archived":
				access.state.LifecycleStatus = "archived"
			case "owner":
				access.state.ManagedBy = "other"
			case "kn":
				access.state.ManagedResourceID = "other"
			case "type":
				access.state.AccountType = "user"
			case "revoked-grant-or-source":
				access.deny = true
			case "state-outage":
				access.stateError = errors.New("unavailable")
				status = 503
			case "decision-outage":
				access.decisionError = errors.New("unavailable")
				status = 503
			case "missing":
				access.missing = true
				status = 503
			case "mismatch":
				access.mismatch = true
				status = 503
			case "inconsistent":
				access.inconsistent = true
				status = 503
			}
			err := service.AuthorizeMetricSnapshotProxies(context.Background(), bindings)
			assertHTTPStatus(t, err, status)
			if access.loads != 2 {
				t.Fatalf("proxy live state was cached across requests: %d reads", access.loads)
			}
		})
	}
}

func TestMetricSnapshotProxyAuthorizationBatchesAndDeduplicatesPerProxy(t *testing.T) {
	access := activeSnapshotProxyStub()
	service := &metricSnapshotProxyAuthorizationService{access: access}
	bindings := []*interfaces.TrustedProxyContext{}
	for index := 0; index < 101; index++ {
		binding := snapshotProxyBinding("one", fmt.Sprint(index))
		bindings = append(bindings, binding, binding)
	}
	bindings = append(bindings, snapshotProxyBinding("two", "0"))
	if err := service.AuthorizeMetricSnapshotProxies(context.Background(), bindings); err != nil {
		t.Fatal(err)
	}
	if access.loads != 2 || access.checks != 3 || len(access.checked) != 102 {
		t.Fatalf("unexpected checks: %#v", access)
	}
	for key, count := range access.checked {
		if count != 1 {
			t.Fatalf("%s checked %d times", key, count)
		}
	}
}
