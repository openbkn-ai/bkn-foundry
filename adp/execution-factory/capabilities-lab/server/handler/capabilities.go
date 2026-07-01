// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/config"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/logic"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/observability"
)

type CapabilitiesHandler struct {
	Service               *logic.Service
	DefaultBusinessDomain string
	Features              config.FeatureFlags
	ServiceVersion        string
	MetricsEnabled        bool
	MetricsCollector      *observability.Metrics
}

func NewCapabilitiesHandler(cfg config.Config, service *logic.Service, metrics *observability.Metrics) *CapabilitiesHandler {
	return &CapabilitiesHandler{
		Service:               service,
		DefaultBusinessDomain: cfg.DefaultBusinessDomain,
		Features:              cfg.Features,
		ServiceVersion:        cfg.ServiceVersion,
		MetricsCollector:      metrics,
		MetricsEnabled:        cfg.MetricsEnabled,
	}
}

func (h *CapabilitiesHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/health", h.Health)
	group.GET("/meta", h.Meta)
	if h.MetricsEnabled {
		group.GET("/metrics", h.Metrics)
	}
	group.GET("/capabilities", h.ListCapabilities)
	group.POST("/capabilities/http", h.CreateHttpCapability)
	group.POST("/capabilities/http/import", h.ImportOpenApiCapabilities)
	group.POST("/capabilities/mcp", h.RegisterMcpCapability)
	group.POST("/capabilities/skill", h.RegisterSkillCapability)
	group.GET("/groups", h.ListGroups)
	group.GET("/categories", h.ListCategories)
	group.POST("/groups/:group_id/publish", h.PublishGroup)
	group.POST("/capabilities/import", h.ImportCapabilityPackage)
	group.GET("/catalog", h.ListCatalog)
	group.POST("/catalog/install", h.InstallFromCatalog)
	group.POST("/capabilities/function", h.CreateFunctionCapability)
	group.POST("/function/execute", h.ExecutePython)
	group.GET("/template/python", h.GetPythonTemplate)
	group.POST("/capabilities/mcp/parse-sse", h.ParseMcpSse)

	cap := group.Group("/capabilities/:id")
	cap.GET("/versions", h.ListVersions)
	cap.GET("/orchestration", h.GetOrchestration)
	cap.POST("/debug", h.DebugCapability)
	cap.POST("/versions/republish", h.RepublishVersion)
	cap.POST("/publish", h.PublishCapability)
	cap.POST("/orchestration/enable", h.EnableOrchestration)
	cap.POST("/orchestration/config", h.UpdateOrchestrationConfig)
	cap.POST("/orchestration/disable", h.DisableOrchestration)
	cap.GET("/export", h.ExportCapability)
	cap.GET("/skill/content", h.GetSkillContent)
	cap.POST("/skill/files/read", h.ReadSkillFile)
	cap.GET("/skill/download", h.DownloadSkillPackage)
	cap.PUT("/skill/package", h.UpdateSkillPackage)
	cap.GET("/mcp/tools", h.ListMcpTools)
	cap.PATCH("", h.UpdateCapability)
	cap.DELETE("", h.DeleteCapability)
	cap.GET("", h.GetCapability)
}

func (h *CapabilitiesHandler) Health(c *gin.Context) {
	upstream := "unknown"
	if err := h.Service.Client.Ping(c.Request.Context()); err != nil {
		upstream = "down"
	} else {
		upstream = "ok"
	}

	status := "ok"
	httpStatus := http.StatusOK
	if upstream != "ok" {
		status = "degraded"
	}

	c.JSON(httpStatus, gin.H{
		"status":   status,
		"service":  "capabilities-lab",
		"upstream": upstream,
	})
}

