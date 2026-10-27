// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"context"
	"net/http"
	"testing"
)

// TestDistinguishableErrorCodes focuses on those status codes that the model uses to decide whether to retry.
//
// DefaultHTTPError uses errCodeMap to check the code string. Codes that are not in the table will be returned.
// InternalServerError. The MCP tool layer returns JSON of HTTPError to the model, and the model reads code.
// Instead of HTTP status - 413 (file too large) and 502 (upstream hung) are said to be internal errors,
// The model will then retry a file that will never get smaller.
func TestDistinguishableErrorCodes(t *testing.T) {
	statuses := []int{
		http.StatusRequestEntityTooLarge,
		http.StatusBadGateway,
		http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity,
	}
	for _, status := range statuses {
		err := DefaultHTTPError(context.Background(), status, "detail")
		if err.HTTPCode != status {
			t.Fatalf("status %d: HTTPCode = %d", status, err.HTTPCode)
		}
		if err.ErrorCode == "" || err.ErrorCode == "Public.InternalServerError" {
			t.Fatalf("status %d: code = %q，落回了 InternalServerError，调用方无从区分", status, err.ErrorCode)
		}
	}
}

func TestExecutionFailureStatusCodes(t *testing.T) {
	input := ExecutionInputValidationError(context.Background(), "safe detail")
	timeout := DefaultHTTPError(context.Background(), http.StatusGatewayTimeout, "safe detail")
	existing := DefaultHTTPError(context.Background(), http.StatusUnprocessableEntity, "existing metric rejection")
	if input.HTTPCode != 422 || input.ErrorCode != "Public.UnprocessableEntity" {
		t.Fatalf("execution input error = %v", input)
	}
	if timeout.HTTPCode != 504 || timeout.ErrorCode != "Public.GatewayTimeout" {
		t.Fatalf("execution timeout error = %v", timeout)
	}
	if existing.HTTPCode != 422 || existing.ErrorCode != "Public.BadRequest" {
		t.Fatalf("existing 422 contract changed: %v", existing)
	}
}
