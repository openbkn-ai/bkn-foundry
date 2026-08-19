package sandbox

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/sandbox"
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

// NewManagementHandlerWithAuth is used for testing to inject authorization services.
func NewManagementHandlerWithAuth(service sandbox.SandboxManagementService, authService interfaces.IAuthorizationService) ManagementHandler {
	return &managementHandler{service: service, authService: authService}
}

// RegisterPublic registers the sandbox read-only observation interface on the public side.
//
// These four interfaces were originally only on internal-v1, and internal-v1 does not verify tokens, and the identity is determined by the X-Account-ID header.
// Disclaimer; this prefix is ​​opened to Ingress in order to make it accessible to Studio's sandbox runtime page (see #326). Walk in public.
// middlewareIntrospectVerify can obtain the verified true identity, and then superimpose the super-control judgment closure.
//
// The response contains cross-tenant information such as user_id, workspace_path, pod_name, python_package_index_url, etc.
// Therefore limit the visibility of super pipes.
func (h *managementHandler) RegisterPublic(engine *gin.RouterGroup) {
	// Lazy construction: The authorization service will load the global configuration, and placing it in the constructor will only register internal callers.
	// (including unit tests) are also forced to rely on configuration files. Route registration occurs only once during startup, there is no concurrency here.
	if h.authService == nil {
		h.authService = auth.NewAuthServiceImpl()
	}
	group := engine.Group("/sandbox", h.requireAdmin)
	group.GET("/health", h.GetHealth)
	group.GET("/pool", h.GetPool)
	group.GET("/sessions", h.ListSessions)
	group.GET("/sessions/:id", h.GetSessionDetail)
}

// requireAdmin intercepts non-supervisory calls. The judgment semantics is consistent with bkn-safe’s Enforcer.CanAdmin.
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
