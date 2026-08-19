// Package drivenadapters defines driver adapters.
// @file business_domain_management.go
// @description: Implement business domain management services.
package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

var (
	bdOnce sync.Once
	bdm    interfaces.BusinessDomainManagement
)

type businessDomainManagementClient struct {
	baseURL    string
	logger     interfaces.Logger
	httpClient interfaces.HTTPClient
}

// NewBusinessDomainManagementClient creates a business domain management service object.
func NewBusinessDomainManagementClient() interfaces.BusinessDomainManagement {
	bdOnce.Do(func() {
		conf := config.NewConfigLoader()
		bdm = &businessDomainManagementClient{
			baseURL: fmt.Sprintf("%s://%s:%d/internal/api/business-system/v1", conf.BusinessDomainManagement.PrivateProtocol,
				conf.BusinessDomainManagement.PrivateHost, conf.BusinessDomainManagement.PrivatePort),
			logger:     conf.GetLogger(),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return bdm
}

// AssociateResource associates resources to business domains.
func (b *businessDomainManagementClient) AssociateResource(ctx context.Context, req *interfaces.BusinessDomainResourceAssociateRequest) error {
	src := fmt.Sprintf("%s/resource", b.baseURL)
	// header := common.GetHeaderFromCtx(ctx)
	header := map[string]string{}

	respCode, _, err := b.httpClient.Post(ctx, src, header, req)

	// Handling 403 Insufficient Permissions.
	if respCode == http.StatusForbidden {
		b.logger.Errorf("businessDomainManagementClient#AssociateResource failed:%v, url:%v", err, src)
		err = infraErr.NewHTTPError(ctx, http.StatusForbidden, infraErr.ErrExtBusinessDomainForbidden, err.Error())
		return err
	}

	// Handling 409 Resource Already Associated Conflict.
	if respCode == http.StatusConflict {
		b.logger.Warnf("businessDomainManagementClient#AssociateResource conflict: resource already connected, resource_id:%s, domain_id:%s", req.ID, req.BDID)
		err = infraErr.NewHTTPError(ctx, http.StatusConflict, infraErr.ErrExtBusinessDomainResourceConflict, err.Error())
		return err
	}

	if err != nil {
		b.logger.Errorf("businessDomainManagementClient#AssociateResource failed:%v, url:%v", err, src)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return err
	}

	b.logger.Infof("businessDomainManagementClient#AssociateResource success, resource_id:%s, domain_id:%s", req.ID, req.BDID)
	return nil
}

// DisassociateResource Disassociates the resource from the business domain.
func (b *businessDomainManagementClient) DisassociateResource(ctx context.Context, req *interfaces.BusinessDomainResourceDisassociateRequest) error {
	// Build query parameters.
	queryParams := url.Values{}
	queryParams.Add("id", req.ID)
	queryParams.Add("type", req.Type)
	queryParams.Add("bd_id", req.BDID)
	src := fmt.Sprintf("%s/resource?%s", b.baseURL, queryParams.Encode())
	// header := common.GetHeaderFromCtx(ctx)
	header := map[string]string{}

	respCode, _, err := b.httpClient.Delete(ctx, src, header)
	if respCode == http.StatusForbidden {
		b.logger.Errorf("businessDomainManagementClient#DisassociateResource failed:%v, url:%v", err, src)
		err = infraErr.NewHTTPError(ctx, http.StatusForbidden, infraErr.ErrExtBusinessDomainForbidden, err.Error())
		return err
	}
	if err != nil {
		b.logger.Errorf("businessDomainManagementClient#DisassociateResource failed:%v, url:%v", err, src)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return err
	}

	b.logger.Infof("businessDomainManagementClient#DisassociateResource success, resource_id:%s, domain_id:%s", req.ID, req.BDID)
	return nil
}

// ResourceList Query the resource list under the business domain.
func (b *businessDomainManagementClient) ResourceList(ctx context.Context, req *interfaces.BusinessDomainResourceListRequest) (*interfaces.BusinessDomainResourceListResponse, error) {
	src := fmt.Sprintf("%s/resource", b.baseURL)
	// header := common.GetHeaderFromCtx(ctx)
	header := map[string]string{}

	// Build query parameters.
	queryParams := url.Values{}
	if req.BDID != "" {
		queryParams.Add("bd_id", req.BDID)
	}
	if req.ID != "" {
		queryParams.Add("id", req.ID)
	}
	if req.Type != "" {
		queryParams.Add("type", req.Type)
	}
	queryParams.Add("limit", fmt.Sprintf("%d", req.Limit))

	if req.Offset > 0 {
		queryParams.Add("offset", fmt.Sprintf("%d", req.Offset))
	}

	respCode, respParam, err := b.httpClient.Get(ctx, src, queryParams, header)
	if respCode == http.StatusForbidden {
		b.logger.Errorf("businessDomainManagementClient#ResourceList failed:%v, url:%v", err, src)
		err = infraErr.NewHTTPError(ctx, http.StatusForbidden, infraErr.ErrExtBusinessDomainForbidden, err.Error())
		return nil, err
	}
	if err != nil {
		b.logger.Errorf("businessDomainManagementClient#ResourceList failed:%v, url:%v", err, src)
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}

	result := &interfaces.BusinessDomainResourceListResponse{}
	resultByt := utils.ObjectToByte(respParam)
	err = jsoniter.Unmarshal(resultByt, result)
	if err != nil {
		b.logger.Errorf("businessDomainManagementClient#ResourceList response unmarshal error:%s", err.Error())
		err = infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}

	b.logger.Infof("businessDomainManagementClient#ResourceList success, bd_id:%s, total:%d", req.BDID, result.Total)
	return result, nil
}
