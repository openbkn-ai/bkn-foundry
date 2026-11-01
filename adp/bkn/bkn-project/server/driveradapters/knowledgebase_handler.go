// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// RegisterKnowledgeBaseResources installs core-owned resource handlers during EE
// assembly. The application's complete-router gate hides them in other editions.
func RegisterKnowledgeBaseResources(engine *gin.Engine) {
	r := &restHandler{as: auth.NewAuthService(), ps: project.NewProjectService()}
	r.registerKnowledgeBaseResources(engine)
}

func (r *restHandler) registerKnowledgeBaseResources(engine *gin.Engine) {
	group := engine.Group("/api/bkn-project/v1/projects/:project_id/kb")
	group.Use(rest.PrivateNoCacheMiddleware())
	for _, resource := range []string{"knowledge-bases", "catalogs", "files", "summary"} {
		group.GET("/"+resource, func(c *gin.Context) { r.readKnowledgeBaseResource(c, resource) })
	}
}

func (r *restHandler) readKnowledgeBaseResource(c *gin.Context, resource string) {
	r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) {
		ctx := i18n.GetLanguageCtx(c)
		if visitor.Type != hydra.VisitorType_User && visitor.Type != hydra.VisitorType_RealName {
			replySourceError(c, ctx, &kb.SourceError{StatusCode: 403})
			return
		}
		if _, err := r.ps.GetProject(ctx, visitor.TokenID, c.Param("project_id")); err != nil {
			rest.ReplyError(c, err)
			return
		}
		providers, available := kb.Lookup()
		if !available {
			replyNotFound(c)
			return
		}
		ctx = kb.WithOperatorAuthorization(ctx, visitor.TokenID)
		scope := kb.SourceScope{KnowledgeBaseID: c.Query("knowledge_base_id")}
		if resource != "knowledge-bases" && scope.KnowledgeBaseID == "" {
			replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
			return
		}
		limit, err := parseLimit(ctx, c.Query("limit"), 20, 200)
		if err != nil {
			rest.ReplyError(c, err)
			return
		}
		page := kb.PageQuery{Limit: limit, Cursor: c.Query("cursor")}
		q := kb.FileQuery{PageQuery: page, CatalogID: c.Query("catalog_id"), UpdatedSince: c.Query("updated_since"), MimeTypes: c.QueryArray("mime_type")}
		if raw := c.Query("include_subcatalogs"); raw != "" {
			q.Recursive, err = strconv.ParseBool(raw)
			if err != nil {
				replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
				return
			}
		}
		if len(q.MimeTypes) > 20 {
			replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
			return
		}
		var result any
		switch resource {
		case "knowledge-bases":
			result, err = providers.Source.ListKnowledgeBases(ctx, scope, page)
		case "catalogs":
			result, err = providers.Source.ListCatalogs(ctx, scope, kb.CatalogQuery{PageQuery: page})
		case "files":
			result, err = providers.Source.ListFiles(ctx, scope, q)
		case "summary":
			result, err = providers.Source.SummarizeFiles(ctx, scope, q)
		}
		if err != nil {
			replySourceError(c, ctx, err)
			return
		}
		rest.ReplyOK(c, http.StatusOK, result)
	})
}

func replySourceError(c *gin.Context, ctx context.Context, err error) {
	status := http.StatusBadGateway
	var upstream *kb.SourceError
	switch {
	case errors.Is(err, kb.ErrUnavailable):
		status = 404
	case errors.Is(err, kb.ErrVersionGone):
		status = 410
	case errors.As(err, &upstream):
		switch upstream.StatusCode {
		case 400, 401, 403, 404, 409, 410, 503:
			status = upstream.StatusCode
		case 422:
			status = 400
		}
	}
	code := httperrors.PublicError_InternalServerError
	switch status {
	case 400:
		code = httperrors.PublicError_BadRequest
	case 401:
		code = httperrors.PublicError_Unauthorized
	case 403:
		code = httperrors.PublicError_Forbidden
	case 404, 410:
		code = httperrors.PublicError_NotFound
	case 409:
		code = httperrors.PublicError_Conflict
	case 503:
		code = httperrors.PublicError_ServiceUnavailable
	}
	key := "BknProject.KnowledgeBase.SourceUnavailable"
	switch status {
	case 400:
		key = "BknProject.KnowledgeBase.InvalidSelection"
	case 401:
		key = "BknProject.KnowledgeBase.AuthenticationExpired"
	case 403:
		key = "BknProject.KnowledgeBase.PermissionDenied"
	case 404:
		key = "BknProject.KnowledgeBase.SourceMissing"
	case 409:
		key = "BknProject.KnowledgeBase.SelectionConflict"
	case 410:
		key = "BknProject.KnowledgeBase.VersionGone"
	}
	message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), key, nil)
	if renderErr != nil {
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, status, code))
		return
	}
	rest.ReplyError(c, httperrors.NewHTTPError(ctx, status, code).WithErrorDetails(message))
}
