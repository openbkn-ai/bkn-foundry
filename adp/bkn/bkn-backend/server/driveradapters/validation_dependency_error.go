// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/trace"

	"bkn-backend/logics"
)

// replyDependencyValidationError keeps validate-only endpoints machine
// readable for dependency failures while preserving HTTP 200 valid:false for
// ordinary model-validation feedback.
func replyDependencyValidationError(c *gin.Context, span trace.Span, err error) bool {
	if !logics.IsDependencyHTTPError(err) {
		return false
	}
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	rest.ReplyError(c, httpErr)
	return true
}
