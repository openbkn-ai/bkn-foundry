// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"bkn-backend/common/visitor"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

// CypherQueryRequest is the request body of a Cypher query. Paging is written
// in the query itself, with SKIP and LIMIT, so there is nothing else to carry.
type CypherQueryRequest struct {
	Query string `json:"query"`
}

func (r *restHandler) RunCypherQueryByEx(c *gin.Context) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.RunCypherQuery(c, vis)
}

func (r *restHandler) RunCypherQueryByIn(c *gin.Context) {
	r.RunCypherQuery(c, visitor.GenerateVisitor(c))
}

func (r *restHandler) RunCypherQuery(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID := c.Param("kn_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("branch").String(branch))

	var body CypherQueryRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	_, exist, err := r.kns.CheckKNExistByID(ctx, knID, branch)
	if err != nil {
		replyHandlerError(c, span, ctx, err)
		return
	}
	if !exist {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	result, err := r.cqs.Query(ctx, interfaces.CypherQuery{
		KNID:   knID,
		Branch: branch,
		Query:  body.Query,
	})
	if err != nil {
		replyHandlerError(c, span, ctx, err)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// replyHandlerError keeps an error that is not an *rest.HTTPError from
// becoming a panic in the handler, which is what a bare type assertion would
// do the first time a dependency returns a plain error.
func replyHandlerError(c *gin.Context, span trace.Span, ctx context.Context, err error) {
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	rest.ReplyError(c, httpErr)
}
