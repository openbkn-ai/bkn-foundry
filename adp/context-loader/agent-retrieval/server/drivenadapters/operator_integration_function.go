// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
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

// ErrCallerTokenMissing indicates that a public caller token is absent from context.
var (
	ErrCallerTokenMissing    = fmt.Errorf("caller token is required for execution-factory authorization")
	ErrCallerIdentityMissing = fmt.Errorf("trusted caller identity is required for execution-factory authorization")
	// Arbitrary function execution has no equivalent caller-scoped internal
	// endpoint. Capability resources use the trusted internal caller routes, but
	// this operation must still carry the original bearer token.
	ErrInternalCapabilityAuthorizationUnsupported = fmt.Errorf(
		"function execution is not supported on the internal API without a caller token")
)

func (o *operatorIntegrationClient) callerAuthorizationHeader(
	ctx context.Context, operationName string,
) (map[string]string, error) {
	header := o.skillHeader(ctx, operationName)
	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}
	header["Authorization"] = "Bearer " + token
	return header, nil
}

// capabilityAuthorizationHeader authenticates a caller-scoped Execution
// Factory request. Public traffic forwards the original bearer token; internal
// traffic carries the trusted account headers already produced by skillHeader.
func (o *operatorIntegrationClient) capabilityAuthorizationHeader(
	ctx context.Context, operationName string,
) (map[string]string, error) {
	return o.capabilityAuthorizationHeaderForAuthMode(ctx, operationName, config.GetAuthEnabled())
}

func (o *operatorIntegrationClient) capabilityAuthorizationHeaderForAuthMode(
	ctx context.Context, operationName string, authEnabled bool,
) (map[string]string, error) {
	if _, ok := common.GetRawTokenFromCtx(ctx); ok {
		return o.callerAuthorizationHeader(ctx, operationName)
	}
	if common.IsPublicAPIFromCtx(ctx) {
		if authEnabled {
			return o.callerAuthorizationHeader(ctx, operationName)
		}
		return o.skillHeader(ctx, operationName), nil
	}
	authContext, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || strings.TrimSpace(authContext.AccountID) == "" || strings.TrimSpace(string(authContext.AccountType)) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerIdentityMissing.Error())
	}
	return o.skillHeader(ctx, operationName), nil
}

func capabilityURI(ctx context.Context, publicURI, internalURI string) string {
	if _, ok := common.GetRawTokenFromCtx(ctx); ok || common.IsPublicAPIFromCtx(ctx) {
		return publicURI
	}
	return internalURI
}

// ExecuteFunction executes code in the sandbox.
func (o *operatorIntegrationClient) ExecuteFunction(
	ctx context.Context, req *interfaces.ExecuteFunctionRequest,
) (*interfaces.ExecuteFunctionResponse, error) {
	if req == nil || req.Code == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "FunctionCodeRequired"))
	}
	if _, hasCallerToken := common.GetRawTokenFromCtx(ctx); !common.IsPublicAPIFromCtx(ctx) && !hasCallerToken {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			ErrInternalCapabilityAuthorizationUnsupported.Error())
	}

	header, err := o.callerAuthorizationHeader(ctx, "operator.function.execute")
	if err != nil {
		return nil, err
	}

	event := req.Event
	if event == nil {
		// Execution Factory requires event; send an empty object when there is no input.
		event = map[string]any{}
	}
	body := map[string]any{"code": req.Code, "language": req.Language, "event": event}
	if req.Timeout > 0 {
		body["timeout"] = req.Timeout
	}
	if req.WorkingDirectory != "" {
		body["working_directory"] = req.WorkingDirectory
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
