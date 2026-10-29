// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

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
// The public API enforces the caller's execute permission on function:adhoc.
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
	if _, ok := common.GetRawTokenFromCtx(ctx); ok {
		return o.callerAuthorizationHeader(ctx, operationName)
	}
	if common.IsPublicAPIFromCtx(ctx) {
		return o.callerAuthorizationHeader(ctx, operationName)
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.FunctionCodeRequired", map[string]any{})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, localizedText)
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
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Request failed, status: %d", code)
		return nil, classifyFunctionExecuteError(ctx, code, err)
	}

	resp := &interfaces.ExecuteFunctionResponse{}
	if err = common.UnmarshalPreciseJSON(utils.ObjectToByte(respBody), resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Unmarshal failed, err: %v", err)
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.FunctionExecutionResponseInvalid", map[string]any{})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, localizedText)
	}
	return resp, nil
}

// Execution Factory error codes returned by the /v1/function/execute gate.
const (
	executionFactoryUseForbidden             = "CommonUseForbidden"
	executionFactoryAuthorizationUnavailable = "CommonAuthorizationUnavailable"
)

// classifyFunctionExecuteError maps an Execution Factory failure on
// /v1/function/execute.
//
// The endpoint's gate separates a denial (403 CommonUseForbidden: the caller lacks
// execute on function:adhoc) from an authorization outage (503
// CommonAuthorizationUnavailable: bkn-safe gave no decision). The code runs in
// neither case, but only a denial is fixed by requesting a grant, so each gets its
// own reason. Both used to reach the caller as the same generic sandbox failure.
// See #1533.
//
// Only the downstream code and localized description are carried through. The
// downstream details and the shared client's transport string can hold internal
// addresses, so they stay in the server log.
func classifyFunctionExecuteError(ctx context.Context, code int, err error) error {
	downstreamCode, description := executionFactoryError(err)
	if code == http.StatusUnprocessableEntity {
		return classifyFunctionValidationError(ctx, err)
	}
	if code == http.StatusGatewayTimeout && downstreamCode == "Public.GatewayTimeout" {
		if timeoutErr := classifyFunctionTimeoutError(ctx, err); timeoutErr != nil {
			return timeoutErr
		}
	}
	switch {
	case code == http.StatusForbidden && isExecutionFactoryCode(downstreamCode, executionFactoryUseForbidden):
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.FunctionExecuteForbidden", map[string]any{})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			downstreamCode+": "+localizedText)
	case code == http.StatusServiceUnavailable &&
		isExecutionFactoryCode(downstreamCode, executionFactoryAuthorizationUnavailable):
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.FunctionAuthorizationUnavailable", map[string]any{})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			downstreamCode+": "+localizedText)
	case code >= http.StatusBadRequest && code < http.StatusInternalServerError &&
		downstreamCode != "" && description != "":
		return infraErr.DefaultHTTPError(ctx, code, downstreamCode+": "+description)
	}
	return skillUpstreamError(ctx, code, "FunctionExecutionRequestFailed", err)
}

// executionFactoryError reads the code and localized description from an
// Execution Factory error body. Both are empty when the body is not its envelope,
// for example a gateway page.
func executionFactoryError(err error) (code, description string) {
	var he *infraErr.DependencyError
	if !errors.As(err, &he) || len(he.Body) == 0 {
		return "", ""
	}
	var envelope struct {
		Code        string `json:"error_code"`
		LegacyCode  string `json:"code"`
		Description string `json:"description"`
	}
	if json.Unmarshal(he.Body, &envelope) != nil {
		return "", ""
	}
	if envelope.Code == "" {
		envelope.Code = envelope.LegacyCode
	}
	return envelope.Code, envelope.Description
}

// isExecutionFactoryCode matches the extension part of a code such as
// "AgentOperatorIntegration.Forbidden.CommonUseForbidden".
func isExecutionFactoryCode(code, extension string) bool {
	return strings.HasSuffix(code, "."+extension)
}

// executionFailureEnvelope decodes only the public fields relevant to execution
// failures. Arbitrary dependency details never become caller-visible metadata.
type executionFailureEnvelope struct {
	Details       json.RawMessage `json:"error_details"`
	LegacyDetails json.RawMessage `json:"details"`
	Metadata      json.RawMessage `json:"metadata"`
}

