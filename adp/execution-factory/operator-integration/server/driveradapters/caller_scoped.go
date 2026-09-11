// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
)

// middlewareCallerScopedAuthorization marks a trusted internal request as a
// caller-scoped business request. The private middleware has already resolved
// X-Account-ID/X-Account-Type into the request context, so the regular public
// authorization branches can enforce that caller without requiring an OAuth
// bearer token between internal services.
func middlewareCallerScopedAuthorization() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := common.SetPublicAPIToCtx(c.Request.Context(), true)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
