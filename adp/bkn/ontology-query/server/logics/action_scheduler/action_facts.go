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
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

// CheckActionFacts reuses the executor's published definition, target resolver,
// condition check and parameter binding. It never creates an execution.
func (s *actionSchedulerService) CheckActionFacts(ctx context.Context, check *interfaces.ActionFactCheck) error {
	if check == nil || check.KNID == "" || check.ActionTypeID == "" ||
		!planDigestPattern.MatchString(check.DefinitionDigest) || check.TargetIdentity == nil {
		return actionTargetError(ctx, "incomplete action fact check")
	}
	if s == nil || s.omAccess == nil {
		return actionPermissionUnavailable(ctx, errors.New("ontology model access is not configured"))
	}
	actionType, snapshot, exists, err := s.omAccess.GetActionType(ctx, check.KNID, interfaces.MAIN_BRANCH, check.ActionTypeID)
	if err != nil {
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return versionErr
		}
		return actionPermissionUnavailable(ctx, err)
	}
	if !exists || len(snapshot) == 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return actionPermissionUnavailable(ctx, err)
	}
	digest := sha256.Sum256(snapshotBytes)
	if hex.EncodeToString(digest[:]) != check.DefinitionDigest {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	request := &interfaces.ActionExecutionRequest{
		KNID: check.KNID, ActionTypeID: check.ActionTypeID, Branch: interfaces.MAIN_BRANCH,
		DynamicParams: check.DynamicParams, ObjectInput: check.ObjectInput, RelationTarget: check.RelationTarget,
	}
	if check.RelationTarget == nil && len(check.ObjectInput) == 0 && len(check.TargetIdentity) != 0 {
		request.InstanceIdentities = []map[string]any{check.TargetIdentity}
	}
	if err := validateActionOperation(&actionType); err != nil {
		return actionTargetError(ctx, err.Error())
	}
	if missing := logics.MissingActionInputDynamicParamNames(&actionType, request.DynamicParams); len(missing) > 0 {
		return actionTargetError(ctx, "required dynamic action parameters are missing")
	}
	// The trusted application still needs its own model/data grants. Check
	// them and the current proxy binding before any target data is read.
	if _, err := s.authorizeActionType(ctx, check.KNID, &actionType); err != nil {
		return err
	}
	if _, _, err := s.resolveActionProxyContext(ctx, check.KNID, &actionType); err != nil {
		return err
	}
	instances, data, err := s.getInstancesForAction(ctx, &actionType, request)
	if err != nil {
		return err
	}
	if len(instances) != 1 || len(data) != 1 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	expected, err := json.Marshal(check.TargetIdentity)
	if err != nil {
		return actionTargetError(ctx, "invalid target identity")
	}
	actual, err := json.Marshal(instances[0].InstanceIdentity)
	if err != nil || string(actual) != string(expected) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired)
	}
	if _, err := s.buildExecutionParams(&actionType, data[0], check.DynamicParams, instances[0].InstanceIdentity); err != nil {
		return actionTargetError(ctx, err.Error())
	}
	return nil
}
