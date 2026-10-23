// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httperrors

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type localizedErrorResponse struct {
	rest.BaseError
	// Error retains BKN Safe's former field while callers migrate to the stable
	// error_code contract. Its value is localized and duplicates description.
	Error string `json:"error"`
}

// New creates a localized HTTP error for the stable BKN Safe status mapping.
func New(ctx context.Context, status int) *rest.HTTPError {
	return NewCode(ctx, status, ForStatus(status))
}

// NewCode creates a localized HTTP error with a handler-specific stable code.
func NewCode(ctx context.Context, status int, code string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, status, code)
}

// Write sends the common localized BKN Safe error envelope. Optional details
// must remain structured, machine-readable context rather than free-form text.
func Write(c *gin.Context, status int, details any) {
	WriteCode(c, status, ForStatus(status), details)
}

// WriteCode sends the common envelope with a handler-specific stable code.
func WriteCode(c *gin.Context, status int, code string, details any) {
	err := NewCode(c.Request.Context(), status, code)
	if details != nil {
		err = err.WithErrorDetails(details)
	}
	renderErr := err.Render()
	if renderErr != nil {
		logger.Errorf("localized error rendering failed: %v", renderErr)
	}
	c.Header(i18n.ContentLanguageHeader, err.Lang)
	rest.ReplyOK(c, status, localizedErrorResponse{
		BaseError: err.BaseError,
		Error:     err.BaseError.Description,
	})
}
