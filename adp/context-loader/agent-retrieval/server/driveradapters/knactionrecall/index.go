// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knactionrecall provides HTTP handler for knowledge network action recall operations.
package knactionrecall

import (
	"net/http"
	"sync"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	validator "github.com/go-playground/validator/v10"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsKAR "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knactionrecall"
)

// KnActionRecallHandler Business knowledge network action recall handler.
type KnActionRecallHandler interface {
	GetActionInfo(c *gin.Context)
	ExecuteAction(c *gin.Context)
	GetActionExecution(c *gin.Context)
	ListActionExecutions(c *gin.Context)
}

type knActionRecallHandler struct {
	Logger                interfaces.Logger
	KnActionRecallService interfaces.IKnActionRecallService
}

var (
	karOnce    sync.Once
	karHandler KnActionRecallHandler
)

// NewKnActionRecallHandler New KnActionRecallHandler.
func NewKnActionRecallHandler() KnActionRecallHandler {
	karOnce.Do(func() {
		conf := config.NewConfigLoader()
		karHandler = &knActionRecallHandler{
			Logger:                conf.GetLogger(),
			KnActionRecallService: logicsKAR.NewKnActionRecallService(),
		}
	})
	return karHandler
}

// GetActionInfo Gets action information (action recall)
func (h *knActionRecallHandler) GetActionInfo(c *gin.Context) {
	var err error
	req := &interfaces.KnActionRecallRequest{}

	// Bind headers.
	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind query parameters.
	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind the JSON body.
	if err = common.BindPreciseJSON(c.Request.Body, req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Set default values.
	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Validate parameters.
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Call business logic.
	resp, err := h.KnActionRecallService.GetActionInfo(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnActionRecallHandler#GetActionInfo] GetActionInfo failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// Return a successful response.
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ExecuteAction executes the action (asynchronously)
func (h *knActionRecallHandler) ExecuteAction(c *gin.Context) {
	var err error
	req := &interfaces.KnActionExecuteRequest{}

	// Bind headers.
	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind query parameters.
	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind the JSON body.
	if err = common.BindPreciseJSON(c.Request.Body, req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Set default values.
	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Validate parameters.
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Call business logic.
	resp, err := h.KnActionRecallService.ExecuteAction(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnActionRecallHandler#ExecuteAction] ExecuteAction failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// Return successful response (asynchronous, return execution_id)
	rest.ReplyOK(c, http.StatusOK, resp)
}

// GetActionExecution queries the status and result of a single action execution.
func (h *knActionRecallHandler) GetActionExecution(c *gin.Context) {
	var err error
	req := &interfaces.KnGetActionExecutionRequest{}

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = c.ShouldBindJSON(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	resp, err := h.KnActionRecallService.GetActionExecution(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnActionRecallHandler#GetActionExecution] GetActionExecution failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, resp)
}

// ListActionExecutions lists action execution history with filters and pagination.
func (h *knActionRecallHandler) ListActionExecutions(c *gin.Context) {
	var err error
	req := &interfaces.KnListActionExecutionsRequest{}

	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = c.ShouldBindQuery(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = common.BindPreciseJSON(c.Request.Body, req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err = validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	resp, err := h.KnActionRecallService.ListActionExecutions(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnActionRecallHandler#ListActionExecutions] ListActionExecutions failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, resp)
}
