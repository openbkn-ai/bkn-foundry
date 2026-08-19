package business_domain

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	infracommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type businessDomainServiceImpl struct {
	logger       interfaces.Logger
	bdManagement interfaces.BusinessDomainManagement
}

// NewBusinessDomainService creates a business domain service instance.
func NewBusinessDomainService() interfaces.IBusinessDomainService {
	if !config.GetBusinessDomainEnabled() {
		return &noopBusinessDomainService{}
	}
	return &businessDomainServiceImpl{
		logger:       config.NewConfigLoader().GetLogger(),
		bdManagement: drivenadapters.NewBusinessDomainManagementClient(),
	}
}

// GetBusinessDomainFromHeader Gets the business domain from the Header and checks whether it exists.
func (s *businessDomainServiceImpl) GetBusinessDomainFromHeader(c *gin.Context) (businessDomain string) {
	businessDomain = c.GetHeader(string(interfaces.HeaderXBusinessDomain))
	return businessDomain
}

// ValidateBusinessDomain Verifies whether the business domain exists.
func (s *businessDomainServiceImpl) ValidateBusinessDomain(ctx context.Context) (err error) {
	_, ok := infracommon.GetBusinessDomainFromCtx(ctx)
	if !ok {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtBusinessDomainIDRequired, "x-business-domain-id is required")
		return err
	}
	return nil
}

// AssociateResource associates resources to business domains.
func (s *businessDomainServiceImpl) AssociateResource(ctx context.Context, bdId, resourceId string, resourceType interfaces.AuthResourceType) error {
	req := &interfaces.BusinessDomainResourceAssociateRequest{
		BDID: bdId,
		ID:   resourceId,
		Type: string(resourceType),
	}

	err := s.bdManagement.AssociateResource(ctx, req)
	if err != nil {
		s.logger.Errorf("AssociateResource failed: %v, bdId: %s, resourceID: %s, resourceType: %s",
			err, bdId, resourceId, resourceType)
		return err
	}

	s.logger.Infof("AssociateResource success, bdId: %s, resourceID: %s, resourceType: %s",
		bdId, resourceId, resourceType)
	return nil
}

// BatchDisassociateResource Disassociates resources from business domains in batches.
func (s *businessDomainServiceImpl) BatchDisassociateResource(ctx context.Context, bdID string, resourceIds []string, resourceType interfaces.AuthResourceType) (err error) {
	if len(resourceIds) == 0 {
		return
	}

	for _, resourceId := range resourceIds {
		err = s.DisassociateResource(ctx, bdID, resourceId, resourceType)
		if err != nil {
			return err
		}
	}
	return
}

// DisassociateResource Disassociates the resource from the business domain.
func (s *businessDomainServiceImpl) DisassociateResource(ctx context.Context, bdId, resourceId string, resourceType interfaces.AuthResourceType) error {
	req := &interfaces.BusinessDomainResourceDisassociateRequest{
		BDID: bdId,
		ID:   resourceId,
		Type: string(resourceType),
	}

	err := s.bdManagement.DisassociateResource(ctx, req)
	if err != nil {
		s.logger.Errorf("DisassociateResource failed: %v, bdId: %s, resourceID: %s, resourceType: %s",
			err, bdId, resourceId, resourceType)
		return err
	}

	s.logger.Infof("DisassociateResource success, bdId: %s, resourceID: %s, resourceType: %s",
		bdId, resourceId, resourceType)
	return nil
}

// ResourceList Query the resource list under the business domain.
func (s *businessDomainServiceImpl) ResourceList(ctx context.Context, bdId string, resourceType interfaces.AuthResourceType) ([]string, error) {
	req := &interfaces.BusinessDomainResourceListRequest{
		BDID:   bdId,
		Type:   string(resourceType),
		Limit:  -1, // Set to -1 to indicate no paging and get all data.
		Offset: 0,
	}

	resp, err := s.bdManagement.ResourceList(ctx, req)
	if err != nil {
		s.logger.Errorf("ResourceList failed: %v, bdId: %s, resourceType: %s",
			err, bdId, resourceType)
		return nil, err
	}

	// Extract resource ID list.
	resourceIDs := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		resourceIDs = append(resourceIDs, item.ID)
	}

	s.logger.Infof("ResourceList success, bdId: %s, resourceType: %s, count: %d",
		bdId, resourceType, len(resourceIDs))
	return resourceIDs, nil
}

// BatchResourceList Batch query resource list under multiple business domains.
func (s *businessDomainServiceImpl) BatchResourceList(ctx context.Context, bdIds []string, resourceType interfaces.AuthResourceType) (resourceToBdMap map[string]string, err error) {
	// Initialization return result.
	resourceToBdMap = make(map[string]string)

	// Traverse all business domain IDs.
	for _, bdId := range bdIds {
		// Call the resource list method of a single business domain.
		resourceIds, err := s.ResourceList(ctx, bdId, resourceType)
		if err != nil {
			s.logger.Errorf("BatchResourceList failed for bdId %s: %v", bdId, err)
			// Return an error and do not continue processing other business domains.
			return nil, err
		}

		// Add the mapping relationship between resource ID and business domain ID to the results.
		for _, resourceId := range resourceIds {
			resourceToBdMap[resourceId] = bdId
		}
	}
	return resourceToBdMap, nil
}
