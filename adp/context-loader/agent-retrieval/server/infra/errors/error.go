// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

var errCodeMap = map[int]string{
	400: "BadRequest",
	401: "Unauthorized",
	403: "Forbidden",
	404: "NotFound",
	405: "MethodNotAllowed",
	409: "Conflict",
	413: "RequestEntityTooLarge",
	422: "BadRequest",
	500: "InternalServerError",
	501: "NotImplemented",
	502: "BadGateway",
	503: "ServiceUnavailable",
	408: "RequestTimeout",
	504: "GatewayTimeout",
}

// DefaultHTTPError returns a shared public error for an HTTP status.
func DefaultHTTPError(ctx context.Context, httpCode int, details any) *httperrors.HTTPError {
	name := errCodeMap[httpCode]
	if name == "" {
		name = "InternalServerError"
	}
	return httperrors.NewHTTPError(ctx, httpCode, "Public."+name).WithErrorDetails(details)
}

// NewHTTPError constructs a registered service error without rendering it.
func NewHTTPError(ctx context.Context, httpCode int, extCode string, details any, descParams ...any) *httperrors.HTTPError {
	name := errCodeMap[httpCode]
	if name == "" {
		name = "InternalServerError"
	}
	code := fmt.Sprintf("agentRetrieval.%s.%s", name, extCode)
	metadata := make(map[string]any, len(descParams))
	for index, value := range descParams {
		metadata[fmt.Sprintf("Arg%d", index+1)] = value
	}
	return httperrors.NewHTTPError(ctx, httpCode, code).WithErrorDetails(details).WithMetadata(metadata)
}

// HTTPStatus extracts a shared public status, including wrapped dependency failures.
func HTTPStatus(err error) (int, bool) {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return 0, false
	}
	return httpErr.HTTPCode, true
}

func IsAuthorizationError(err error) bool {
	status, ok := HTTPStatus(err)
	return ok && (status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusServiceUnavailable)
}
