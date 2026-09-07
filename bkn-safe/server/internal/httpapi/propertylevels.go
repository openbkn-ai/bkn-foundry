// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

const (
	maxPropertyLevelObjectTypes       = 100
	maxPropertiesPerObjectType        = 200
	maxPropertiesPerPropertyLevelCall = 1000
	maxObjectIDLength                 = 40
	maxObjectTypeRefLength            = maxObjectIDLength*2 + 1
)

var propertyLevelObjectTypeRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}/[a-z0-9][a-z0-9_-]{0,39}$`)
var propertyLevelPropertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)

type propertyLevelsRequest struct {
	AccessorID string                      `json:"accessor_id" binding:"required"`
	Items      []propertyLevelsRequestItem `json:"items" binding:"required"`
}

type propertyLevelsRequestItem struct {
	ObjectTypeRef string   `json:"object_type_ref" binding:"required"`
	Properties    []string `json:"properties" binding:"required"`
}

// registerPropertyLevels adds the batched object-property decision to the
// existing tokenless S2S authz group. The chart deliberately excludes this
// group from Ingress, so accessor_id can only be supplied by trusted workloads
// that already authenticated the public caller.
func registerPropertyLevels(group *gin.RouterGroup, enforcer *authz.Enforcer, db *gorm.DB) {
	group.POST("/property-levels", func(c *gin.Context) {
		var body propertyLevelsRequest
		if !bind(c, &body) {
			return
		}
		if !validPropertyLevelsRequest(body) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}

		request := permdata.Request{
			AccessorID: body.AccessorID,
			Items:      make([]permdata.RequestItem, len(body.Items)),
		}
		for index, item := range body.Items {
			request.Items[index] = permdata.RequestItem{
				ObjectTypeRef: item.ObjectTypeRef,
				Properties:    append([]string(nil), item.Properties...),
				BaseLevel:     propertyaccess.None,
			}
		}

		active, err := activeAccount(c, db, body.AccessorID)
		if err != nil {
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		if !active {
			response, err := permdata.Fallback(request)
			if err != nil {
				replyPublicError(c, http.StatusServiceUnavailable)
				return
			}
			c.JSON(http.StatusOK, response)
			return
		}

		refs := make([]authz.ResourceRef, 0, len(request.Items))
		for _, item := range request.Items {
			refs = append(refs, authz.ResourceRef{Type: "object_type", ID: item.ObjectTypeRef})
		}
		allowed, err := enforcer.FilterResourceOps(body.AccessorID, refs, nil, []string{"view_detail", "query_data"})
		if err != nil {
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		operationsByObject := make(map[string][]string, len(allowed))
		for _, result := range allowed {
			operationsByObject[result.ID] = result.Operations
		}
		for index := range request.Items {
			request.Items[index].BaseLevel = basePropertyLevel(operationsByObject[request.Items[index].ObjectTypeRef])
		}

		response, err := permdata.Resolve(c.Request.Context(), request)
		if err != nil {
			// A missing, partial, or unknown enterprise result is an unavailable
			// authorization decision, never permission to reuse the core fallback.
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		c.JSON(http.StatusOK, response)
	})
}

func validPropertyLevelsRequest(request propertyLevelsRequest) bool {
	if request.AccessorID == "" || len(request.Items) == 0 || len(request.Items) > maxPropertyLevelObjectTypes {
		return false
	}
	totalProperties := 0
	seenObjects := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		if !validObjectTypeRef(item.ObjectTypeRef) || len(item.Properties) == 0 || len(item.Properties) > maxPropertiesPerObjectType {
			return false
		}
		if _, duplicate := seenObjects[item.ObjectTypeRef]; duplicate {
			return false
		}
		seenObjects[item.ObjectTypeRef] = struct{}{}
		totalProperties += len(item.Properties)
		if totalProperties > maxPropertiesPerPropertyLevelCall {
			return false
		}
		seenProperties := make(map[string]struct{}, len(item.Properties))
		for _, name := range item.Properties {
			if !propertyLevelPropertyNamePattern.MatchString(name) {
				return false
			}
			if _, duplicate := seenProperties[name]; duplicate {
				return false
			}
			seenProperties[name] = struct{}{}
		}
	}
	return true
}

func validObjectTypeRef(reference string) bool {
	return len(reference) <= maxObjectTypeRefLength && propertyLevelObjectTypeRefPattern.MatchString(reference)
}

func basePropertyLevel(operations []string) propertyaccess.Level {
	for _, operation := range operations {
		if operation == "query_data" {
			return propertyaccess.Full
		}
	}
	for _, operation := range operations {
		if operation == "view_detail" {
			return propertyaccess.Schema
		}
	}
	return propertyaccess.None
}
