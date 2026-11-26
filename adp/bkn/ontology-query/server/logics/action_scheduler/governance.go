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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

// managedActionScopes is a rollout list, not a governance policy. A scope is
// switched on only after a policy and an executor adapter are deployed.
type managedActionScopes map[string]struct{}

var planDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var permitIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

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

func (s *actionSchedulerService) actionGovernanceRequired(ctx context.Context, knID, actionTypeID string) (bool, error) {
	if s.governanceConfigError != nil {
		return false, governanceUnavailable(ctx, s.governanceConfigError)
	}
	required := s.managedActions.contains(knID, actionTypeID)
	if resolver, ok := s.governanceGate.(interfaces.ActionGovernanceScope); ok {
		current, err := resolver.RequiresActionGovernance(ctx, knID, actionTypeID)
		if err != nil {
			return false, governanceUnavailable(ctx, err)
		}
		required = required || current
	}
	return required, nil
}

func (s *actionSchedulerService) claimActionGovernance(ctx context.Context, req *interfaces.ActionExecutionRequest, executor interfaces.AccountInfo, snapshot map[string]any) error {
	required, err := s.actionGovernanceRequired(ctx, req.KNID, req.ActionTypeID)
	if err != nil {
		return err
	}
	if !required {
		// Client-supplied references are not trusted for an ungoverned execution.
		req.GovernanceRequestID, req.GovernancePermitID, req.GovernancePlanDigest = "", "", ""
		return nil
	}
	if s.governanceGate == nil {
		return governanceUnavailable(ctx, errors.New("action governance adapter is not configured"))
	}
	if err := s.governanceGate.Available(); err != nil {
		return governanceUnavailable(ctx, err)
	}
	if req.GovernanceRequestID == "" || !planDigestPattern.MatchString(req.GovernancePlanDigest) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	if req.GovernancePermitID == "" {
		if _, ok := s.governanceGate.(interfaces.ActionGovernancePreparer); !ok {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
		}
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
	if claim.PermitID == "" {
		preparer, ok := s.governanceGate.(interfaces.ActionGovernancePreparer)
		if !ok {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
		}
		permitID, err := preparer.PrepareActionPermit(ctx, claim)
		if err != nil {
			return governanceGateError(ctx, err)
		}
		if !permitIDPattern.MatchString(permitID) {
			return governanceUnavailable(ctx, errors.New("governance returned an invalid permit ID"))
		}
		claim.PermitID = permitID
		req.GovernancePermitID = permitID
	}
	if err := s.governanceGate.ClaimActionPermit(ctx, claim); err != nil {
		return governanceGateError(ctx, err)
	}
	return nil
}

// ActionGovernanceDigest hashes the current published definition snapshot with
// the exact encoding used by claimActionGovernance, so a governance plan built
// from it cannot drift from the definition the executor will enforce.
func (s *actionSchedulerService) ActionGovernanceDigest(ctx context.Context, knID, atID string) (string, error) {
	if s == nil || s.omAccess == nil {
		return "", actionPermissionUnavailable(ctx, errors.New("ontology model access is not configured"))
	}
	if knID == "" || atID == "" {
		return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter)
	}
	// The digest is always bound to the published branch, even when the caller
	// is previewing a draft definition elsewhere in the product.
	_, snapshot, exists, err := s.omAccess.GetActionType(ctx, knID, interfaces.MAIN_BRANCH, atID)
	if err != nil {
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return "", versionErr
		}
		return "", actionPermissionUnavailable(ctx, err)
	}
	if !exists || len(snapshot) == 0 {
		return "", httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound)
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return "", actionPermissionUnavailable(ctx, err)
	}
	digest := sha256.Sum256(snapshotBytes)
	encoded := hex.EncodeToString(digest[:])
	if !planDigestPattern.MatchString(encoded) {
		return "", actionPermissionUnavailable(ctx, errors.New("published action snapshot digest is invalid"))
	}
	return encoded, nil
}

func governanceGateError(ctx context.Context, err error) error {
	if errors.Is(err, interfaces.ErrActionPermitRejected) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	return governanceUnavailable(ctx, err)
}

func governanceUnavailable(ctx context.Context, err error) error {
	logger.Errorf("Action governance unavailable: %v", err)
	return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_ActionExecution_GovernanceUnavailable)
}
