// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
)

// registerManagedProxyAccounts mounts the ClusterIP-only control-plane API.
// The route is intentionally separate from public /me and /admin surfaces: its
// caller is the trusted BKN workload, and network policy is the trust boundary.
func registerManagedProxyAccounts(r *gin.Engine, service *managedproxy.Service) {
	group := r.Group("/api/safe/in/v1/managed-proxy-accounts")

	group.POST("", func(c *gin.Context) {
		var req managedproxy.CreateRequest
		if !bind(c, &req) {
			return
		}
		account, created, err := service.Create(c.Request.Context(), req)
		if errors.Is(err, managedproxy.ErrInvalidManagedResource) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, account)
	})

	group.GET("/:id", func(c *gin.Context) {
		account, err := service.Get(c.Request.Context(), c.Param("id"))
		writeManagedProxyResult(c, account, err)
	})

	group.POST("/:id/restore", func(c *gin.Context) {
		account, err := service.Restore(c.Request.Context(), c.Param("id"))
		if errors.Is(err, managedproxy.ErrInvalidLifecycle) {
			replyPublicError(c, http.StatusConflict)
			return
		}
		writeManagedProxyResult(c, account, err)
	})

	group.POST("/:id/disable", func(c *gin.Context) {
		account, err := service.Disable(c.Request.Context(), c.Param("id"))
		writeManagedProxyResult(c, account, err)
	})

	group.POST("/:id/archive", func(c *gin.Context) {
		account, err := service.Archive(c.Request.Context(), c.Param("id"))
		writeManagedProxyResult(c, account, err)
	})
}

func writeManagedProxyResult(c *gin.Context, account *managedproxy.Account, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		replyPublicError(c, http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, account)
}
