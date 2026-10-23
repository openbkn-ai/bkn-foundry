// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

var errCodeMap = map[int]string{
	400: "BadRequest",
	401: "Unauthorized",
	403: "Forbidden",
	404: "NotFound",
	405: "MethodNotAllowed",
	409: "Conflict",
	413: "RequestEntityTooLarge",
	500: "InternalServerError",
	501: "NotImplemented",
	502: "BadGateway",
	503: "ServiceUnavailable",
	408: "RequestTimeout",
	504: "GatewayTimeout",
}

// DefaultHTTPError returns a shared public error for an HTTP status.
func DefaultHTTPError(ctx context.Context, httpCode int, details any) *rest.HTTPError {
	name := errCodeMap[httpCode]
	if name == "" {
		name = "InternalServerError"
	}
	return rest.NewHTTPError(ctx, httpCode, "Public."+name).WithErrorDetails(details)
}

// NewHTTPError constructs a registered service error without rendering it.
func NewHTTPError(ctx context.Context, httpCode int, extCode ErrorCode, details any, descParams ...any) *rest.HTTPError {
	name := errCodeMap[httpCode]
	if name == "" {
		name = "InternalServerError"
	}
	code := fmt.Sprintf("AgentOperatorIntegration.%s.%s", name, extCode.String())
	metadata := make(map[string]any, len(descParams))
	for index, value := range descParams {
		metadata[fmt.Sprintf("Arg%d", index+1)] = value
	}
	return rest.NewHTTPError(ctx, httpCode, code).WithErrorDetails(details).WithMetadata(metadata)
}
