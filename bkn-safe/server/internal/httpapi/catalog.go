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

// registerAuthorizationCatalog exposes the catalog that is currently effective
// in bkn-safe's database.  Consumers must use this endpoint rather than carry
// a second, hand-maintained copy of authorization-catalog.json: seed may change the catalog
// during an upgrade and the enforcer evaluates these persisted rows.
//
// The authz face is ClusterIP-internal.  The endpoint intentionally contains no
// grants, principals, or concrete resource ids, so it is safe for a domain
// service to proxy to its authorization UI.
func registerAuthorizationCatalog(g *gin.RouterGroup, db *gorm.DB) {
	g.GET("/catalog", func(c *gin.Context) {
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

		operationsByType := make(map[string][]authorizationCatalogOperation, len(resourceTypes))
		for _, operation := range operations {
			operationsByType[operation.ResourceTypeID] = append(
				operationsByType[operation.ResourceTypeID],
				authorizationCatalogOperation{
					ID:              operation.ID,
					Name:            operation.Name,
					ParentOperation: operation.ParentOperationID,
					Requires:        operationRequirements(operation.RequiredOperationIDs),
				},
			)
		}

		items := make([]authorizationCatalogResourceType, 0, len(resourceTypes))
		for _, resourceType := range resourceTypes {
			items = append(items, authorizationCatalogResourceType{
				ID:         resourceType.ID,
				Name:       resourceType.Name,
				ParentType: resourceType.ParentTypeID,
				Operations: operationsByType[resourceType.ID],
			})
		}
		c.JSON(http.StatusOK, gin.H{"resource_types": items})
	})
}

type authorizationCatalogResourceType struct {
	ID         string                          `json:"id"`
	Name       string                          `json:"name"`
	ParentType string                          `json:"parent_type,omitempty"`
	Operations []authorizationCatalogOperation `json:"operations"`
}

type authorizationCatalogOperation struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
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
