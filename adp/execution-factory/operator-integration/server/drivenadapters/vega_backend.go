package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	vegaBackendOnce     sync.Once
	vegaBackendInstance interfaces.VegaBackendClient
)

type vegaBackendClient struct {
	baseURL    string
	logger     interfaces.Logger
	httpClient interfaces.HTTPClient
}

func NewVegaBackendClient() interfaces.VegaBackendClient {
	vegaBackendOnce.Do(func() {
		conf := config.NewConfigLoader()
		vegaBackendInstance = &vegaBackendClient{
			baseURL: fmt.Sprintf("%s://%s:%d/api/vega-backend/in", conf.VegaBackend.PrivateProtocol,
				conf.VegaBackend.PrivateHost, conf.VegaBackend.PrivatePort),
			logger:     conf.GetLogger(),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return vegaBackendInstance
}

func (v *vegaBackendClient) buildHeaders(ctx context.Context) map[string]string {
	headers := common.GetHeaderFromCtx(ctx)
	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Content-Type"] = "application/json"
	if accountID, ok := headers[string(interfaces.HeaderXAccountID)]; !ok || accountID == "" {
		headers[string(interfaces.HeaderXAccountID)] = interfaces.ADMIN_ACCOUNT_ID
		headers[string(interfaces.HeaderXAccountType)] = interfaces.ADMIN_ACCOUNT_TYPE
	}
	return headers
}

// GetCatalogByID Gets the Vega catalog.
func (v *vegaBackendClient) GetCatalogByID(ctx context.Context, id string) (*interfaces.VegaCatalog, error) {
	src := fmt.Sprintf("%s/v1/catalogs/%s", v.baseURL, url.PathEscape(id))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("get vega catalog, catalog_id=%s, url=%s", id, src)
	respCode, respData, err := v.httpClient.GetNoUnmarshal(ctx, src, nil, headers)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to get vega catalog, catalog_id=%s, url=%s, err=%v", id, src, err)
		return nil, err
	}
	if respCode == http.StatusNotFound {
		return nil, nil
	}
	if respCode != http.StatusOK {
		return nil, fmt.Errorf("get catalog by id failed: %s", string(respData))
	}

	// Vega's query by ID uses the batch endpoint, and the response is {"entries":[...]} packaging;
	// Direct deserialization into VegaCatalog will only result in a zero value (ID is empty). Historically, the caller only determined.
	// nil without being exposed.
	var entries struct {
		Entries []*interfaces.VegaCatalog `json:"entries"`
	}
	if err = json.Unmarshal(respData, &entries); err == nil && len(entries.Entries) > 0 {
		// Select by id and do not recognize the order of the list: the caller (adoption of the existing directory) is strongly dependent on getting the requested directory.
		for _, entry := range entries.Entries {
			if entry != nil && entry.ID == id {
				return entry, nil
			}
		}
		return nil, nil
	}

	catalog := &interfaces.VegaCatalog{}
	if err = json.Unmarshal(respData, catalog); err != nil {
		v.logger.WithContext(ctx).Errorf("failed to unmarshal catalog: %v", err)
		return nil, err
	}
	if catalog.ID == "" {
		return nil, nil
	}
	return catalog, nil
}

// CreateCatalog creates a Vega catalog.
func (v *vegaBackendClient) CreateCatalog(ctx context.Context, req *interfaces.VegaCatalogRequest) (*interfaces.VegaCatalog, error) {
	src := fmt.Sprintf("%s/v1/catalogs", v.baseURL)
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("create vega catalog, catalog_id=%s, url=%s", req.ID, src)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, src, headers, req)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to create vega catalog, catalog_id=%s, url=%s, err=%v", req.ID, src, err)
		return nil, err
	}
	if respCode != http.StatusCreated && respCode != http.StatusOK {
		return nil, fmt.Errorf("create catalog failed: %s", string(respData))
	}

	catalog := &interfaces.VegaCatalog{}
	if err = json.Unmarshal(respData, catalog); err != nil {
		v.logger.WithContext(ctx).Errorf("failed to unmarshal created catalog: %v", err)
		return nil, err
	}
	return catalog, nil
}

// UpdateCatalog updates the display information of the Vega catalog (name/tag/description)
// vega's PUT /catalogs/{id} requires connector_type and enabled to be consistent with the current values.
// The caller must backfill these two fields unchanged from the return of GetCatalogByID.
func (v *vegaBackendClient) UpdateCatalog(ctx context.Context, req *interfaces.VegaCatalogRequest) error {
	src := fmt.Sprintf("%s/v1/catalogs/%s", v.baseURL, url.PathEscape(req.ID))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("update vega catalog, catalog_id=%s, name=%s, url=%s", req.ID, req.Name, src)
	respCode, respData, err := v.httpClient.PutNoUnmarshal(ctx, src, headers, req)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to update vega catalog, catalog_id=%s, url=%s, err=%v", req.ID, src, err)
		return err
	}
	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		return fmt.Errorf("update catalog failed: %s", string(respData))
	}
	return nil
}

