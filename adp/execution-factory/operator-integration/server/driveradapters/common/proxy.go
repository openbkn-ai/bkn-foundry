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
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/sandbox"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
)

// UnifiedProxyHandler 统一代理处理接口
type UnifiedProxyHandler interface {
	FunctionExecuteProxy(c *gin.Context)
	FunctionExecute(c *gin.Context)
	// 从函数代码推导参数定义
	FunctionInferSchema(c *gin.Context)
	// 从Pypi源查询依赖库版本
	QueryPypiVersions(c *gin.Context)
	// 获取依赖库列表
	GetDependencies(c *gin.Context)
}

// unifiedProxyHandler 代理处理实现
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

// FunctionExecute 函数执行
//
// 该接口在沙箱中执行调用方提交的任意代码，因此在公开面要求调用方在算子类型上持有 execute
// 权限（见 #345）。此前公开面无任何授权判定，任何持有有效令牌的账号——包括权限集为空的
// 账号——都可借此获得沙箱内的代码执行能力。
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

// 执行上下文的全部键。沙箱会话是池化复用的,容器环境里留着上一个调用方的值,
// 而下发的 env_vars 只是覆盖同名键 —— 漏掉哪个,函数读到的就是别人的身份。
// 所以每次执行都下发全套,未知的显式置空。
func executionEnvKeys() []string {
	return []string{
		"source", "task_id", "capability_id", "capability_name",
		"function_version_id", "user_id", "user_name",
	}
}

func newExecutionEnv() map[string]any {
	env := make(map[string]any, len(executionEnvKeys()))
	for _, k := range executionEnvKeys() {
		env[k] = ""
	}
	return env
}

// 推导 schema 同样会执行用户代码,身份键必须一并覆盖 ——
// 少发一个,池化容器里上一个调用方的身份就会被用户代码读到。
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

// FunctionExecuteResp 函数执行响应
type FunctionExecuteResp struct {
	Stdout          string `json:"stdout"`                     // 标准输出
	Stderr          string `json:"stderr"`                     // 标准错误输出
	Result          any    `json:"result"`                     // 执行结果值
	Metrics         any    `json:"metrics"`                    // 执行指标
	ExitCode        int    `json:"exit_code"`                  // 退出码,0 表示成功
	ErrorMessage    string `json:"error_message,omitempty"`    // 沙箱侧错误信息
	ExecutionTimeMS int64  `json:"execution_time_ms"`          // 执行耗时,单位毫秒
	Artifacts       any    `json:"artifacts,omitempty"`        // 文件制品
	SessionID       string `json:"session_id,omitempty"`       // 沙箱会话ID,便于排障
}

// newFunctionExecuteResp 把沙箱执行结果转成对外响应。
// 沙箱本身返回了退出码、耗时、制品等信息,调试函数时这些和 stdout/stderr 同样关键,
// 因此整体透出而不是只保留输出流。
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
	return env
}

// FunctionExecuteProxyReq 函数执行代理请求参数
type FunctionExecuteProxyReq struct {
	Version string `uri:"version" validate:"required,uuid4"`
	Timeout int64  `query:"timeout"` // 毫秒
}

// FunctionExecuteProxy 执行代理请求
func (h *unifiedProxyHandler) FunctionExecuteProxy(c *gin.Context) {
	var err error
	req := &FunctionExecuteProxyReq{}
	if err = c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	// 读取请求体
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

	// 获取元数据
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

	// 执行函数
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

// QueryPypiVersions 查询Pypi依赖库版本
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

// GetDependencies 获取依赖库列表
func (h *unifiedProxyHandler) GetDependencies(c *gin.Context) {
	var err error
	resp, err := h.SessionPool.GetDependencies(c.Request.Context())
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// FunctionInferSchemaReq 从函数代码推导参数定义的请求
type FunctionInferSchemaReq struct {
	Code string `json:"code" validate:"required"` // 用户函数代码
}

// FunctionInferSchemaResp 推导结果。代码未使用 @tool 时 Supported 为 false，
// 其余字段为空，调用方据此回退到手工填写。
type FunctionInferSchemaResp struct {
	Supported   bool                      `json:"supported"`             // 是否推导出了 @tool 函数
	Name        string                    `json:"name,omitempty"`        // 函数名
	Description string                    `json:"description,omitempty"` // 取自 docstring
	Inputs      []*interfaces.ParameterDef `json:"inputs,omitempty"`     // 输入参数定义
	Outputs     []*interfaces.ParameterDef `json:"outputs,omitempty"`    // 输出参数定义
}

// 探针代码：附在用户代码之后，向 SDK 取它登记的 schema。
//
// 在模块级直接打印并退出，不定义 handler：用户代码带 @tool 时，wrapper 会走
// dispatch 分支去调用用户函数，探针若写成 handler 形态根本不会被执行，而 dispatch
// 会因为缺少业务入参而失败。
//
// 结果按标记包裹。隔离层有两种提取方式：subprocess 取 stdout 里最后一个合法 JSON 行，
// bwrap 与 macseatbelt 只认 ===SANDBOX_RESULT=== 标记。裸打印在后者下取不到值，
// 推导会恒为 supported:false —— 带上标记则两种都命中（标记行本身不是合法 JSON，
// 不影响末行 JSON 的判定）。
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

// FunctionInferSchema 从函数代码推导参数定义。
//
// 参数定义本来要用户在界面上再填一遍,而 @tool 函数的签名、类型注解和 docstring
// 已经描述了同样的信息。这里在沙箱中执行用户代码,向 SDK 取它登记的 schema,
// 让签名成为唯一事实源。
//
// 执行用户代码意味着与 FunctionExecute 同等的能力,因此沿用同一套 execute 授权。
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
	// 用户代码本身有语法错误或 import 失败时推导不出结果,这不是服务端故障,
	// 按「无法推导」返回,让调用方回退到手工填写。
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
