// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/httperrors"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
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

func localizedHTTPError(c *gin.Context, status int) *sharedrest.HTTPError {
	// New() registers once during normal startup. Keeping this call here makes
	// the helper safe for focused middleware tests that construct a bare router.
	httperrors.Register()
	return sharedrest.NewHTTPError(c.Request.Context(), status, httperrors.ForStatus(status))
}

type localizedErrorResponse struct {
	sharedrest.BaseError
	// Error retains BKN Safe's former field while callers migrate to the stable
	// error_code contract. Its value is localized and duplicates description.
	Error string `json:"error"`
}

func writeLocalizedError(c *gin.Context, status int, details any) {
	err := localizedHTTPError(c, status)
	if details != nil {
		err.WithErrorDetails(details)
	}
	sharedrest.MarkLocalizedResponse(c)
	c.JSON(status, localizedErrorResponse{
		BaseError: err.BaseError,
		Error:     err.BaseError.Description,
	})
}
