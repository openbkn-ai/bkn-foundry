// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Release lineage of published knowledge networks. Every authenticated caller
// may read it. Publishing imports with the caller's token, so bkn-backend
// applies its own permissions to the new network.

func (r *restHandler) ListReleasesByEx(c *gin.Context)   { r.withExVisitor(c, r.ListReleases) }
func (r *restHandler) GetReleaseByEx(c *gin.Context)     { r.withExVisitor(c, r.GetRelease) }
func (r *restHandler) PublishReleaseByEx(c *gin.Context) { r.withExVisitor(c, r.PublishRelease) }

func replyError(c *gin.Context, span trace.Span, err error) {
	httpErr := err.(*httperrors.HTTPError)
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	rest.ReplyError(c, httpErr)
}

func (r *restHandler) ListReleases(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID))

	limit, err := parseLimit(ctx, c.Query("limit"), interfaces.DEFAULT_RELEASE_LIMIT, interfaces.MAX_RELEASE_LIMIT)
	if err != nil {
		replyError(c, span, err)
		return
	}
	list, err := r.rs.ListReleases(ctx, lineageID, limit, c.Query("cursor"))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, list)
}

func (r *restHandler) GetRelease(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	networkID := c.Param("network_id")
	span.SetAttributes(attr.Key("network_id").String(networkID))

	release, err := r.rs.GetRelease(ctx, networkID)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, release)
}

// PublishRelease serves POST /lineages/{lineage_id}/releases with
// {"branch", "version", "project_id", "notes"}.
func (r *restHandler) PublishRelease(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID))

	req := interfaces.PublishReleaseRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, invalidBody(ctx))
		return
	}
	span.SetAttributes(attr.Key("branch").String(req.Branch), attr.Key("version").String(req.Version))

	publisher := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	result, err := r.ls.PublishRelease(ctx, visitor.TokenID, lineageID, req, publisher, gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// parseLimit reads the page size, from 1 to max; an absent limit takes the default.
func parseLimit(ctx context.Context, value string, defaultLimit int, max int) (int, error) {
	if value == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err == nil && limit >= 1 && limit <= max {
		return limit, nil
	}
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.Validation.Detail.LimitOutOfRange",
		map[string]any{"max": max})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	return 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Limit).
		WithErrorDetails(localizedText)
}