// EnableCatalog enables Vega catalog.
func (v *vegaBackendClient) EnableCatalog(ctx context.Context, id string) error {
	src := fmt.Sprintf("%s/v1/catalogs/%s/enable", v.baseURL, url.PathEscape(id))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("enable vega catalog, catalog_id=%s, url=%s", id, src)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, src, headers, nil)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to enable vega catalog, catalog_id=%s, url=%s, err=%v", id, src, err)
		return err
	}
	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		return fmt.Errorf("enable catalog failed: %s", string(respData))
	}
	return nil
}

func (v *vegaBackendClient) GetResourceByID(ctx context.Context, id string) (*interfaces.VegaResource, error) {
	src := fmt.Sprintf("%s/v1/resources/%s", v.baseURL, url.PathEscape(id))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("get vega resource, resource_id=%s, url=%s", id, src)
	respCode, respData, err := v.httpClient.GetNoUnmarshal(ctx, src, nil, headers)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to get vega resource, resource_id=%s, url=%s, err=%v", id, src, err)
		return nil, err
	}
	if respCode == http.StatusNotFound {
		return nil, nil
	}
	if respCode != http.StatusOK {
		return nil, fmt.Errorf("get resource by id failed: %s", string(respData))
	}

	var entries struct {
		Entries []*interfaces.VegaResource `json:"entries"`
	}
	if err = json.Unmarshal(respData, &entries); err == nil && len(entries.Entries) > 0 {
		// Same as GetCatalogByID: select by id. When adopting an existing dataset, you must confirm that it is the one you get.
		for _, entry := range entries.Entries {
			if entry != nil && entry.ID == id {
				return entry, nil
			}
		}
		return nil, nil
	}

	resource := &interfaces.VegaResource{}
	if err = json.Unmarshal(respData, resource); err != nil {
		v.logger.WithContext(ctx).Errorf("failed to unmarshal resource: %v", err)
		return nil, err
	}
	if resource.ID == "" {
		return nil, nil
	}
	return resource, nil
}

func (v *vegaBackendClient) CreateResource(ctx context.Context, req *interfaces.VegaResourceRequest) (*interfaces.VegaResource, error) {
	src := fmt.Sprintf("%s/v1/resources", v.baseURL)
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("create vega resource, resource_id=%s, catalog_id=%s, url=%s", req.ID, req.CatalogID, src)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, src, headers, req)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to create vega resource, resource_id=%s, catalog_id=%s, url=%s, err=%v", req.ID, req.CatalogID, src, err)
		return nil, err
	}
	if respCode != http.StatusCreated && respCode != http.StatusOK {
		return nil, fmt.Errorf("create resource failed: %s", string(respData))
	}

	resource := &interfaces.VegaResource{}
	if err = json.Unmarshal(respData, resource); err != nil {
		v.logger.WithContext(ctx).Errorf("failed to unmarshal created resource: %v", err)
		return nil, err
	}
	return resource, nil
}

func (v *vegaBackendClient) WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error {
	src := fmt.Sprintf("%s/v1/resources/%s/data", v.baseURL, url.PathEscape(datasetID))
	headers := v.buildHeaders(ctx)
	headers["X-HTTP-Method-Override"] = "POST"
	v.logger.WithContext(ctx).Infof("write vega dataset documents, resource_id=%s, documents=%d, url=%s", datasetID, len(documents), src)
	respCode, respData, err := v.httpClient.PostNoUnmarshal(ctx, src, headers, documents)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to write vega dataset documents, resource_id=%s, documents=%d, url=%s, err=%v", datasetID, len(documents), src, err)
		return err
	}
	if respCode != http.StatusCreated && respCode != http.StatusOK {
		return fmt.Errorf("write dataset documents failed: %s", string(respData))
	}
	return nil
}

func (v *vegaBackendClient) UpdateDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error {
	src := fmt.Sprintf("%s/v1/resources/%s/data", v.baseURL, url.PathEscape(datasetID))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("update vega dataset documents, resource_id=%s, documents=%d, url=%s", datasetID, len(documents), src)
	respCode, respData, err := v.httpClient.PutNoUnmarshal(ctx, src, headers, documents)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to update vega dataset documents, resource_id=%s, documents=%d, url=%s, err=%v", datasetID, len(documents), src, err)
		return err
	}
	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		return fmt.Errorf("update dataset documents failed: %s", string(respData))
	}
	return nil
}

func (v *vegaBackendClient) DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error {
	src := fmt.Sprintf("%s/v1/resources/%s/data/%s", v.baseURL, url.PathEscape(datasetID), url.PathEscape(docID))
	headers := v.buildHeaders(ctx)
	v.logger.WithContext(ctx).Infof("delete vega dataset document, resource_id=%s, doc_id=%s, url=%s", datasetID, docID, src)
	respCode, respData, err := v.httpClient.DeleteNoUnmarshal(ctx, src, headers)
	if err != nil {
		v.logger.WithContext(ctx).Errorf("failed to delete vega dataset document, resource_id=%s, doc_id=%s, url=%s, err=%v", datasetID, docID, src, err)
		return err
	}
	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		return fmt.Errorf("delete dataset document failed: %s", string(respData))
	}
	return nil
}
