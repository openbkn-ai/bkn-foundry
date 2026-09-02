// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

type actionExecutionAccess struct {
	baseURL    string
	httpClient rest.HTTPClient
}

func NewActionExecutionAccess(appSetting *common.AppSetting) interfaces.ActionExecutionAccess {
	return &actionExecutionAccess{
		baseURL:    appSetting.OntologyQueryUrl,
		httpClient: common.NewHTTPClient(),
	}
}

func (a *actionExecutionAccess) CheckActionExecution(ctx context.Context,
	request interfaces.ActionExecutionCheckRequest) error {
	if a == nil || a.httpClient == nil || a.baseURL == "" {
		return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionSchedule_InternalError).WithErrorDetails("action execution authorization is not configured")
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || account.ID == "" || account.Type == "" {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
	}
	url := fmt.Sprintf("%s/api/ontology-query/in/v1/knowledge-networks/%s/action-types/%s/execute/check",
		a.baseURL, request.KNID, request.ActionTypeID)
	status, _, err := a.httpClient.PostNoUnmarshal(ctx, url, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}, map[string]any{"dynamic_params": request.DynamicParams})
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionSchedule_InternalError).WithErrorDetails("action execution permission check failed")
	}
	switch status {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter)
	case http.StatusForbidden, http.StatusUnauthorized:
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
	case http.StatusNotFound:
		return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_ActionTypeNotFound)
	default:
		return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionSchedule_InternalError).WithErrorDetails("action execution permission check unavailable")
	}
}
