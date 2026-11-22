// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

type actionGateStub struct {
	calls        int
	claim        interfaces.ActionGovernanceClaim
	err          error
	availableErr error
}

type preparingActionGateStub struct {
	actionGateStub
	prepares int
	err      error
	id       string
}

func (gate *preparingActionGateStub) PrepareActionPermit(_ context.Context, claim interfaces.ActionGovernanceClaim) (string, error) {
	gate.prepares++
	if claim.PermitID != "" || claim.RequestID != "request1" {
		return "", errors.New("permit preparation received an untrusted plan")
	}
	if gate.err != nil {
		return "", gate.err
	}
	if gate.id != "" {
		return gate.id, nil
	}
	return "permit-issued", nil
}

func (gate *actionGateStub) Available() error { return gate.availableErr }

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

func TestActionGovernanceDigestBindsPublishedSnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	snapshot := map[string]any{"name": "pay", "object_type_id": "order"}
	models.EXPECT().GetActionType(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "pay").
		Return(interfaces.ActionType{ATID: "pay"}, snapshot, true, nil)
	gate := &dynamicScopeGateStub{required: true}
	service := &actionSchedulerService{omAccess: models, governanceGate: gate}
	digest, err := service.ActionGovernanceDigest(context.Background(), "kn1", "pay")
	if err != nil {
		t.Fatalf("digest error=%v", err)
	}
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	if err := service.claimActionGovernance(context.Background(), governanceRequest(), actor, snapshot); err != nil {
		t.Fatalf("claim action governance: %v", err)
	}
	if gate.calls != 1 || digest == "" || digest != gate.claim.DefinitionDigest {
		t.Fatalf("published digest=%q, claimed digest=%q, claims=%d", digest, gate.claim.DefinitionDigest, gate.calls)
	}
}

func TestActionGovernanceDigestRejectsMissingDefinition(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	models.EXPECT().GetActionType(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "gone").
		Return(interfaces.ActionType{}, nil, false, nil)
	service := &actionSchedulerService{omAccess: models}
	if _, err := service.ActionGovernanceDigest(context.Background(), "kn1", "gone"); status(err) != http.StatusNotFound {
		t.Fatalf("missing definition status=%d err=%v", status(err), err)
	}
	if _, err := service.ActionGovernanceDigest(context.Background(), "", "pay"); status(err) != http.StatusBadRequest {
		t.Fatalf("missing ids status=%d err=%v", status(err), err)
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
	gate.availableErr = errors.New("action governance adapter is not configured")
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusServiceUnavailable || gate.calls != 0 {
		t.Fatalf("unavailable gate with missing permit status=%d calls=%d", status(err), gate.calls)
	}
	request.GovernancePermitID = "permit1"
	request.Instances = append(request.Instances, interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"order_id": "o2"}})
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusServiceUnavailable || gate.calls != 0 {
		t.Fatalf("unavailable gate with multiple targets status=%d calls=%d", status(err), gate.calls)
	}
	request.Instances = request.Instances[:1]
	request.GovernancePermitID = ""
	gate.availableErr = nil
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusConflict || gate.calls != 0 {
		t.Fatalf("missing permit status=%d calls=%d", status(err), gate.calls)
	}
	request.GovernancePermitID = "permit1"
	request.Instances = append(request.Instances, interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{"order_id": "o2"}})
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusConflict || gate.calls != 0 {
		t.Fatalf("multiple targets status=%d calls=%d", status(err), gate.calls)
	}
	request.Instances = request.Instances[:1]
	if err := service.claimActionGovernance(context.Background(), request, actor, nil); status(err) != http.StatusServiceUnavailable || gate.calls != 0 {
		t.Fatalf("missing published snapshot status=%d calls=%d", status(err), gate.calls)
	}
}

func TestManagedActionPreparesPermitFromTrustedResolvedPlan(t *testing.T) {
	managed, _ := parseManagedActionScopes("kn1/pay")
	gate := &preparingActionGateStub{}
	service := &actionSchedulerService{managedActions: managed, governanceGate: gate}
	request := governanceRequest()
	request.GovernancePermitID = ""
	snapshot := map[string]any{"id": "pay", "revision": "v2"}
	actor := interfaces.AccountInfo{ID: "alice", Type: "user"}
	if err := service.claimActionGovernance(context.Background(), request, actor, snapshot); err != nil {
		t.Fatal(err)
	}
	if gate.prepares != 1 || gate.calls != 1 || gate.claim.PermitID != "permit-issued" || request.GovernancePermitID != "permit-issued" {
		t.Fatalf("issued permit not bound to execution: prepares=%d claims=%d claim=%+v request=%+v", gate.prepares, gate.calls, gate.claim, request)
	}
	request.GovernancePermitID = ""
	gate.err = interfaces.ErrActionPermitRejected
	if err := service.claimActionGovernance(context.Background(), request, actor, snapshot); status(err) != http.StatusConflict || gate.calls != 1 || request.GovernancePermitID != "" {
		t.Fatalf("rejected preparation reached claim: %v, claims=%d", err, gate.calls)
	}
	gate.err = nil
	gate.id = "invalid/permit"
	if err := service.claimActionGovernance(context.Background(), request, actor, snapshot); status(err) != http.StatusServiceUnavailable || gate.calls != 1 || request.GovernancePermitID != "" {
		t.Fatalf("invalid issued permit reached claim: %v, claims=%d", err, gate.calls)
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
	sum := sha256.Sum256([]byte(`{"id":"pay","revision":"v2"}`))
	if gate.calls != 1 || gate.claim.Initiator.ID != "alice" || gate.claim.TargetIdentity["order_id"] != "o1" || gate.claim.ActionTypeSnapshot["revision"] != "v2" || gate.claim.DefinitionDigest != hex.EncodeToString(sum[:]) {
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

type dynamicScopeGateStub struct {
	actionGateStub
	required  bool
	lookupErr error
}

func (gate *dynamicScopeGateStub) RequiresActionGovernance(context.Context, string, string) (bool, error) {
	return gate.required, gate.lookupErr
}

func TestSavedPolicyRequiresPermitWithoutStaticRollout(t *testing.T) {
	gate := &dynamicScopeGateStub{required: true}
	service := &actionSchedulerService{governanceGate: gate}
	req := governanceRequest()
	req.GovernanceRequestID = ""
	if err := service.claimActionGovernance(context.Background(), req, interfaces.AccountInfo{ID: "alice", Type: "user"}, map[string]any{"id": "pay"}); err == nil || gate.calls != 0 {
		t.Fatalf("missing permit bypassed: %v", err)
	}
	req = governanceRequest()
	if err := service.claimActionGovernance(context.Background(), req, interfaces.AccountInfo{ID: "alice", Type: "user"}, map[string]any{"id": "pay"}); err != nil || gate.calls != 1 {
		t.Fatalf("saved policy not enforced: %v", err)
	}
	gate.lookupErr = errors.New("governance unavailable")
	if err := service.claimActionGovernance(context.Background(), governanceRequest(), interfaces.AccountInfo{}, nil); err == nil {
		t.Fatal("lookup failure bypassed gate")
	}
	gate.lookupErr = nil
	gate.required = false
	req = governanceRequest()
	if err := service.claimActionGovernance(context.Background(), req, interfaces.AccountInfo{}, nil); err != nil || req.GovernanceRequestID != "" || req.GovernancePermitID != "" {
		t.Fatalf("unmanaged references trusted: %+v %v", req, err)
	}
}
