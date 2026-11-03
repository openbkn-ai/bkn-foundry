// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package driveradapters

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbrefresh"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"net/http"
)

func replyRefreshError(c *gin.Context, ctx context.Context, err error) {
	status := http.StatusServiceUnavailable
	var backend *interfaces.BknBackendError
	switch {
	case errors.As(err, &backend):
		if backend.HTTPCode == 403 || backend.HTTPCode == 404 {
			status = backend.HTTPCode
		}
	case errors.Is(err, kbrefresh.ErrNoPublishedTarget):
		status = http.StatusConflict
	case errors.Is(err, kbrefresh.ErrLostLease), errors.Is(err, kbrefresh.ErrInvalidManifest), errors.Is(err, kb.ErrPublishedBindingsChanged):
		status = http.StatusConflict
	}
	code := httperrors.PublicError_InternalServerError
	switch status {
	case 403:
		code = httperrors.PublicError_Forbidden
	case 404:
		code = httperrors.PublicError_NotFound
	case 409:
		code = httperrors.PublicError_Conflict
	}
	rest.ReplyError(c, httperrors.NewHTTPError(ctx, status, code))
}
