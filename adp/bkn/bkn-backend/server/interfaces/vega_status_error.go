// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"errors"
	"fmt"
	"net/http"
)

// VegaStatusError reports the non-success status of a Vega call. Its message is
// the status line the access layer has always returned, so callers that log or
// wrap it see the same text as before.
type VegaStatusError struct {
	Operation  string
	HTTPStatus int
}

func (e *VegaStatusError) Error() string {
	if e == nil {
		return "vega request failed"
	}
	return fmt.Sprintf("%s returned HTTP %d", e.Operation, e.HTTPStatus)
}

// IsVegaForbidden reports whether Vega refused the account the call was made as.
func IsVegaForbidden(err error) bool {
	var statusErr *VegaStatusError
	return errors.As(err, &statusErr) && statusErr.HTTPStatus == http.StatusForbidden
}