func (h *CapabilitiesHandler) ListCapabilities(c *gin.Context) {
	bd := h.businessDomain(c)
	page := queryInt(c, "page", 1)
	pageSize := queryInt(c, "page_size", 20)

	resp, err := h.Service.ListCapabilities(
		c.Request.Context(),
		bd,
		c.Query("kind"),
		c.Query("keyword"),
		c.Query("group_id"),
		c.Query("status"),
		page,
		pageSize,
	)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) GetCapability(c *gin.Context) {
	bd := h.businessDomain(c)
	resp, err := h.Service.GetCapability(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeNotFound(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) ListGroups(c *gin.Context) {
	bd := h.businessDomain(c)
	page := queryInt(c, "page", 1)
	pageSize := queryInt(c, "page_size", 50)

	resp, err := h.Service.ListGroups(c.Request.Context(), bd, c.Query("keyword"), page, pageSize)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) CreateHttpCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.CreateHttpCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	if req.Group.Mode == "" {
		req.Group.Mode = "auto"
	}

	resp, err := h.Service.CreateHttpCapability(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func (h *CapabilitiesHandler) ImportOpenApiCapabilities(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.ImportOpenApiCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	if req.Group.Mode == "" {
		req.Group.Mode = "auto"
	}

	resp, err := h.Service.ImportOpenApiCapabilities(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func (h *CapabilitiesHandler) DebugCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.DebugCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.DebugCapability(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) ListVersions(c *gin.Context) {
	bd := h.businessDomain(c)

	resp, err := h.Service.ListVersions(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) RepublishVersion(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.RepublishVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	if err := h.Service.RepublishVersion(c.Request.Context(), bd, c.Param("id"), req); err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CapabilitiesHandler) PublishCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.PublishCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	status := req.Status
	if status == "" {
		status = "published"
	}

	if err := h.Service.PublishCapability(c.Request.Context(), bd, c.Param("id"), status); err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "status": status})
}

func (h *CapabilitiesHandler) EnableOrchestration(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.EnableOrchestrationRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.EnableOrchestration(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) DisableOrchestration(c *gin.Context) {
	bd := h.businessDomain(c)

	resp, err := h.Service.DisableOrchestration(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) GetOrchestration(c *gin.Context) {
	bd := h.businessDomain(c)

	resp, err := h.Service.GetOrchestrationDetail(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) UpdateOrchestrationConfig(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.UpdateOrchestrationConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.UpdateOrchestrationConfig(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) UpdateHttpCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.UpdateHttpCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.UpdateHttpCapability(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) DeleteCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	if err := h.Service.DeleteCapability(c.Request.Context(), bd, c.Param("id")); err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CapabilitiesHandler) RegisterMcpCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.RegisterMcpCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.RegisterMcpCapability(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"capability": resp})
}

func (h *CapabilitiesHandler) RegisterSkillCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		writeBadRequest(c, "file is required")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	req := model.RegisterSkillCapabilityRequest{
		FileType: c.PostForm("file_type"),
		Category: c.PostForm("category"),
		Source:   c.PostForm("source"),
		Filename: fileHeader.Filename,
		Content:  content,
		MimeType: mimeType,
	}

	resp, err := h.Service.RegisterSkillCapability(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"capability": resp})
}

func (h *CapabilitiesHandler) PublishGroup(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.PublishCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	status := req.Status
	if status == "" {
		status = "published"
	}

	if err := h.Service.PublishGroup(c.Request.Context(), bd, c.Param("group_id"), status); err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "status": status})
}

func (h *CapabilitiesHandler) ExportCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	payload, componentType, err := h.Service.ExportCapability(
		c.Request.Context(),
		bd,
		c.Param("id"),
	)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header(
		"Content-Disposition",
		fmt.Sprintf("attachment; filename=%s_export.adp.json", componentType),
	)
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *CapabilitiesHandler) ImportCapabilityPackage(c *gin.Context) {
	bd := h.businessDomain(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		writeBadRequest(c, "file is required")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.ImportCapabilityPackage(
		c.Request.Context(),
		bd,
		c.PostForm("type"),
		c.PostForm("mode"),
		content,
	)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func (h *CapabilitiesHandler) ListCatalog(c *gin.Context) {
	bd := h.businessDomain(c)
	page := queryInt(c, "page", 1)
	pageSize := queryInt(c, "page_size", 20)

	resp, err := h.Service.ListCatalog(
		c.Request.Context(),
		bd,
		c.Query("kind"),
		c.Query("keyword"),
		page,
		pageSize,
	)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) InstallFromCatalog(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.InstallCatalogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.InstallFromCatalog(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func (h *CapabilitiesHandler) CreateFunctionCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.CreateFunctionCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.CreateFunctionCapability(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func (h *CapabilitiesHandler) ExecutePython(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.ExecutePythonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.ExecutePython(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) GetPythonTemplate(c *gin.Context) {
	bd := h.businessDomain(c)

	template, err := h.Service.GetPythonTemplate(c.Request.Context(), bd)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, model.PythonTemplateResponse{Template: template})
}

func (h *CapabilitiesHandler) ParseMcpSse(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.ParseMcpSseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.ParseMcpSse(c.Request.Context(), bd, req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) GetSkillContent(c *gin.Context) {
	bd := h.businessDomain(c)

	resp, err := h.Service.GetSkillContent(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) ReadSkillFile(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.ReadSkillFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.ReadSkillFile(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) businessDomain(c *gin.Context) string {
	if bd := c.GetHeader("x-business-domain"); bd != "" {
		return bd
	}

	return h.DefaultBusinessDomain
}

func queryInt(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}

	return value
}
