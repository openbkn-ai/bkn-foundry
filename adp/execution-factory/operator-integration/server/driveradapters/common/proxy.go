package common

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/sandbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// UnifiedProxyHandler unified proxy processing interface.
type UnifiedProxyHandler interface {
	FunctionExecuteProxy(c *gin.Context)
	FunctionExecute(c *gin.Context)
	// Deriving parameter definitions from function code.
	FunctionInferSchema(c *gin.Context)
	// Query dependency version from PyPI source.
	QueryPypiVersions(c *gin.Context)
	// Get the list of dependent libraries.
	GetDependencies(c *gin.Context)
}

// unifiedProxyHandler proxy processing implementation.
type unifiedProxyHandler struct {
	Logger          interfaces.Logger
	MetadataService interfaces.IMetadataService
	SessionPool     sandbox.SessionPool
	AuthService     interfaces.IAuthorizationService
}

var (
	pOnce        sync.Once
	proxyHandler UnifiedProxyHandler
)

func NewUnifiedProxyHandler() UnifiedProxyHandler {
	pOnce.Do(func() {
		conf := config.NewConfigLoader()
		proxyHandler = &unifiedProxyHandler{
			Logger:          conf.Logger,
			MetadataService: metadata.NewMetadataService(),
			SessionPool:     sandbox.GetSessionPool(),
			AuthService:     auth.NewAuthServiceImpl(),
		}
	})
	return proxyHandler
}

// FunctionExecute function execution.
//
// This interface executes any code submitted by the caller in the sandbox, so the public interface requires the caller to hold execute on the operator type.
// Permissions (see #345). There has been no authorization determination on the public face before, and any account holding a valid token - including those with empty permission sets.
// Account - you can use this to gain code execution capabilities in the sandbox.
func (h *unifiedProxyHandler) FunctionExecute(c *gin.Context) {
	var err error
	if err = requireOperatorTypePermission(c.Request.Context(), h.AuthService,
		interfaces.AuthOperationTypeExecute); err != nil {
		rest.ReplyError(c, err)
		return
	}
	req := &interfaces.FunctionProxyExecuteCodeReq{}
	if err = c.ShouldBindJSON(req); err != nil {
		err = errors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, errors.ErrExtDebugParamsInvalid,
			fmt.Sprintf("invalid request body, err: %v", err))
		rest.ReplyError(c, err)
		return
	}

	err = defaults.Set(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, fmt.Sprintf("set default value failed, err: %v", err))
		rest.ReplyError(c, err)
		return
	}
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	execReq := &interfaces.ExecuteCodeReq{
		Code:                  req.Code,
		Event:                 req.Event,
		Language:              req.Language,
		Timeout:               req.Timeout,
		EnvVars:               buildFunctionExecutionEnv(req),
		Dependencies:          req.Dependencies,
		PythonPackageIndexURL: req.DependenciesURL,
	}
	resp, err := h.SessionPool.ExecuteCode(c.Request.Context(), execReq)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	h.Logger.Infof("FunctionExecute response summary: %v", summarizeExecutionResponse(resp))
	rest.ReplyOK(c, http.StatusOK, newFunctionExecuteResp(resp))
}

// All keys of the execution context. The sandbox session is pooled and reused, and the value of the previous caller is retained in the container environment.
// The issued env_vars only overwrite keys with the same name - if you miss any one, the function will read someone else's identity.
// Therefore, the full set is issued every time it is executed, and the unknown ones are explicitly blanked.
func executionEnvKeys() []string {
	return []string{
		"source", "task_id", "capability_id", "capability_name",
		"function_version_id", "user_id", "user_name",
		// BKN identity and session context. Must be listed here: newExecutionEnv Rely on this list to put each key.
		// Preset it as an empty string, so as to achieve "the complete set is delivered every time it is executed". Missing one, the previous caller in the pooled session.
		// The token will be left for the next one - and what the token misses is not the mark, but the identity.
		"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID",
	}
}

