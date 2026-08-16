// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
)

// abortPublicError keeps authentication middleware on the same localized error
// contract as the rest of the platform. Handlers must not write a second body
// after this function returns.
func abortPublicError(c *gin.Context, status int) {
	c.Abort()
	writeLocalizedError(c, status, nil)
}

func abortInternalError(c *gin.Context) {
	abortPublicError(c, http.StatusInternalServerError)
}

func replyPublicError(c *gin.Context, status int) {
	writeLocalizedError(c, status, nil)
}

// replyPublicErrorDetails preserves dynamic, machine-readable context without
// making response localization depend on a free-form human error string.
func replyPublicErrorDetails(c *gin.Context, status int, details any) {
	writeLocalizedError(c, status, details)
}

func replyInternalError(c *gin.Context) {
	replyPublicError(c, http.StatusInternalServerError)
}

func writeLocalizedError(c *gin.Context, status int, details any) {
	httperrors.Write(c, status, details)
}
