// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	dtype "ontology-query/interfaces/data_type"
)

type rowFilterCapabilityRequest struct {
	ObjectTypeRef string `json:"object_type_ref"`
}

type rowFilterCapabilityProperty struct {
	Type            string `json:"type"`
	ExactFilterable bool   `json:"exact_filterable"`
}

type rowFilterCapabilityResponse struct {
	ObjectTypeRef string                                 `json:"object_type_ref"`
	Published     bool                                   `json:"published"`
	Properties    map[string]rowFilterCapabilityProperty `json:"properties"`
}

// GetRowFilterCapabilities answers the narrow, server-to-server validation
// contract used before a row-filter policy is stored. "ExactFilterable" means
// the same mapped data property and scalar type are accepted by the existing
// row-filter compiler; it deliberately excludes logic and system properties.
func (r *restHandler) GetRowFilterCapabilities(c *gin.Context) {
	var request rowFilterCapabilityRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		rest.ReplyError(c, rest.NewHTTPError(c, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter))
		return
	}
	parts := strings.Split(strings.TrimSpace(request.ObjectTypeRef), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		rest.ReplyError(c, rest.NewHTTPError(c, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter))
		return
	}
	if r.oma == nil {
		rest.ReplyError(c, rest.NewHTTPError(c, http.StatusServiceUnavailable, oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed))
		return
	}
	objectType, exists, err := r.oma.GetObjectType(c, parts[0], interfaces.MAIN_BRANCH, parts[1])
	if err != nil {
		rest.ReplyError(c, rest.NewHTTPError(c, http.StatusServiceUnavailable, oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed))
		return
	}
	if !exists || (objectType.KNID != "" && objectType.KNID != parts[0]) {
		c.JSON(http.StatusOK, rowFilterCapabilityResponse{ObjectTypeRef: request.ObjectTypeRef, Properties: map[string]rowFilterCapabilityProperty{}})
		return
	}
	c.JSON(http.StatusOK, rowFilterCapabilityForObjectType(request.ObjectTypeRef, objectType))
}

func rowFilterCapabilityForObjectType(objectTypeRef string, objectType interfaces.ObjectType) rowFilterCapabilityResponse {
	response := rowFilterCapabilityResponse{
		ObjectTypeRef: objectTypeRef,
		// An index-unavailable object type must not receive a stored policy:
		// execution cannot guarantee that the predicate reaches the backend.
		Published:  objectType.Status != nil && objectType.Status.IndexAvailable,
		Properties: map[string]rowFilterCapabilityProperty{},
	}
	for _, property := range objectType.DataProperties {
		valueType, supported := rowFilterValueType(property)
		if !supported || strings.TrimSpace(property.Name) == "" || strings.TrimSpace(property.MappedField.Name) == "" {
			continue
		}
		response.Properties[property.Name] = rowFilterCapabilityProperty{Type: valueType, ExactFilterable: true}
	}
	return response
}

func rowFilterValueType(property cond.DataProperty) (string, bool) {
	switch {
	case dtype.SimpleTypeMapping[property.Type] == dtype.SimpleChar || dtype.DataType_IsString(property.Type):
		return "string", true
	case dtype.SimpleTypeMapping[property.Type] == dtype.SimpleInt || dtype.DataType_IsNumber(property.Type):
		return "integer", true
	case property.Type == dtype.DATATYPE_BOOLEAN || dtype.SimpleTypeMapping[property.Type] == dtype.SimpleBool:
		return "boolean", true
	default:
		return "", false
	}
}
