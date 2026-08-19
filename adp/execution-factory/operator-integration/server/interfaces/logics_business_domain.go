package interfaces

import (
	"context"

	"github.com/gin-gonic/gin"
)

//go:generate mockgen -source=logics_business_domain.go -destination=../mocks/logics_business_domain.go -package=mocks

// IBusinessDomainService business domain service interface.
type IBusinessDomainService interface {
	// Get the business domain from the Header and verify whether it exists.
	GetBusinessDomainFromHeader(c *gin.Context) (businessDomain string)
	// ValidateBusinessDomain Verifies whether the business domain exists.
	ValidateBusinessDomain(ctx context.Context) (err error)
	// AssociateResource associates resources to business domains.
	AssociateResource(ctx context.Context, bdID, resourceID string, resourceType AuthResourceType) (err error)

	// DisassociateResource Disassociates the resource from the business domain.
	DisassociateResource(ctx context.Context, bdID, resourceID string, resourceType AuthResourceType) (err error)

	// BatchDisassociateResource Disassociates resources from business domains in batches.
	BatchDisassociateResource(ctx context.Context, bdID string, resourceIds []string, resourceType AuthResourceType) (err error)

	// ResourceList Query the resource list under the business domain.
	ResourceList(ctx context.Context, bdID string, resourceType AuthResourceType) (resourceIDs []string, err error)

	// BatchResourceList Batch query resource list under multiple business domains.
	BatchResourceList(ctx context.Context, bdIds []string, resourceType AuthResourceType) (resourceToBdMap map[string]string, err error)
}
