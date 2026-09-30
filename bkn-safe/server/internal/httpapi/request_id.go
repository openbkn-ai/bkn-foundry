// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
)

// requestIDMiddleware establishes request correlation for every HTTP request.
// It carries no business meaning and never writes a log fact; access and
// decision producers use the propagated ID only to correlate their own data.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ensureRequestID(c)
		c.Next()
	}
}

func ensureRequestID(c *gin.Context) string {
	requestID := requestIDFromHeader(c)
	if requestID == "" {
		requestID = audit.NewID()
		c.Request.Header.Set("x-request-id", requestID)
	}
	c.Header("x-request-id", requestID)
	return requestID
}
