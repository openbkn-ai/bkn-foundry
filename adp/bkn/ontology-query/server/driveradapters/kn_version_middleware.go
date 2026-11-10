// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// KNVersionMiddleware accepts kn_version and draft_revision on every query and pins them to
// the request context. The pin fixes the knowledge network version at the first bkn-backend
// read, and every later bkn-backend call of the query asks for that version. The legacy
// branch parameter is untouched: it still accepts only main, which means the running version.
func (r *restHandler) KNVersionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		pin, err := interfaces.NewKNVersionPin(c.Query("kn_version"), c.Query("draft_revision"))
		if err != nil {
			rest.ReplyError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusBadRequest,
				oerrors.OntologyQuery_KNVersion_InvalidParameter).WithErrorDetails(err.Error()))
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(interfaces.WithKNVersionPin(c.Request.Context(), pin))
		c.Next()
	}
}
