// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httperrors

import (
	"context"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type localizedErrorResponse struct {
	sharedrest.BaseError
	// Error retains BKN Safe's former field while callers migrate to the stable
	// error_code contract. Its value is localized and duplicates description.
	Error string `json:"error"`
}

// New creates a localized HTTP error for the stable BKN Safe status mapping.
func New(ctx context.Context, status int) *sharedrest.HTTPError {
	Register()
	return sharedrest.NewHTTPError(ctx, status, ForStatus(status))
}

// Write sends the common localized BKN Safe error envelope. Optional details
// must remain structured, machine-readable context rather than free-form text.
func Write(c *gin.Context, status int, details any) {
	err := New(c.Request.Context(), status)
	if details != nil {
		err.WithErrorDetails(details)
	}
	sharedrest.MarkLocalizedResponse(c)
	c.JSON(status, localizedErrorResponse{
		BaseError: err.BaseError,
		Error:     err.BaseError.Description,
	})
}
