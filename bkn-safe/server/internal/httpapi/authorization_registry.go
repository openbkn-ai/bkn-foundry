// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// registerAuthorizationRegistry exposes the catalog on the ClusterIP-only authz
// surface for service-to-service callers. Browser callers must use
// registerMeAuthorizationRegistry instead; it is protected by RequireUser.
func registerAuthorizationRegistry(g *gin.RouterGroup, db *gorm.DB) {
	g.GET("/registry", authorizationRegistryHandler(db))
}

// registerMeAuthorizationRegistry exposes the same persisted contract to a
// signed-in browser through the gateway-exposed /me surface. The catalog has no
// grants, principals, or concrete resource ids, but it must not make the
// tokenless /authz surface browser-reachable.
func registerMeAuthorizationRegistry(g *gin.RouterGroup, db *gorm.DB) {
	g.GET("/authorization-registry", authorizationRegistryHandler(db))
}

// authorizationRegistryHandler reads the catalog currently effective in
// bkn-safe's database. Consumers must not keep a second hand-maintained copy:
// seed may change it during an upgrade and the enforcer evaluates these rows.
func authorizationRegistryHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var resourceTypes []model.ResourceType
		if err := db.WithContext(c.Request.Context()).
			Order("id ASC").
			Find(&resourceTypes).Error; err != nil {
			serverError(c, err)
			return
		}

		var operations []model.Operation
		if err := db.WithContext(c.Request.Context()).
			Order("resource_type_id ASC").
			Order("id ASC").
			Find(&operations).Error; err != nil {
			serverError(c, err)
			return
		}

		operationsByType := make(map[string][]authorizationRegistryOperation, len(resourceTypes))
		for _, operation := range operations {
			operationsByType[operation.ResourceTypeID] = append(
				operationsByType[operation.ResourceTypeID],
				authorizationRegistryOperation{
					ID:              operation.ID,
					Name:            operation.Name,
					Description:     operation.Description,
					Grantable:       operation.IsGrantable(),
					ParentOperation: operation.ParentOperationID,
					Requires:        operationRequirements(operation.RequiredOperationIDs),
				},
			)
		}

		items := make([]authorizationRegistryResourceType, 0, len(resourceTypes))
		for _, resourceType := range resourceTypes {
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

func operationRequirements(value string) []string {
	if value == "" {
		return []string{}
	}
	seen := make(map[string]bool)
	requires := make([]string, 0)
	for _, operation := range strings.Split(value, ",") {
		operation = strings.TrimSpace(operation)
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		requires = append(requires, operation)
	}
	return requires
}