type executionFailureDetails struct {
	Reason      string `json:"reason"`
	Stage       string `json:"stage"`
	Actual      int    `json:"actual"`
	Max         int    `json:"max"`
	Unit        string `json:"unit"`
	Timeout     int    `json:"timeout_seconds"`
	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`
}

func functionFailureEnvelope(err error) executionFailureEnvelope {
	var he *infraErr.DependencyError
	var envelope executionFailureEnvelope
	if errors.As(err, &he) {
		_ = json.Unmarshal(he.Body, &envelope)
	}
	if len(envelope.Details) == 0 {
		envelope.Details = envelope.LegacyDetails
	}
	return envelope
}

func classifyFunctionValidationError(ctx context.Context, err error) *httperrors.HTTPError {
	envelope := functionFailureEnvelope(err)
	var details executionFailureDetails
	_ = json.Unmarshal(envelope.Details, &details)
	result := infraErr.ExecutionInputValidationError(ctx,
		functionFailureDetail(ctx, "FunctionExecutionInputInvalid", nil))
	result.Metadata = map[string]any{"reason": "invalid_execution_input", "stage": "validation"}
	if details.Reason == "code_too_long" && details.Stage == "validation" &&
		details.Unit == "unicode_characters" && details.Max > 0 && details.Actual > details.Max {
		result.ErrorDetails = functionFailureDetail(ctx, "FunctionCodeTooLong", map[string]any{"Actual": details.Actual, "Max": details.Max})
		result.Metadata["reason"] = "code_too_long"
		result.Metadata["actual"] = details.Actual
		result.Metadata["max"] = details.Max
		result.Metadata["unit"] = details.Unit
	}
	attachFunctionFailureCorrelation(ctx, result.Metadata, details)
	return result
}

var legacyFunctionTimeout = regexp.MustCompile(`^function execution timed out(?: after ([1-9][0-9]*) seconds)?$`)

// Only the known execution timeout envelope is forwarded as 504. A gateway's
// arbitrary 504 body is still a dependency failure, with no retry advice inferred.
func classifyFunctionTimeoutError(ctx context.Context, err error) error {
	envelope := functionFailureEnvelope(err)
	var details executionFailureDetails
	metadataErr := json.Unmarshal(envelope.Metadata, &details)
	if metadataErr != nil || details.Reason != "execution_timeout" || details.Stage != "execution" || details.Timeout < 0 {
		var legacy string
		if json.Unmarshal(envelope.Details, &legacy) != nil {
			return nil
		}
		parts := legacyFunctionTimeout.FindStringSubmatch(legacy)
		if len(parts) != 2 {
			return nil
		}
		seconds := 0
		if parts[1] != "" {
			var parseErr error
			seconds, parseErr = strconv.Atoi(parts[1])
			if parseErr != nil {
				return nil
			}
		}
		details = executionFailureDetails{Reason: "execution_timeout", Stage: "execution", Timeout: seconds}
	}
	result := infraErr.DefaultHTTPError(ctx, http.StatusGatewayTimeout, nil)
	result.ErrorDetails = functionFailureDetail(ctx, "FunctionExecutionTimeoutUnknown", nil)
	result.Metadata = map[string]any{"reason": "execution_timeout", "stage": "execution"}
	if details.Timeout > 0 {
		result.ErrorDetails = functionFailureDetail(ctx, "FunctionExecutionTimeout", map[string]any{"Timeout": details.Timeout})
		result.Metadata["timeout_seconds"] = details.Timeout
	}
	attachFunctionFailureCorrelation(ctx, result.Metadata, details)
	return result
}

func attachFunctionFailureCorrelation(ctx context.Context, metadata map[string]any, details executionFailureDetails) {
	if id := strings.TrimSpace(details.SessionID); common.IsValidCorrelationID(id) {
		metadata["session_id"] = id
	}
	if id := strings.TrimSpace(details.ExecutionID); common.IsValidCorrelationID(id) {
		metadata["execution_id"] = id
	}
	if trace, ok := common.GetTraceContextFromCtx(ctx); ok && common.IsValidBKNRequestID(trace.RequestID) {
		metadata["request_id"] = trace.RequestID
	}
}

// functionFailureDetail uses the deployed shared locale registry without exposing
// a dependency's untrusted description or rendering parameters.
func functionFailureDetail(ctx context.Context, key string, params map[string]any) string {
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail."+key, params)
	if err != nil {
		logger.Errorf("execution failure message rendering failed: %v", err)
	}
	return text
}
