// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knquerysubgraph provides HTTP handler for subgraph query operations.
package knquerysubgraph

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
	logicskn "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
)

// KnQuerySubgraphHandler subgraph query processor.
type KnQuerySubgraphHandler interface {
	QueryInstanceSubgraph(c *gin.Context)
	ExploreSubgraph(c *gin.Context)
}

type knQuerySubgraphHandler struct {
	Logger                 interfaces.Logger
	KnQuerySubgraphService interfaces.IKnQuerySubgraphService
}

var (
	kqsOnce    sync.Once
	kqsHandler KnQuerySubgraphHandler
)

// NewKnQuerySubgraphHandler New KnQuerySubgraphHandler.
func NewKnQuerySubgraphHandler() KnQuerySubgraphHandler {
	kqsOnce.Do(func() {
		conf := config.NewConfigLoader()
		kqsHandler = &knQuerySubgraphHandler{
			Logger:                 conf.GetLogger(),
			KnQuerySubgraphService: logicskn.NewKnQuerySubgraphService(),
		}
	})
	return kqsHandler
}

// QueryInstanceSubgraph queries the object subgraph.
func (h *knQuerySubgraphHandler) QueryInstanceSubgraph(c *gin.Context) {
	var err error
	req := &interfaces.QueryInstanceSubgraphReq{}

	// Bind headers.
	if err = c.ShouldBindHeader(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind Path Parameters.
	if err = c.ShouldBindUri(req); err != nil {
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
	resp, err := h.KnQuerySubgraphService.QueryInstanceSubgraph(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnQuerySubgraphHandler#QueryInstanceSubgraph] QueryInstanceSubgraph failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// Return a successful response.
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ExploreSubgraph runs source-based exploratory subgraph queries.
func (h *knQuerySubgraphHandler) ExploreSubgraph(c *gin.Context) {
	var err error
	req := &interfaces.ExploreSubgraphReq{}

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

	resp, err := h.KnQuerySubgraphService.ExploreSubgraph(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnQuerySubgraphHandler#ExploreSubgraph] ExploreSubgraph failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}

	rest.ReplyOK(c, http.StatusOK, resp)
}