func newExecutionEnv() map[string]any {
	env := make(map[string]any, len(executionEnvKeys()))
	for _, k := range executionEnvKeys() {
		env[k] = ""
	}
	return env
}

// Deriving the schema will also execute user code, and the identity key must be overwritten as well -.
// If one is sent less, the identity of the previous caller in the pooled container will be read by the user code.
func inferSchemaExecutionEnv() map[string]any {
	env := newExecutionEnv()
	env["source"] = "function_infer_schema"
	return env
}

func buildFunctionProxyExecutionEnv(version string) map[string]any {
	env := newExecutionEnv()
	env["source"] = "function_proxy"
	env["task_id"] = "function_proxy_" + uuid.NewString()
	env["capability_id"] = "function_version:" + version
	env["function_version_id"] = version
	return env
}

// FunctionExecuteResp function execution response.
type FunctionExecuteResp struct {
	Stdout          string `json:"stdout"`                  // standard output.
	Stderr          string `json:"stderr"`                  // standard error output.
	Result          any    `json:"result"`                  // execution result value.
	Metrics         any    `json:"metrics"`                 // Execution metrics.
	ExitCode        int    `json:"exit_code"`               // Exit code, 0 indicates success.
	ErrorMessage    string `json:"error_message,omitempty"` // Sandbox side error message.
	ExecutionTimeMS int64  `json:"execution_time_ms"`       // Execution time, unit milliseconds.
	Artifacts       any    `json:"artifacts,omitempty"`     // Documentation.
	SessionID       string `json:"session_id,omitempty"`    // Sandbox session ID for easy troubleshooting.
}

// newFunctionExecuteResp converts the sandbox execution result into an external response.
// The sandbox itself returns information such as exit code, time consumption, and artifacts. These are as critical as stdout/stderr when debugging functions.
// So the whole thing is revealed instead of just keeping the output stream.
func newFunctionExecuteResp(resp *interfaces.ExecuteCodeResp) *FunctionExecuteResp {
	return &FunctionExecuteResp{
		Stdout:          resp.Stdout,
		Stderr:          resp.Stderr,
		Result:          resp.ReturnValue,
		Metrics:         resp.Metrics,
		ExitCode:        resp.ExitCode,
		ErrorMessage:    resp.ErrorMessage,
		ExecutionTimeMS: resp.ExecutionTime,
		Artifacts:       resp.Artifacts,
		SessionID:       resp.SessionID,
	}
}

func buildFunctionExecutionEnv(req *interfaces.FunctionProxyExecuteCodeReq) map[string]any {
	env := newExecutionEnv()
	env["source"] = "function_debug"
	if req == nil {
		return env
	}
	if req.Source != "" {
		env["source"] = req.Source
	}
	if req.TaskID != "" {
		env["task_id"] = req.TaskID
	}
	if req.CapabilityID != "" {
		env["capability_id"] = req.CapabilityID
	}
	if req.CapabilityName != "" {
		env["capability_name"] = req.CapabilityName
	}
	if req.UserID != "" {
		env["user_id"] = req.UserID
	}
	if req.UserName != "" {
		env["user_name"] = req.UserName
	}
	// BKN context. The key name is capitalized and prefixed to distinguish it from the user_id tracking tags - in those comments.
	// It is clearly written "only used as a tracking mark, not involved in authentication", and bkn_token is a real credential and should not be mixed into the same naming style.
	// Unconditional assignment, if not passed, it will be an empty string: these three keys have been preset in executionEnvKeys, and conditional writing will cause.
	// The unpassed one leaves the value of the previous caller. An empty string is equivalent to not configured on the sandbox side.
	env["BKN_TOKEN"] = req.BKNToken
	env["BKN_CONVERSATION_ID"] = req.BKNConversationID
	env["BKN_INTERACTION_ID"] = req.BKNInteractionID
	return env
}

// FunctionExecuteProxyReq function execution proxy request parameters.
type FunctionExecuteProxyReq struct {
	Version string `uri:"version" validate:"required,uuid4"`
	Timeout int64  `query:"timeout"` // milliseconds.
}

