// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

type actionGateStub struct {
	calls int
	claim interfaces.ActionGovernanceClaim
	err   error
}

func (gate *actionGateStub) ClaimActionPermit(_ context.Context, claim interfaces.ActionGovernanceClaim) error {
	gate.calls++
	gate.claim = claim
	return gate.err
}

func TestManagedActionScopeParserRejectsAmbiguousRollout(t *testing.T) {
	scopes, err := parseManagedActionScopes("kn1/pay, kn2/ship")
	if err != nil || !scopes.contains("kn1", "pay") || !scopes.contains("kn2", "ship") || scopes.contains("kn1", "ship") {
		t.Fatalf("scopes=%v err=%v", scopes, err)
	}
	for _, raw := range []string{"kn1", "kn1/", "*/pay", "kn1/pay,", "kn1/pay/extra"} {
		if _, err := parseManagedActionScopes(raw); err == nil {
			t.Fatalf("invalid rollout %q accepted", raw)
		}
	}
}

func governanceRequest() *interfaces.ActionExecutionRequest {
	return &interfaces.ActionExecutionRequest{KNID: "kn1", ActionTypeID: "pay", GovernanceRequestID: "request1", GovernancePermitID: "permit1", GovernancePlanDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", DynamicParams: map[string]any{"amount": 100}, Instances: []interfaces.ObjectSystemInfo{{InstanceIdentity: map[string]any{"order_id": "o1"}}}}
}

func TestManagedActionRequiresGateAndPermit(t *testing.T) {
	managed, _ := parseManagedActionScopes("kn1/pay")
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	request := governanceRequest()
	service := &actionSchedulerService{managedActions: managed}
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusServiceUnavailable {
		t.Fatalf("missing gate status=%d err=%v", status(err), err)
	}
	gate := &actionGateStub{}
	service.governanceGate = gate
	request.GovernancePermitID = ""
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusConflict || gate.calls != 0 {
		t.Fatalf("missing permit status=%d calls=%d", status(err), gate.calls)
	}
	request.GovernancePermitID = "permit1"
	request.Instances = append(request.Instances, interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"order_id": "o2"}})
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusConflict || gate.calls != 0 {
		t.Fatalf("multiple targets status=%d calls=%d", status(err), gate.calls)
	}
}

func TestManagedActionPassesTrustedResolvedTargetToGate(t *testing.T) {
	managed, _ := parseManagedActionScopes("kn1/pay")
	gate := &actionGateStub{}
	service := &actionSchedulerService{managedActions: managed, governanceGate: gate}
	request := governanceRequest()
	snapshot := map[string]any{"id": "pay", "revision": "v2"}
	if err := service.claimActionGovernance(context.Background(), request, interfaces.AccountInfo{ID: "alice", Type: "user"}, snapshot); err != nil {
		t.Fatal(err)
	}
	if gate.calls != 1 || gate.claim.Initiator.ID != "alice" || gate.claim.TargetIdentity["order_id"] != "o1" || gate.claim.ActionTypeSnapshot["revision"] != "v2" {
		t.Fatalf("untrusted or incomplete claim: %+v", gate.claim)
	}
	gate.err = interfaces.ErrActionPermitRejected
	if err := service.claimActionGovernance(context.Background(), request, interfaces.AccountInfo{ID: "alice", Type: "user"}, snapshot); status(err) != http.StatusConflict {
		t.Fatalf("replayed permit status=%d err=%v", status(err), err)
	}
	gate.err = errors.New("governance service unavailable")
	if err := service.claimActionGovernance(context.Background(), request, interfaces.AccountInfo{ID: "alice", Type: "user"}, snapshot); status(err) != http.StatusServiceUnavailable {
		t.Fatalf("unavailable governance status=%d err=%v", status(err), err)
	}
}

func TestManagedActionRejectsBeforeExecutionRecord(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetActionType(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "pay").Return(
		interfaces.ActionType{ATID: "pay", ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeTool, BoxID: "box1", ToolID: "tool1"}, OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"}},
		map[string]any{"id": "pay", "revision": "v2"}, true, nil)
	managed, _ := parseManagedActionScopes("kn1/pay")
	gate := &actionGateStub{err: interfaces.ErrActionPermitRejected}
	service := &actionSchedulerService{
		omAccess: models, permissions: &actionPermissionStub{}, proxy: &actionProxyResolverStub{},
		managedActions: managed, governanceGate: gate,
	}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "alice", Type: "user"})
	response, err := service.ExecuteAction(ctx, governanceRequest())
	if response != nil || status(err) != http.StatusConflict || gate.calls != 1 || gate.claim.TargetIdentity == nil {
		t.Fatalf("response=%+v err=%v gate=%+v", response, err, gate)
	}
}

func TestUnmanagedActionsKeepExistingExecutionContract(t *testing.T) {
	service := &actionSchedulerService{managedActions: managedActionScopes{}}
	if err := service.claimActionGovernance(context.Background(), &interfaces.ActionExecutionRequest{KNID: "kn1", ActionTypeID: "legacy"}, interfaces.AccountInfo{}, nil); err != nil {
		t.Fatal(err)
	}
	service.governanceConfigError = errors.New("bad rollout configuration")
	if err := service.claimActionGovernance(context.Background(), &interfaces.ActionExecutionRequest{KNID: "kn1", ActionTypeID: "legacy"}, interfaces.AccountInfo{}, nil); status(err) != http.StatusServiceUnavailable {
		t.Fatalf("invalid config must fail closed: %v", err)
	}
}

func status(err error) int {
	if httpErr, ok := err.(*httperrors.HTTPError); ok {
		return httpErr.HTTPCode
	}
	return 0
}
