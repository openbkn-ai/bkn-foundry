// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package outboundtls

import (
	"errors"
	"strings"
)

type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case "TLS_TRUST_CONFLICT":
		return 409
	case "TLS_ENDPOINT_INVALID", "TLS_POLICY_INVALID":
		return 400
	case "TLS_CERTIFICATE_CHANGED", "TLS_CERTIFICATE_EXPIRED", "TLS_CERTIFICATE_MISSING", "TLS_TRUST_REQUIRED", "TLS_ENROLLMENT_FAILED":
		return 502
	default:
		return 503
	}
}
func fault(message string) error {
	code, detail, _ := strings.Cut(message, ":")
	return &Error{Code: code, Message: strings.TrimSpace(detail)}
}

func Extract(err error) *Error {
	var result *Error
	if errors.As(err, &result) {
		return result
	}
	return nil
}
