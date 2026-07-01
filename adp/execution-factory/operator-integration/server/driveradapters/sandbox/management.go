package sandbox

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/sandbox"
)

type ManagementHandler interface {
	RegisterPrivate(engine *gin.RouterGroup)
	GetHealth(c *gin.Context)
	GetPool(c *gin.Context)
	ListSessions(c *gin.Context)
	GetSessionDetail(c *gin.Context)
}

type managementHandler struct {
	service sandbox.SandboxManagementService
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

func (h *managementHandler) RegisterPrivate(engine *gin.RouterGroup) {
	group := engine.Group("/sandbox")
	group.GET("/health", h.GetHealth)
	group.GET("/pool", h.GetPool)
	group.GET("/sessions", h.ListSessions)
	group.GET("/sessions/:id", h.GetSessionDetail)
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
