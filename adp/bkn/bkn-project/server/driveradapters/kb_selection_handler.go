// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	kbservice "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbresource"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func (r *restHandler) registerKBSelection(group *gin.RouterGroup) {
	group.GET("/selection/changes", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			return service.Changes(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"))
		})
	})
	group.GET("/selection", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			return service.Read(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"))
		})
	})
	group.PUT("/selection", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			var req interfaces.KBLockRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				return nil, &kb.SourceError{StatusCode: 400}
			}
			return service.Lock(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), req)
		})
	})
	group.PUT("/selection/samples", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			var req struct {
				ID       string                  `json:"selection_id"`
				Revision int64                   `json:"revision"`
				Plan     interfaces.KBSamplePlan `json:"plan"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" || req.Revision < 1 {
				return nil, &kb.SourceError{StatusCode: 400}
			}
			return service.SetSamples(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), req.ID, req.Revision, req.Plan)
		})
	})
	group.POST("/selection/clear", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			var req struct {
				Confirm bool `json:"confirm_clear_all"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
				return nil, &kb.SourceError{StatusCode: 400}
			}
			return service.Clear(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
		})
	})
	group.POST("/selection/cleanup", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			return service.Cleanup(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
		})
	})
	group.POST("/selection/operations/:operation_id/resume", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			return service.Resume(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), c.Param("operation_id"))
		})
	})
	group.POST("/selection/operations/:operation_id/cancel", func(c *gin.Context) {
		r.withKBSelection(c, func(service *kbservice.Service, visitor hydra.Visitor) (any, error) {
			return service.Cancel(i18n.GetLanguageCtx(c), visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), c.Param("operation_id"))
		})
	})
}

func (r *restHandler) withKBSelection(c *gin.Context, action func(*kbservice.Service, hydra.Visitor) (any, error)) {
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
		if r.kbra == nil {
			replySourceError(c, ctx, &kb.SourceError{StatusCode: 503})
			return
		}
		// Only small owner decisions arrive here, never original content or instances.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
		result, err := action(kbservice.NewService(r.ps, r.kbra, providers.Source), visitor)
		if err != nil {
			var httpErr *httperrors.HTTPError
			if errors.As(err, &httpErr) {
				rest.ReplyError(c, err)
				return
			}
			if errors.Is(err, interfaces.ErrKBSelectionConflict) {
				err = &kb.SourceError{StatusCode: 409}
			}
			if errors.Is(err, interfaces.ErrKBSamplePlan) {
				err = &kb.SourceError{StatusCode: 400}
			}
			replySourceError(c, ctx, err)
			return
		}
		rest.ReplyOK(c, http.StatusOK, result)
	})
}
