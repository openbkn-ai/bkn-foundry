package sandbox

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/sandbox"
)

type ManagementHandler interface {
	RegisterPublic(engine *gin.RouterGroup)
	GetHealth(c *gin.Context)
	GetPool(c *gin.Context)
	ListSessions(c *gin.Context)
	GetSessionDetail(c *gin.Context)
}

type managementHandler struct {
	service     sandbox.SandboxManagementService
	authService interfaces.IAuthorizationService
}

func NewManagementHandler() ManagementHandler {
	return &managementHandler{
		service: sandbox.NewSandboxManagementService(
			drivenadapters.NewSandBoxControlPlaneClient(),
			sandbox.GetSessionPool(),
		),
	}
}

func NewManagementHandlerWithService(service sandbox.SandboxManagementService) ManagementHandler {
	return &managementHandler{service: service}
}

// NewManagementHandlerWithAuth 供测试注入授权服务。
func NewManagementHandlerWithAuth(service sandbox.SandboxManagementService, authService interfaces.IAuthorizationService) ManagementHandler {
	return &managementHandler{service: service, authService: authService}
}

// RegisterPublic 在公开面注册沙箱只读观测接口。
//
// 这四条接口原本只在 internal-v1 上，而 internal-v1 不校验令牌、身份由 X-Account-ID 头
// 声明；为了让 Studio 的沙箱运行时页能访问，该前缀被开到了 Ingress（见 #326）。公开面走
// middlewareIntrospectVerify，可拿到经校验的真实身份，再叠加超管判定收口。
//
// 响应含 user_id、workspace_path、pod_name、python_package_index_url 等跨租户信息，
// 因此限定超管可见。
func (h *managementHandler) RegisterPublic(engine *gin.RouterGroup) {
	// 惰性构造：授权服务会加载全局配置，放在构造函数里会让只注册内部面的调用方
	// （含单元测试）也被迫依赖配置文件。路由注册发生在启动期且仅一次，此处无并发。
	if h.authService == nil {
		h.authService = auth.NewAuthServiceImpl()
	}
	group := engine.Group("/sandbox", h.requireAdmin)
	group.GET("/health", h.GetHealth)
	group.GET("/pool", h.GetPool)
	group.GET("/sessions", h.ListSessions)
	group.GET("/sessions/:id", h.GetSessionDetail)
}

// requireAdmin 拦截非超管调用。判定口径与 bkn-safe 的 Enforcer.CanAdmin 一致。
func (h *managementHandler) requireAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	authContext, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || authContext == nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "authentication required"))
		c.Abort()
		return
	}
	accessor := &interfaces.AuthAccessor{
		ID:   authContext.AccountID,
		Type: authContext.AccountType,
	}
	if err := h.authService.CheckAdminPermission(ctx, accessor); err != nil {
		rest.ReplyError(c, err)
		c.Abort()
		return
	}
	c.Next()
}

func (h *managementHandler) GetHealth(c *gin.Context) {
	resp, err := h.service.GetHealth(c.Request.Context())
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

func (h *managementHandler) GetPool(c *gin.Context) {
	resp, err := h.service.GetPool(c.Request.Context())
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

func (h *managementHandler) ListSessions(c *gin.Context) {
	req := &sandbox.SandboxSessionListReq{}
	if err := c.ShouldBindQuery(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	resp, err := h.service.ListSessions(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

func (h *managementHandler) GetSessionDetail(c *gin.Context) {
	resp, err := h.service.GetSessionDetail(c.Request.Context(), c.Param("id"))
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}
