// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package httperrors owns BKN Safe's stable, platform-facing HTTP error codes.
package httperrors

import (
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/locale"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const (
	InvalidRequest     = "BknSafe.InvalidRequest"
	Unauthorized       = "BknSafe.Unauthorized"
	Forbidden          = "BknSafe.Forbidden"
	NotFound           = "BknSafe.NotFound"
	Conflict           = "BknSafe.Conflict"
	MethodNotAllowed   = "BknSafe.MethodNotAllowed"
	PayloadTooLarge    = "BknSafe.PayloadTooLarge"
	ServiceUnavailable = "BknSafe.ServiceUnavailable"
	InternalError      = "BknSafe.InternalError"

	AdminWriteInvalid                         = "BknSafe.AdminWrite.Invalid"
	AdminWriteImmutable                       = "BknSafe.AdminWrite.Immutable"
	AdminWriteNoUpdatableFields               = "BknSafe.AdminWrite.NoUpdatableFields"
	AdminWriteWildcardGrantForbidden          = "BknSafe.AdminWrite.WildcardGrantForbidden"
	AdminWriteAdminConsolePermissionForbidden = "BknSafe.AdminWrite.AdminConsolePermissionForbidden"
)

var (
	registerOnce sync.Once
	errorCodes   = []string{
		InvalidRequest,
		Unauthorized,
		Forbidden,
		NotFound,
		Conflict,
		MethodNotAllowed,
		PayloadTooLarge,
		ServiceUnavailable,
		InternalError,
		AdminWriteInvalid,
		AdminWriteImmutable,
		AdminWriteNoUpdatableFields,
		AdminWriteWildcardGrantForbidden,
		AdminWriteAdminConsolePermissionForbidden,
	}
)

// Register loads BKN Safe resources before making its error codes available.
// It is idempotent because HTTP router tests create several engines in one process.
func Register() {
	registerOnce.Do(func() {
		locale.Register()
		rest.Register(errorCodes)
	})
}

// ForStatus maps an HTTP status to the stable BKN Safe error code exposed to
// callers. Handler-specific validation remains a later extension; this mapping
// first replaces the previous free-form error strings without changing routes.
func ForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return InvalidRequest
	case http.StatusUnauthorized:
		return Unauthorized
	case http.StatusForbidden:
		return Forbidden
	case http.StatusNotFound:
		return NotFound
	case http.StatusConflict:
		return Conflict
	case http.StatusMethodNotAllowed:
		return MethodNotAllowed
	case http.StatusRequestEntityTooLarge:
		return PayloadTooLarge
	case http.StatusServiceUnavailable:
		return ServiceUnavailable
	default:
		return InternalError
	}
}
