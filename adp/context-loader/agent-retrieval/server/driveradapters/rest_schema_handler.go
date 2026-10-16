// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	aerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type restSchemaHandler struct {
	Hydra   interfaces.Hydra
	AppKeys interfaces.AppKeyVerifier
	Schema  interfaces.SchemaReadAccess
}

// NewRestSchemaHandler keeps the existing SDK REST paths available on the
// authenticated sandbox listener. It is not a general backend proxy.
func NewRestSchemaHandler() interfaces.HTTPRouterInterface {
	return &restSchemaHandler{
		Hydra:   drivenadapters.NewHydra(),
		AppKeys: drivenadapters.NewAppKeyVerifier(),
		Schema:  drivenadapters.NewSchemaReadAccess(),
	}
}

func (h *restSchemaHandler) RegisterRouter(group *gin.RouterGroup) {
	group.Use(middlewareTrace, sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware(), middlewareIntrospectVerify(h.Hydra, h.AppKeys))
	group.GET("/knowledge-networks/:kn_id", h.read(""))
	group.GET("/knowledge-networks/:kn_id/object-types", h.read("object-types"))
	group.GET("/knowledge-networks/:kn_id/relation-types", h.read("relation-types"))
	group.GET("/knowledge-networks/:kn_id/object-metrics", h.read("object-metrics"))
}

func (h *restSchemaHandler) read(collection string) gin.HandlerFunc {
	return func(c *gin.Context) {
		knID := c.Param("kn_id")
		if knID == "" || knID == "." || knID == ".." || strings.ContainsAny(knID, "/\r\n") {
			rest.ReplyError(c, aerrors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "invalid knowledge network id"))
			return
		}
		query, queryErr := url.ParseQuery(c.Request.URL.RawQuery)
		if queryErr != nil {
			rest.ReplyError(c, aerrors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "invalid schema query string"))
			return
		}
		for key := range query {
			allowed := key == "branch" || collection == "object-metrics" && key == "status" || collection != "" && (key == "limit" || key == "offset" || key == "sort" || key == "direction")
			if !allowed {
				rest.ReplyError(c, aerrors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "unsupported schema query parameter"))
				return
			}
		}
		status, body, err := h.Schema.ReadSchema(c.Request.Context(), knID, collection, query)
		if err != nil || status < 200 || status > 599 || status >= 300 && status < 400 {
			// Transport diagnostics can include URLs or credentials; do not expose them.
			rest.ReplyError(c, aerrors.DefaultHTTPError(c.Request.Context(), http.StatusBadGateway, "schema backend request failed"))
			return
		}
		// Preserve backend status, full schema fields, and permission failures. There
		// is no agent-detail projection, summary mode, or MCP session deduplication.
		c.Data(status, "application/json", body)
	}
}
