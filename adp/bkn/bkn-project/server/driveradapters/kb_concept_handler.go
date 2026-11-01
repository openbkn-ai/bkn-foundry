// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func (r *restHandler) registerKBConcept(group *gin.RouterGroup) {
	group.POST("/concepts:extract", func(c *gin.Context) {
		r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) {
			ctx := i18n.GetLanguageCtx(c)
			if visitor.Type != hydra.VisitorType_User && visitor.Type != hydra.VisitorType_RealName {
				replySourceError(c, ctx, &kb.SourceError{StatusCode: 403})
				return
			}
			providers, enabled := kb.Lookup()
			if !enabled {
				replyNotFound(c)
				return
			}
			if r.kbra == nil || r.da == nil {
				replyConceptError(c, ctx, kb.ErrUnavailable)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
			var req kbconcept.ExtractRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
				return
			}
			result, err := kbconcept.NewService(r.ps, r.kbra, r.da, providers.Source, providers.Concepts).Extract(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), req)
			if err != nil {
				replyConceptError(c, ctx, err)
				return
			}
			rest.ReplyOK(c, http.StatusOK, result)
		})
	})
}
func replyConceptError(c *gin.Context, ctx context.Context, err error) {
	status, key := 0, ""
	switch {
	case errors.Is(err, kbconcept.ErrNoDebugSamples):
		status, key = 400, "NoDebugSamples"
	case errors.Is(err, kbconcept.ErrNoLocatedSamples):
		status, key = 400, "NoLocatedSamples"
	case errors.Is(err, kbconcept.ErrSampleLimit):
		status, key = 413, "SampleLimit"
	case errors.Is(err, kbconcept.ErrInvalidModel), errors.Is(err, kbconcept.ErrUnverifiedCitation):
		status, key = 502, "InvalidProposal"
	case errors.Is(err, kb.ErrUnavailable):
		status, key = 503, "ModelUnavailable"
	case errors.Is(err, context.DeadlineExceeded):
		status, key = 503, "ExtractionTimeout"
	}
	var upstream *kb.SourceError
	if errors.As(err, &upstream) && upstream.StatusCode == 413 {
		status, key = 413, "SampleLimit"
	}
	if status == 0 {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			rest.ReplyError(c, err)
		} else {
			replySourceError(c, ctx, err)
		}
		return
	}
	code := httperrors.PublicError_BadRequest
	if status >= 500 {
		code = httperrors.PublicError_ServiceUnavailable
	}
	message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.KnowledgeBase."+key, nil)
	if renderErr != nil {
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, status, code))
		return
	}
	rest.ReplyError(c, httperrors.NewHTTPError(ctx, status, code).WithErrorDetails(message))
}
