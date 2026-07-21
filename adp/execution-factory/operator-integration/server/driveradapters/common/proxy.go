package common

import (
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
	h.Logger.Infof("FunctionExecute resp: %v", resp)
	result := &FunctionExecuteResp{
		Stdout:  resp.Stdout,
		Stderr:  resp.Stderr,
		Result:  resp.ReturnValue,
		Metrics: resp.Metrics,
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func buildFunctionProxyExecutionEnv(version string) map[string]any {
	return map[string]any{
		"source":              "function_proxy",
		"task_id":             "function_proxy_" + uuid.NewString(),
		"capability_id":       "function_version:" + version,
		"function_version_id": version,
	}
}

// FunctionExecuteResp 函数执行响应
type FunctionExecuteResp struct {
	Stdout  string `json:"stdout"`  // 标准输出
	Stderr  string `json:"stderr"`  // 标准错误输出
	Result  any    `json:"result"`  // 执行结果值
	Metrics any    `json:"metrics"` // 执行指标
}

func buildFunctionExecutionEnv(req *interfaces.FunctionProxyExecuteCodeReq) map[string]any {
	env := map[string]any{"source": "function_debug"}
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
	h.Logger.Infof("FunctionExecuteProxy resp: %v", resp)
	// 转换为 FunctionExecuteResp
	result := &FunctionExecuteResp{
		Stdout:  resp.Stdout,
		Stderr:  resp.Stderr,
		Result:  resp.ReturnValue,
		Metrics: resp.Metrics,
	}
	rest.ReplyOK(c, http.StatusOK, result)
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
