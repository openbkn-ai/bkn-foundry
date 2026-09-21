// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	rowfilter "ontology-query/logics/row_filter"
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
		// Looking up a model from MAIN is the publication boundary. Index status
		// is deliberately not used here: both OpenSearch and resource-backed
		// object types must prove exact filtering at the property level instead.
		Published:  true,
		Properties: map[string]rowFilterCapabilityProperty{},
	}
	for _, property := range objectType.DataProperties {
		valueType, supported := rowfilter.ExactFilterValueType(property)
		if !supported {
			continue
		}
		response.Properties[property.Name] = rowFilterCapabilityProperty{Type: valueType, ExactFilterable: true}
	}
	return response
}