// FunctionExecuteProxy executes proxy requests.
func (h *unifiedProxyHandler) FunctionExecuteProxy(c *gin.Context) {
	var err error
	req := &FunctionExecuteProxyReq{}
	if err = c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	// Read request body.
	event := map[string]any{}
	if err = c.ShouldBindJSON(&event); err != nil {
		err = errors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, errors.ErrExtDebugParamsInvalid,
			fmt.Sprintf("invalid request body, err: %v", err))
		rest.ReplyError(c, err)
		return
	}
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Get metadata.
	exists, metadata, err := h.MetadataService.CheckMetadataExists(c.Request.Context(), interfaces.MetadataTypeFunc, req.Version)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	if !exists {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusNotFound, fmt.Sprintf("metadata %s not found", req.Version))
		rest.ReplyError(c, err)
		return
	}

	// Execute function.
	scriptType := metadata.GetScriptType()
	if scriptType != string(interfaces.ScriptTypePython) {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, fmt.Sprintf("script_type %s not supported", scriptType))
		rest.ReplyError(c, err)
		return
	}
	code := metadata.GetCode()
	if code == "" {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, fmt.Sprintf("function code is empty for version %s", req.Version))
		rest.ReplyError(c, err)
		return
	}
	dependencies := []*interfaces.DependencyInfo{}
	if metadata.GetDependencies() != "" {
		dependencies = utils.JSONToObject[[]*interfaces.DependencyInfo](metadata.GetDependencies())
	}
	execReq := &interfaces.ExecuteCodeReq{
		Code:                  code,
		Event:                 event,
		Timeout:               int(req.Timeout / 1000),
		Language:              scriptType,
		EnvVars:               buildFunctionProxyExecutionEnv(req.Version),
		Dependencies:          dependencies,
		PythonPackageIndexURL: metadata.GetDependenciesURL(),
	}
	if err = defaults.Set(execReq); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, fmt.Sprintf("set default value failed, err: %v", err))
		rest.ReplyError(c, err)
		return
	}
	resp, err := h.SessionPool.ExecuteCode(c.Request.Context(), execReq)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	h.Logger.Infof("FunctionExecuteProxy response summary: %v", summarizeExecutionResponse(resp))
	rest.ReplyOK(c, http.StatusOK, newFunctionExecuteResp(resp))
}

func summarizeExecutionResponse(resp *interfaces.ExecuteCodeResp) map[string]any {
	summary := map[string]any{
		"stdout_length": 0,
		"stderr_length": 0,
	}
	if resp == nil {
		return summary
	}
	summary["stdout_length"] = len(resp.Stdout)
	summary["stderr_length"] = len(resp.Stderr)
	if resp.Stdout != "" {
		sum := sha256.Sum256([]byte(resp.Stdout))
		summary["stdout_hash"] = fmt.Sprintf("sha256:%x", sum[:])
	}
	if resp.Stderr != "" {
		sum := sha256.Sum256([]byte(resp.Stderr))
		summary["stderr_hash"] = fmt.Sprintf("sha256:%x", sum[:])
	}
	return summary
}

