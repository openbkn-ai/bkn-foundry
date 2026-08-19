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
	}
	for _, status := range statuses {
		err := DefaultHTTPError(context.Background(), status, "detail")
		if err.HTTPCode != status {
			t.Fatalf("status %d: HTTPCode = %d", status, err.HTTPCode)
		}
		if err.Code == "" || err.Code == "agentRetrieval.InternalServerError" {
			t.Fatalf("status %d: code = %q，落回了 InternalServerError，调用方无从区分", status, err.Code)
		}
	}
}
