// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package httperrors

// System default error codes.
const (
	PublicError_BadRequest          = "Public.BadRequest"
	PublicError_Unauthorized        = "Public.Unauthorized"
	PublicError_Forbidden           = "Public.Forbidden"
	PublicError_NotFound            = "Public.NotFound"
	PublicError_MethodNotAllowed    = "Public.MethodNotAllowed"
	PublicError_Conflict            = "Public.Conflict"
	PublicError_InternalServerError = "Public.InternalServerError"
	PublicError_NotImplemented      = "Public.NotImplemented"
	PublicError_ServiceUnavailable  = "Public.ServiceUnavailable"

	PublicError_RequestTimeout        = "Public.RequestTimeout"
	PublicError_RequestEntityTooLarge = "Public.RequestEntityTooLarge"
	PublicError_UnprocessableEntity   = "Public.UnprocessableEntity"
	PublicError_BadGateway            = "Public.BadGateway"
	PublicError_GatewayTimeout        = "Public.GatewayTimeout"
)

var publicErrorCodes = []string{
	PublicError_BadRequest,
	PublicError_Unauthorized,
	PublicError_Forbidden,
	PublicError_NotFound,
	PublicError_MethodNotAllowed,
	PublicError_Conflict,
	PublicError_InternalServerError,
	PublicError_NotImplemented,
	PublicError_ServiceUnavailable,
	PublicError_RequestTimeout,
	PublicError_RequestEntityTooLarge,
	PublicError_UnprocessableEntity,
	PublicError_BadGateway,
	PublicError_GatewayTimeout,
}

func init() {
	Register(publicErrorCodes)
}
