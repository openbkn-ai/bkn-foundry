// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// isNotFoundError distinguishes between a deleted parent object and a temporary service error.
func isNotFoundError(err error) bool {
	var httpErr *rest.HTTPError
	return errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound
}
