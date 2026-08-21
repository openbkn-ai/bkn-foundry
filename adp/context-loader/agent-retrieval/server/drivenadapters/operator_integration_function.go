// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// executeFunctionURI invokes sandbox execution through Execution Factory's public API.
//
// The internal endpoint /internal-v1/function/exec/:version runs only a
// registered function version and cannot execute arbitrary code.
//
// The public API enforces the caller's execute permission on the operator type.
// Calling the internal endpoint with a service identity would bypass that check,
// so this request carries the original caller bearer token.
const executeFunctionURI = "/v1/function/execute"

// ErrCallerTokenMissing indicates that the caller token is absent from context.
//
// Do not silently fall back to a service identity because that would bypass
// Execution Factory's execute permission check.
var ErrCallerTokenMissing = fmt.Errorf("caller token is required for sandbox execution")

// ExecuteFunction executes code in the sandbox.
func (o *operatorIntegrationClient) ExecuteFunction(
	ctx context.Context, req *interfaces.ExecuteFunctionRequest,
) (*interfaces.ExecuteFunctionResponse, error) {
	if req == nil || req.Code == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "FunctionCodeRequired"))
	}

	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}

	header := o.skillHeader(ctx, "operator.function.execute")
	header["Authorization"] = "Bearer " + token

	event := req.Event
	if event == nil {
		// Execution Factory requires event; send an empty object when there is no input.
		event = map[string]any{}
	}
	body := map[string]any{"code": req.Code, "language": req.Language, "event": event}
	if req.Timeout > 0 {
		body["timeout"] = req.Timeout
	}

	fullURL := o.baseURL + executeFunctionURI
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ExecuteFunction] URL: %s, language: %s", fullURL, req.Language)

	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "FunctionExecutionRequestFailed", err)
	}

	resp := &interfaces.ExecuteFunctionResponse{}
	if err = common.UnmarshalPreciseJSON(utils.ObjectToByte(respBody), resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			infraErr.LocalizedDetail(ctx, "FunctionExecutionResponseInvalid"))
	}
	return resp, nil
}
