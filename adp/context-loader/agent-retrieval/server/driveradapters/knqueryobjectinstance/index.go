// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knqueryobjectinstance provides HTTP handler for object instance query operations.
package knqueryobjectinstance

import (
	"net/http"
	"sync"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	validator "github.com/go-playground/validator/v10"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// KnQueryObjectInstanceHandler query object instance handler.
type KnQueryObjectInstanceHandler interface {
	QueryObjectInstance(c *gin.Context)
}

type knQueryObjectInstanceHandler struct {
	Logger        interfaces.Logger
	OntologyQuery interfaces.DrivenOntologyQuery
}

var (
	koiOnce    sync.Once
	koiHandler KnQueryObjectInstanceHandler
)

// NewKnQueryObjectInstanceHandler New KnQueryObjectInstanceHandler.
func NewKnQueryObjectInstanceHandler() KnQueryObjectInstanceHandler {
	koiOnce.Do(func() {
		conf := config.NewConfigLoader()
		koiHandler = &knQueryObjectInstanceHandler{
			Logger:        conf.GetLogger(),
			OntologyQuery: drivenadapters.NewOntologyQueryAccess(),
		}
	})
	return koiHandler
}

// QueryObjectInstance queryobject instance.
func (h *knQueryObjectInstanceHandler) QueryObjectInstance(c *gin.Context) {
	var err error
	req := &interfaces.QueryObjectInstancesReq{}

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
	req.IncludeTypeInfo = false

	// Validate parameters.
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}

	// Call business logic.
	resp, err := h.OntologyQuery.QueryObjectInstances(c.Request.Context(), req)
	if err != nil {
		h.Logger.Errorf("[KnQueryObjectInstanceHandler#QueryObjectInstance] QueryObjectInstances failed, err: %v", err)
		rest.ReplyError(c, err)
		return
	}
	if eventID := bkntrace.EmitQueryObjectInstanceEvents(c.Request.Context(), h.Logger, req, resp); eventID != "" {
		c.Header("bkn-evidence-event-id", eventID)
	}

	// Pure structured filtering has no relevance score; strip the constant _score to avoid misleading callers.
	// Keep real relevance scores from knn/match (#236).
	if !req.HasScoringOperator() {
		resp.StripInstanceScores()
	}

	// Return a successful response.
	rest.ReplyOK(c, http.StatusOK, resp)
}
