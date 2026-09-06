// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common/visitor"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func proxyRequestContext(c *gin.Context) (context.Context, string) {
	actor := visitor.GenerateVisitor(c)
	account := interfaces.AccountInfo{ID: actor.ID, Type: string(actor.Type)}
	return context.WithValue(c.Request.Context(), interfaces.ACCOUNT_INFO_KEY, account), actor.ID
}

func (r *restHandler) GetKNProxy(c *gin.Context) {
	ctx, _ := proxyRequestContext(c)
	mapping, err := r.kns.GetKNProxy(ctx, c.Param("kn_id"))
	if err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	rest.ReplyOK(c, http.StatusOK, mapping)
}

func (r *restHandler) ResolveKNProxyBinding(c *gin.Context) {
	ctx, _ := proxyRequestContext(c)
	var binding interfaces.KNProxyBinding
	if err := c.ShouldBindJSON(&binding); err != nil {
		rest.ReplyError(c, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_KnowledgeNetwork_InvalidParameter).WithErrorDetails("invalid proxy binding request"))
		return
	}
	mapping, err := r.kns.ResolveKNProxyBinding(ctx, c.Param("kn_id"), binding)
	if err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	rest.ReplyOK(c, http.StatusOK, mapping)
}

func (r *restHandler) RetryKNProxySync(c *gin.Context) {
	ctx, _ := proxyRequestContext(c)
	mapping, err := r.kns.RetryKNProxySync(ctx, c.Param("kn_id"))
	if err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	rest.ReplyOK(c, http.StatusOK, mapping)
}

func (r *restHandler) PlanKNProxySync(c *gin.Context) {
	ctx, _ := proxyRequestContext(c)
	plan, err := r.kns.PlanKNProxySync(ctx, c.Param("kn_id"))
	if err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	rest.ReplyOK(c, http.StatusOK, plan)
}

func (r *restHandler) FinalizeKNProxyDeletion(c *gin.Context) {
	ctx, _ := proxyRequestContext(c)
	if err := r.kns.FinalizeKNProxyDeletion(ctx, c.Param("kn_id")); err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *restHandler) ReconcileKNProxies(c *gin.Context) {
	ctx, requestedBy := proxyRequestContext(c)
	report, err := r.kns.ReconcileKNProxies(ctx, requestedBy)
	if err != nil {
		rest.ReplyError(c, err.(*rest.HTTPError))
		return
	}
	rest.ReplyOK(c, http.StatusOK, report)
}
