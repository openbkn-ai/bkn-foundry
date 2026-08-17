package handler

import (
	"oss-gateway/internal/service"
	"oss-gateway/pkg/response"

	"github.com/gin-gonic/gin"
)

type StorageHandler struct {
	service service.StorageService
}

func NewStorageHandler(service service.StorageService) *StorageHandler {
	return &StorageHandler{service: service}
}

func (h *StorageHandler) Create(c *gin.Context) {
	var req service.CreateStorageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	storageID, err := h.service.Create(c.Request.Context(), &req)
	if err != nil {
		if writeValidationError(c, err) {
			return
		}
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, map[string]string{
		"id":     storageID,
		"status": "ok",
	})
}

func (h *StorageHandler) Update(c *gin.Context) {
	storageID := c.Param("id")

	var req service.UpdateStorageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := h.service.Update(c.Request.Context(), storageID, &req); err != nil {
		if writeValidationError(c, err) {
			return
		}
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, map[string]string{
		"status": "ok",
		"id":     storageID,
	})
}

func (h *StorageHandler) Delete(c *gin.Context) {
	storageID := c.Param("id")

	if err := h.service.Delete(c.Request.Context(), storageID); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, map[string]string{
		"status": "ok",
		"id":     storageID,
	})
}

func (h *StorageHandler) Get(c *gin.Context) {
	storageID := c.Param("id")

	storage, err := h.service.Get(c.Request.Context(), storageID)
	if err != nil {
		response.StorageNotFound(c)
		return
	}

	response.Success(c, storage)
}

func (h *StorageHandler) List(c *gin.Context) {
	var req service.ListStorageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	result, err := h.service.List(c.Request.Context(), &req)
	if err != nil {
		if writeValidationError(c, err) {
			return
		}
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithCount(c, result.Data, result.Count)
}

func (h *StorageHandler) CheckConnection(c *gin.Context) {
	storageID := c.Param("id")

	if err := h.service.CheckConnection(c.Request.Context(), storageID); err != nil {
		if writeValidationError(c, err) {
			return
		}
		response.ConnectionFailed(c, err.Error())
		return
	}

	response.Success(c, map[string]bool{
		"connected": true,
	})
}
