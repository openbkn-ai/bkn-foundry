// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/authzresource"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// registerAuthorizationRegistry exposes the catalog on the ClusterIP-only authz
// surface for service-to-service callers. Browser callers must use
// registerMeAuthorizationRegistry instead; it is protected by RequireUser.
func registerAuthorizationRegistry(g *gin.RouterGroup, registry *authz.Registry) {
	g.GET("/registry", authorizationRegistryHandler(registry))
}

// registerMeAuthorizationRegistry exposes the same persisted contract to a
// signed-in browser through the gateway-exposed /me surface. The catalog has no
// grants, principals, or concrete resource ids, but it must not make the
// tokenless /authz surface browser-reachable.
func registerMeAuthorizationRegistry(g *gin.RouterGroup, registry *authz.Registry) {
	g.GET("/authorization-registry", authorizationRegistryHandler(registry))
}

// authorizationRegistryHandler exposes the same startup snapshot used by the enforcer.
func authorizationRegistryHandler(registry *authz.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		resourceTypes, err := registry.ResourceTypes(c.Request.Context())
		if err != nil {
			serverError(c, err)
			return
		}
		operations, err := registry.Operations(c.Request.Context(), "")
		if err != nil {
			serverError(c, err)
			return
		}
		operationsByType := make(map[string][]authorizationRegistryOperation, len(resourceTypes))
		for _, operation := range operations {
			if !authzresource.Entitled(operation.ResourceTypeID) {
				continue
			}
			requirementsByOperation, err := registry.Requirements(c.Request.Context(), operation.ResourceTypeID, []string{operation.ID})
			if err != nil {
				serverError(c, err)
				return
			}
			requirements := requirementsByOperation[operation.ID]
			if requirements == nil {
				requirements = []string{}
			}
			operationsByType[operation.ResourceTypeID] = append(
				operationsByType[operation.ResourceTypeID],
				authorizationRegistryOperation{
					ID:              operation.ID,
					Name:            operation.Name,
					Description:     operation.Description,
					Grantable:       operation.IsGrantable(),
					ParentOperation: operation.ParentOperationID,
					Requires:        requirements,
				},
			)
		}

		items := make([]authorizationRegistryResourceType, 0, len(resourceTypes))
		for _, resourceType := range resourceTypes {
			// A paid type below its edition is left out like an absent module (#2512).
			if !authzresource.Entitled(resourceType.ID) {
				continue
			}
			items = append(items, authorizationRegistryResourceType{
				ID:         resourceType.ID,
				Name:       resourceType.Name,
				ParentType: resourceType.ParentTypeID,
				Operations: operationsByType[resourceType.ID],
			})
		}
		c.JSON(http.StatusOK, gin.H{"resource_types": items})
	}
}

type authorizationRegistryResourceType struct {
	ID         string                           `json:"id"`
	Name       string                           `json:"name"`
	ParentType string                           `json:"parent_type,omitempty"`
	Operations []authorizationRegistryOperation `json:"operations"`
}

type authorizationRegistryOperation struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Grantable       bool     `json:"grantable"`
	ParentOperation string   `json:"parent_operation,omitempty"`
	Requires        []string `json:"requires"`
}
