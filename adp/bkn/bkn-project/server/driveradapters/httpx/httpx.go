// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package httpx is what every handler of the project API shares, the handlers an edition adds
// included: authenticating the caller, reading a limit and replying with an error.
package httpx

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/trace"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Account is the caller as the services take it.
func Account(visitor hydra.Visitor) interfaces.AccountInfo {
	return interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
}

// WithVisitor authenticates the request before handing it on; a request that fails is answered 401.
func WithVisitor(c *gin.Context, auth interfaces.AuthService, handle func(*gin.Context, hydra.Visitor)) {
	ctx := i18n.GetLanguageCtx(c)
	visitor, err := auth.VerifyToken(ctx, c)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.Validation.Detail.AuthenticationFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusUnauthorized, httperrors.PublicError_Unauthorized).
			WithErrorDetails(localizedText))
		return
	}
	handle(c, visitor)
}

// ReplyError answers with a public error the services returned.
func ReplyError(c *gin.Context, span trace.Span, err error) {
	httpErr := err.(*httperrors.HTTPError)
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	rest.ReplyError(c, httpErr)
}

// ReplyNotFound answers 404, as for a route that does not exist.
func ReplyNotFound(c *gin.Context) {
	rest.ReplyError(c, httperrors.NewHTTPError(c.Request.Context(), http.StatusNotFound, httperrors.PublicError_NotFound))
}

// ParseLimit reads a page size between 1 and max; empty is the default.
func ParseLimit(ctx context.Context, value string, defaultLimit int, max int) (int, error) {
	if value == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err == nil && limit >= 1 && limit <= max {
		return limit, nil
	}
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.Validation.Detail.LimitOutOfRange",
		map[string]any{"max": max})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	return 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Limit).
		WithErrorDetails(localizedText)
}
