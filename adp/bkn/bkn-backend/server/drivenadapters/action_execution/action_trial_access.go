// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// actionTrialTimeout bounds a trial: it runs while the action type is saved.
const actionTrialTimeout = 60 * time.Second

// NewActionTrialAccess runs recognition actions once through ontology-query.
func NewActionTrialAccess(appSetting *common.AppSetting) interfaces.ActionTrialAccess {
	return &actionExecutionAccess{
		baseURL:    appSetting.OntologyQueryUrl,
		httpClient: common.NewHTTPClient(),
	}
}

// TrialEventAction asks ontology-query for the objects the action type selects now, as the
// account in ctx. A refusal of the definition is a 400 that carries ontology-query's reason.
func (a *actionExecutionAccess) TrialEventAction(ctx context.Context, branch string,
	actionType *interfaces.ActionType) (*interfaces.ActionEventTrial, error) {
	if a == nil || a.httpClient == nil || a.baseURL == "" {
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails("the trial of record-event action types is not configured")
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || account.ID == "" || account.Type == "" {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	target := fmt.Sprintf("%s/api/ontology-query/in/v1/knowledge-networks/%s/action-trial?branch=%s",
		a.baseURL, url.PathEscape(actionType.KNID), url.QueryEscape(branch))
	trialCtx, cancel := context.WithTimeout(ctx, actionTrialTimeout)
	defer cancel()
	status, body, err := a.httpClient.PostNoUnmarshal(trialCtx, target, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}, actionType)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails("the trial of the record-event action type could not reach ontology-query")
	}
	switch status {
	case http.StatusOK:
		var reply struct {
			Event *interfaces.ActionEventTrial `json:"event"`
		}
		if err := json.Unmarshal(body, &reply); err != nil || reply.Event == nil {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadGateway,
				berrors.BknBackend_ActionType_InternalError).WithErrorDetails("the trial of the record-event action type returned no result")
		}
		if reply.Event.Hits == nil {
			reply.Event.Hits = []map[string]any{}
		}
		return reply.Event, nil
	case http.StatusBadRequest, http.StatusNotFound:
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails("trial: " + upstreamReason(body))
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("trial: " + upstreamReason(body))
	default:
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(fmt.Sprintf("the trial of the record-event action type is unavailable (%d): %s", status, upstreamReason(body)))
	}
}

// upstreamReason is what an error body of ontology-query says: its description and details.
func upstreamReason(body []byte) string {
	var reply httperrors.BaseError
	if err := json.Unmarshal(body, &reply); err != nil {
		return strings.TrimSpace(string(body))
	}
	parts := []string{}
	if reply.Description != "" {
		parts = append(parts, reply.Description)
	}
	switch details := reply.ErrorDetails.(type) {
	case nil:
	case string:
		if details != "" {
			parts = append(parts, details)
		}
	default:
		if text, err := json.Marshal(details); err == nil {
			parts = append(parts, string(text))
		}
	}
	if len(parts) == 0 {
		return reply.ErrorCode
	}
	return strings.Join(parts, ": ")
}