// QueryPyPIVersions Query PyPI dependency version.
func (h *unifiedProxyHandler) QueryPypiVersions(c *gin.Context) {
	req := &sandbox.ParsePypiReq{}
	if err := c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	if err := c.ShouldBindQuery(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	if err := defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err := validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	resp, err := sandbox.ParsePypi(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// GetDependencies Get the list of dependent libraries.
func (h *unifiedProxyHandler) GetDependencies(c *gin.Context) {
	var err error
	resp, err := h.SessionPool.GetDependencies(c.Request.Context())
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// FunctionInferSchemaReq request to derive parameter definitions from function code.
type FunctionInferSchemaReq struct {
	Code string `json:"code" validate:"required"` // User function code.
}

// FunctionInferSchemaResp derivation result. Supported is false when the code does not use @tool.
// The remaining fields are empty, and the caller falls back to filling them in manually.
type FunctionInferSchemaResp struct {
	Supported   bool                       `json:"supported"`             // Whether the @tool function is derived.
	Name        string                     `json:"name,omitempty"`        // function name.
	Description string                     `json:"description,omitempty"` // Taken from docstring.
	Inputs      []*interfaces.ParameterDef `json:"inputs,omitempty"`      // Input parameter definition.
	Outputs     []*interfaces.ParameterDef `json:"outputs,omitempty"`     // Output parameter definition.
}

// Probe code: Attached to the user code, obtain its registered schema from the SDK.
//
// Print and exit directly at the module level without defining the handler: when the user code contains @tool, the wrapper will exit.
// dispatch branch to call the user function. If the probe is written in handler form, it will not be executed at all, and dispatch.
// It will fail due to lack of business input parameters.
//
// Results are wrapped by tag. There are two ways to extract the isolation layer: subprocess takes the last legal JSON line in stdout,
// bwrap and macseatbelt only recognize ===SANDBOX_RESULT=== tags. Naked printing cannot get the value under the latter.
// The derivation will always be supported:false - if marked, both will be hit (the marked line itself is not legal JSON,
// Does not affect the judgment of the last line of JSON).
const inferSchemaProbe = `

import json as _bkn_json, sys as _bkn_sys
try:
    import sandbox_sdk as _bkn_sdk
    _bkn_schema = _bkn_sdk.export_schema()
    _bkn_out = {
        "supported": True,
        "name": _bkn_schema.get("name", ""),
        "description": _bkn_schema.get("description", ""),
        "inputs": _bkn_schema.get("inputs", []),
        "outputs": _bkn_schema.get("outputs", []),
    } if _bkn_schema else {"supported": False}
except Exception:
    _bkn_out = {"supported": False}
print("===SANDBOX_RESULT===")
print(_bkn_json.dumps(_bkn_out))
print("===SANDBOX_RESULT_END===")
_bkn_sys.exit(0)
`

// FunctionInferSchema derives parameter definitions from function code.
//
// The parameter definition originally required the user to fill it out again on the interface, but the signature, type annotation and docstring of the @tool function.
// The same information has already been described. Here, the user code is executed in the sandbox and its registered schema is obtained from the SDK.
// Make the signature the only source of truth.
//
// Executing user code means the same capability as FunctionExecute, so the same set of execute authorizations is used.
func (h *unifiedProxyHandler) FunctionInferSchema(c *gin.Context) {
	ctx := c.Request.Context()
	if err := requireOperatorTypePermission(ctx, h.AuthService,
		interfaces.AuthOperationTypeExecute); err != nil {
		rest.ReplyError(c, err)
		return
	}
	req := &FunctionInferSchemaReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtDebugParamsInvalid,
			fmt.Sprintf("invalid request body, err: %v", err)))
		return
	}
	if err := validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	resp, err := h.SessionPool.ExecuteCode(ctx, &interfaces.ExecuteCodeReq{
		Code:     req.Code + inferSchemaProbe,
		Event:    map[string]any{},
		Language: string(interfaces.ScriptTypePython),
		EnvVars:  inferSchemaExecutionEnv(),
	})
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	// There are syntax errors in the user code itself or the result cannot be deduced when the import fails. This is not a server-side failure.
	// Press "Unable to deduce" to return, allowing the caller to fall back to manual filling.
	if resp.ExitCode != 0 || resp.ReturnValue == nil {
		h.Logger.WithContext(ctx).Infof("infer schema produced no result, exit_code: %d, stderr: %s",
			resp.ExitCode, resp.Stderr)
		rest.ReplyOK(c, http.StatusOK, &FunctionInferSchemaResp{Supported: false})
		return
	}

	result := &FunctionInferSchemaResp{}
	if err = utils.StringToObject(utils.ObjectToJSON(resp.ReturnValue), result); err != nil {
		h.Logger.WithContext(ctx).Errorf("decode infer schema result failed, err: %v", err)
		rest.ReplyOK(c, http.StatusOK, &FunctionInferSchemaResp{Supported: false})
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}
