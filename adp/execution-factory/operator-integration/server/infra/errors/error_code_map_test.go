package errors

import (
	"context"
	"net/http"
	"testing"
)

func TestExecutionFailureStatusCodes(t *testing.T) {
	for status, code := range map[int]string{http.StatusUnprocessableEntity: "Public.UnprocessableEntity", http.StatusGatewayTimeout: "Public.GatewayTimeout"} {
		he := DefaultHTTPError(context.Background(), status, "safe detail")
		if he.HTTPCode != status || he.ErrorCode != code {
			t.Fatalf("error=%v, status=%d; want %s", he, he.HTTPCode, code)
		}
	}
}
