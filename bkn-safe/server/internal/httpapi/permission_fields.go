// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"net/http"
	"sort"
	"strings"
)

// Configuration-list endpoints intentionally remain sparse. This read-only
// endpoint projects every published data property through the runtime resolver.
func registerEffectiveFieldPermissions(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB, published RowFilterPublishedObjectTypeResolver) {
	g.GET("/permission-projection/fields", RequirePermission(e, "admin-authz", "view"), func(c *gin.Context) {
		id, kind, ref := c.Query("accessor_id"), c.Query("accessor_type"), c.Query("object_type_ref")
		network, object, ok := strings.Cut(ref, "/")
		if id == "" || (kind != "user" && kind != "role") || !ok || network == "" || object == "" || !isConcreteResourceID(ref) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if kind == "user" {
			active, err := activeAccount(c, db, id)
			if err != nil {
				serverError(c, err)
				return
			}
			if !active {
				replyPublicError(c, http.StatusNotFound)
				return
			}
		} else {
			var role model.Role
			if err := db.WithContext(c.Request.Context()).First(&role, "id = ?", id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					replyPublicError(c, http.StatusNotFound)
				} else {
					serverError(c, err)
				}
				return
			}
		}
		if published == nil {
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		schema, err := published.ResolvePublishedObjectType(c.Request.Context(), c.GetString(ctxAccessorID), ref)
		if err != nil {
			serverError(c, err)
			return
		}
		if !schema.Published || schema.ObjectTypeRef != ref {
			replyPublicError(c, http.StatusConflict)
			return
		}
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		base, err := e.FilterResourceOpsScoped(c.Request.Context(), id, []authz.ResourceRef{{Type: "object_type", ID: ref}}, nil, []string{"view_detail", "query_data"}, authz.VisibilityMatchAll, authz.ScopeEffective)
		if err != nil {
			serverError(c, err)
			return
		}
		var operations []string
		if len(base) == 1 {
			operations = base[0].Operations
		}
		request := permdata.Request{AccessorID: id, Items: []permdata.RequestItem{{ObjectTypeRef: ref, Properties: names, BaseLevel: basePropertyLevel(operations)}}}
		var response permdata.Response
		if kind == "role" {
			response, err = permdata.ResolveRole(c.Request.Context(), request)
		} else {
			response, err = permdata.Resolve(c.Request.Context(), request)
		}
		if err != nil {
			serverError(c, err)
			return
		}
		decisions := make([]gin.H, 0, len(names))
		for _, field := range response.Entries[0].Properties {
			decisions = append(decisions, gin.H{"name": field.Name, "display_name": schema.Properties[field.Name].DisplayName, "level": field.Level, "source": field.Source})
		}
		c.JSON(http.StatusOK, gin.H{"accessor": gin.H{"id": id, "type": kind}, "object_type_ref": ref, "decisions": decisions, "entries": []any{}})
	})
}
