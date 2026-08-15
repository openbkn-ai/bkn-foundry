// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/capabilitieslab/client"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const (
	contextKeyRequestID = "request_id"
	contextKeyUserID    = "user_id"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
		}

		c.Set(contextKeyRequestID, requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func newRequestID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

func AuthMiddleware(defaultUserID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetHeader("user_id")
		if userID == "" {
			userID = c.GetHeader("X-User-ID")
		}
		if userID == "" {
			userID = defaultUserID
		}

		c.Set(contextKeyUserID, userID)
		forwardedCtx := client.WithForwardedAuth(
			c.Request.Context(),
			c.GetHeader("Authorization"),
			c.GetHeader("Cookie"),
		)
		c.Request = c.Request.WithContext(forwardedCtx)
		c.Next()
	}
}

func requestIDFromContext(c *gin.Context) string {
	if value, ok := c.Get(contextKeyRequestID); ok {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}

	return ""
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, APIErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: requestIDFromContext(c),
	})
}

func writeLocalizedError(c *gin.Context, status int, code string) {
	sharedrest.MarkLocalizedCacheableResponse(c)
	writeError(c, status, code, capabilitiesLabErrorMessage(sharedrest.GetLanguageByCtx(c.Request.Context()), code))
}

func writeBadGateway(c *gin.Context, message string) {
	writeError(c, http.StatusBadGateway, "upstream_error", message)
}

func writeBadRequest(c *gin.Context, message string) {
	_ = message
	writeLocalizedError(c, http.StatusBadRequest, capabilitiesLabInvalidRequest)
}

func writeNotFound(c *gin.Context, message string) {
	_ = message
	writeLocalizedError(c, http.StatusNotFound, capabilitiesLabNotFound)
}

func writeFileRequired(c *gin.Context) {
	writeLocalizedError(c, http.StatusBadRequest, capabilitiesLabFileRequired)
}

func writeFeatureDisabled(c *gin.Context) {
	writeLocalizedError(c, http.StatusNotFound, capabilitiesLabFeatureDisabled)
}

type APIErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}
