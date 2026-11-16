// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// managedActionScopes is a rollout list, not a governance policy. A scope is
// switched on only after a policy and an executor adapter are deployed.
type managedActionScopes map[string]struct{}

var planDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parseManagedActionScopes(raw string) (managedActionScopes, error) {
	scopes := make(managedActionScopes)
	if strings.TrimSpace(raw) == "" {
		return scopes, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		network, action, ok := strings.Cut(entry, "/")
		if !ok || network == "" || action == "" || strings.Contains(action, "/") || strings.ContainsAny(entry, "*? 	\n") {
			return nil, fmt.Errorf("invalid GOVERNANCE_MANAGED_ACTIONS entry %q", entry)
		}
		scopes[network+"\x00"+action] = struct{}{}
	}
	return scopes, nil
}

func (scopes managedActionScopes) contains(network, action string) bool {
	_, ok := scopes[network+"\x00"+action]
	return ok
}

func (s *actionSchedulerService) claimActionGovernance(ctx context.Context, req *interfaces.ActionExecutionRequest, executor interfaces.AccountInfo, snapshot map[string]any) error {
	if s.governanceConfigError != nil {
		return governanceUnavailable(ctx, s.governanceConfigError)
	}
	if !s.managedActions.contains(req.KNID, req.ActionTypeID) {
		return nil
	}
	if s.governanceGate == nil {
		return governanceUnavailable(ctx, errors.New("action governance adapter is not configured"))
	}
	if req.GovernanceRequestID == "" || req.GovernancePermitID == "" || !planDigestPattern.MatchString(req.GovernancePlanDigest) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	if len(req.Instances) != 1 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired).WithErrorDetails("managed actions require exactly one resolved target")
	}
	if executor.ID == "" || executor.Type == "" {
		return governanceUnavailable(ctx, errors.New("authenticated action initiator is missing"))
	}
	if len(snapshot) == 0 {
		return governanceUnavailable(ctx, errors.New("published action snapshot is missing"))
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return governanceUnavailable(ctx, fmt.Errorf("cannot encode published action snapshot: %w", err))
	}
	snapshotHash := sha256.Sum256(snapshotBytes)
	claim := interfaces.ActionGovernanceClaim{
		KNID: req.KNID, ActionTypeID: req.ActionTypeID, Initiator: executor,
		DefinitionDigest: hex.EncodeToString(snapshotHash[:]),
		RequestID:        req.GovernanceRequestID, PermitID: req.GovernancePermitID, PlanDigest: req.GovernancePlanDigest,
		TargetIdentity: req.Instances[0].InstanceIdentity, DynamicParams: req.DynamicParams,
		RelationTarget: req.RelationTarget, ObjectInput: req.ObjectInput, ActionTypeSnapshot: snapshot,
	}
	if err := s.governanceGate.ClaimActionPermit(ctx, claim); err != nil {
		if errors.Is(err, interfaces.ErrActionPermitRejected) {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
		}
		return governanceUnavailable(ctx, err)
	}
	return nil
}

func governanceUnavailable(ctx context.Context, err error) error {
	logger.Errorf("Action governance unavailable: %v", err)
	return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_ActionExecution_GovernanceUnavailable)
}
