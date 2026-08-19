// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch provides HTTP handler for knowledge network search operations.
package knsearch

import (
	"net/http"
	"sync"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	validator "github.com/go-playground/validator/v10"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicskn "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knsearch"
)

// KnSearchHandler kn_search handler.
type KnSearchHandler interface {
	KnSearch(c *gin.Context)
	SearchSchema(c *gin.Context)
	SearchInstance(c *gin.Context)
}

type knSearchHandler struct {
	Logger          interfaces.Logger
	KnSearchService interfaces.IKnSearchService
}

var (
	ksOnce    sync.Once
	ksHandler KnSearchHandler
)

// NewKnSearchHandler New KnSearchHandler.
func NewKnSearchHandler() KnSearchHandler {
	ksOnce.Do(func() {
		conf := config.NewConfigLoader()
		ksHandler = &knSearchHandler{
			Logger:          conf.GetLogger(),
			KnSearchService: logicskn.NewKnSearchService(),
		}
	})
	return ksHandler
}

// KnSearch knowledge network search.
func (h *knSearchHandler) KnSearch(c *gin.Context) {
	var err error
	req := &interfaces.KnSearchReq{}

	// Bind headers.
	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind the JSON body.
	if err = c.ShouldBindJSON(req); err != nil {
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
	resp, err := h.KnSearchService.KnSearch(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnSearchHandler#KnSearch] KnSearch failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// Return a successful response.
	rest.ReplyOK(c, http.StatusOK, resp)
}

// SearchSchema Standard Schema Search HTTP entry.
func (h *knSearchHandler) SearchSchema(c *gin.Context) {
	var err error
	req := &interfaces.SearchSchemaReq{}

	if err = c.ShouldBindHeader(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}

	if err = c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}

	if err = validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	resp, err := h.KnSearchService.SearchSchema(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnSearchHandler#SearchSchema] SearchSchema failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, resp)
}

// SearchInstance Natural language instance recall HTTP entry.
func (h *knSearchHandler) SearchInstance(c *gin.Context) {
	var err error
	req := &interfaces.SearchInstanceReq{}

	if err = c.ShouldBindHeader(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}

	if err = c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}

	if err = defaults.Set(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}

	if err = validator.New().Struct(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	resp, err := h.KnSearchService.SearchInstance(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnSearchHandler#SearchInstance] SearchInstance failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, resp)
}
