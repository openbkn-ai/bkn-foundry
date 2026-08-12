// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-comm-go/rest"
)

// isNotFoundError 区分父对象已删除和临时服务错误。
func isNotFoundError(err error) bool {
	var httpErr *rest.HTTPError
	return errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound
}
