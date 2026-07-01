// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func (h *CapabilitiesHandler) ListCategories(c *gin.Context) {
	items, err := h.Service.ListCategories(c.Request.Context(), h.businessDomain(c))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	response := make([]model.CategoryEntry, 0, len(items))
	for _, item := range items {
		response = append(response, model.CategoryEntry{
			CategoryType: item.CategoryType,
			Name:         item.Name,
		})
	}

	c.JSON(http.StatusOK, model.CategoryListResponse{Data: response})
}

func (h *CapabilitiesHandler) UpdateCapability(c *gin.Context) {
	bd := h.businessDomain(c)

	var req model.UpdateCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	resp, err := h.Service.UpdateCapability(c.Request.Context(), bd, c.Param("id"), req)
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *CapabilitiesHandler) DownloadSkillPackage(c *gin.Context) {
	bd := h.businessDomain(c)
	payload, filename, err := h.Service.DownloadSkillPackage(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Data(http.StatusOK, "application/octet-stream", payload)
}

func (h *CapabilitiesHandler) UpdateSkillPackage(c *gin.Context) {
	bd := h.businessDomain(c)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeBadRequest(c, "file is required")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	fileType := c.PostForm("file_type")
	if fileType == "" {
		fileType = "zip"
	}

	resp, err := h.Service.UpdateSkillPackage(c.Request.Context(), bd, c.Param("id"), model.RegisterSkillCapabilityRequest{
		FileType: fileType,
		Filename: header.Filename,
		Content:  content,
		MimeType: header.Header.Get("Content-Type"),
	})
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"capability": resp})
}

func (h *CapabilitiesHandler) ListMcpTools(c *gin.Context) {
	bd := h.businessDomain(c)
	tools, err := h.Service.ListMcpTools(c.Request.Context(), bd, c.Param("id"))
	if err != nil {
		writeBadGateway(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"tools": tools})
}
