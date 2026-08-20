// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	vbAccessOnce     sync.Once
	vbAccess         interfaces.VegaBackendAccess
	vegaResponseJSON = sonic.Config{UseNumber: true}.Froze()
)

type vegaBackendAccess struct {
	appSetting *common.AppSetting
	httpClient rest.HTTPClient
	baseUrl    string
}

// NewVegaBackendAccess creates a new vega-backend access instance
func NewVegaBackendAccess(appSetting *common.AppSetting) interfaces.VegaBackendAccess {
	vbAccessOnce.Do(func() {
		vbAccess = &vegaBackendAccess{
			appSetting: appSetting,
			httpClient: common.NewHTTPClient(),
			baseUrl:    appSetting.VegaBackendUrl,
		}
	})

	return vbAccess
}

func (vba *vegaBackendAccess) buildHeaders(ctx context.Context) map[string]string {
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}

	// accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
		headers[interfaces.HTTP_HEADER_ACCOUNT_ID] = accountInfo.ID
		headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] = accountInfo.Type
	} else {
		headers[interfaces.HTTP_HEADER_ACCOUNT_ID] = interfaces.ADMIN_ACCOUNT_ID
		headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] = interfaces.ADMIN_ACCOUNT_TYPE
	}

	return headers
}

func (vba *vegaBackendAccess) GetCatalogByID(ctx context.Context, id string) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Get catalog by ID")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_id").String(id))

	httpUrl := fmt.Sprintf("%s/catalogs/%s", vba.baseUrl, url.PathEscape(id))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetCatalogByID finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "GetCatalogByID http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get catalog by ID failed")
		return nil, fmt.Errorf("Vega dependency request failed")
	}

	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}

	if respCode != http.StatusOK {
		err := fmt.Errorf("GetCatalogByID returned HTTP %d", respCode)
		common.LogSafeError(ctx, "GetCatalogByID failed", err)
		logger.Debugf("GetCatalogByID response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, err
	}

	var catalog interfaces.Catalog
	if err := json.Unmarshal([]byte(respData), &catalog); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal GetCatalogByID response", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal GetCatalogByID response failed")
		return nil, fmt.Errorf("failed to unmarshal GetCatalogByID response: %v", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &catalog, nil
}

func (vba *vegaBackendAccess) CreateCatalog(ctx context.Context, req *interfaces.CatalogRequest) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Create catalog")
	defer span.End()

	span.SetAttributes(attr.Key("catalog_name").String(req.Name))

	httpUrl := fmt.Sprintf("%s/catalogs", vba.baseUrl)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, req)
	logger.Debugf("CreateCatalog finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "CreateCatalog http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http create catalog failed")
		return nil, fmt.Errorf("Vega dependency request failed")
	}

	if respCode != http.StatusCreated && respCode != http.StatusOK {
		err := fmt.Errorf("CreateCatalog returned HTTP %d", respCode)
		common.LogSafeError(ctx, "CreateCatalog failed", err)
		logger.Debugf("CreateCatalog response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201 or 200")
		return nil, err
	}

	var catalog interfaces.Catalog
	if err := json.Unmarshal([]byte(respData), &catalog); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal CreateCatalog response", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal CreateCatalog response failed")
		return nil, fmt.Errorf("failed to unmarshal CreateCatalog response: %v", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &catalog, nil
}

func (vba *vegaBackendAccess) GetResourceByID(ctx context.Context, id string) (*interfaces.VegaResource, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Get resource by ID")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(id))

	httpUrl := fmt.Sprintf("%s/resources/%s", vba.baseUrl, url.PathEscape(id))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetResourceByID finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "GetResourceByID http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get resource by ID failed")
		return nil, fmt.Errorf("Vega dependency request failed")
	}

	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}

	if respCode != http.StatusOK {
		err := fmt.Errorf("GetResourceByID returned HTTP %d", respCode)
		common.LogSafeError(ctx, "GetResourceByID failed", err)
		logger.Debugf("GetResourceByID response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, err
	}

	var resourceData struct {
		Entries []*interfaces.VegaResource `json:"entries"`
	}
	if err := json.Unmarshal([]byte(respData), &resourceData); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal GetResourceByID response", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal GetResourceByID response failed")
		return nil, fmt.Errorf("failed to unmarshal GetResourceByID response: %v", err)
	}

	if len(resourceData.Entries) == 0 {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return resourceData.Entries[0], nil
}

func (vba *vegaBackendAccess) CreateResource(ctx context.Context, req *interfaces.VegaResource) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Create resource")
	defer span.End()

	span.SetAttributes(attr.Key("resource_name").String(req.Name))

	httpUrl := fmt.Sprintf("%s/resources", vba.baseUrl)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, req)
	logger.Debugf("CreateResource finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "CreateResource http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http create resource failed")
		return fmt.Errorf("Vega dependency request failed")
	}

	if respCode != http.StatusCreated && respCode != http.StatusOK {
		err := fmt.Errorf("CreateResource returned HTTP %d", respCode)
		common.LogSafeError(ctx, "CreateResource failed", err)
		logger.Errorf("CreateResource failed: response_code=%d, response=%s", respCode, string(respData))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201 or 200")
		return err
	}

	var resource interfaces.VegaResource
	if err := json.Unmarshal([]byte(respData), &resource); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal CreateResource response", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal CreateResource response failed")
		return fmt.Errorf("failed to unmarshal CreateResource response: %v", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

func (vba *vegaBackendAccess) DeleteResource(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Delete resource")
	defer span.End()

	span.SetAttributes(attr.Key("resource_id").String(id))

	httpUrl := fmt.Sprintf("%s/resources/%s", vba.baseUrl, url.PathEscape(id))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodDelete,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.DeleteNoUnmarshal(ctx, httpUrl, headers)
	logger.Debugf("DeleteResource finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "DeleteResource http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http delete resource failed")
		return fmt.Errorf("Vega dependency request failed")
	}

	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		err := fmt.Errorf("DeleteResource returned HTTP %d", respCode)
		common.LogSafeError(ctx, "DeleteResource failed", err)
		logger.Debugf("DeleteResource response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 204 or 200")
		return err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

func (vba *vegaBackendAccess) DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Delete dataset document by ID")
	defer span.End()

	span.SetAttributes(attr.Key("dataset_id").String(datasetID))
	span.SetAttributes(attr.Key("doc_id").String(docID))

	httpUrl := fmt.Sprintf("%s/resources/%s/data/%s", vba.baseUrl, url.PathEscape(datasetID), url.PathEscape(docID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:    httpUrl,
		HttpMethod: http.MethodDelete,
	})

	headers := vba.buildHeaders(ctx)
	respCode, respData, err := vba.httpClient.DeleteNoUnmarshal(ctx, httpUrl, headers)
	logger.Debugf("DeleteDatasetDocumentByID finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "DeleteDatasetDocumentByID http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http delete dataset document by ID failed")
		return fmt.Errorf("Vega dependency request failed")
	}

	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		err := fmt.Errorf("DeleteDatasetDocumentByID returned HTTP %d", respCode)
		common.LogSafeError(ctx, "DeleteDatasetDocumentByID failed", err)
		logger.Debugf("DeleteDatasetDocumentByID response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 204 or 200")
		return err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

func (vba *vegaBackendAccess) DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Delete dataset documents by query")
	defer span.End()

	span.SetAttributes(attr.Key("dataset_id").String(datasetID))

	httpUrl := fmt.Sprintf("%s/resources/%s/data", vba.baseUrl, url.PathEscape(datasetID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	reqBody := map[string]any{
		"filter_condition": filterCondition,
	}

	headers := vba.buildHeaders(ctx)
	headers[oteltrace.HTTP_HEADER_METHOD_OVERRIDE] = http.MethodDelete
	respCode, respData, err := vba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, reqBody)
	logger.Debugf("DeleteDatasetDocumentsByQuery finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		safeErr := fmt.Errorf("Vega dependency request failed")
		common.LogSafeError(ctx, "DeleteDatasetDocumentsByQuery failed: "+common.SafeErrorSummary(err), safeErr)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http delete dataset documents by query failed")
		return safeErr
	}

	if respCode != http.StatusNoContent && respCode != http.StatusOK {
		err := fmt.Errorf("DeleteDatasetDocumentsByQuery returned HTTP %d", respCode)
		common.LogSafeError(ctx, "DeleteDatasetDocumentsByQuery failed", err)
		logger.Debugf("DeleteDatasetDocumentsByQuery response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 204 or 200")
		return err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// GetData retrieves data from a Vega resource.
func (vba *vegaBackendAccess) QueryResourceData(ctx context.Context, resourceID string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Query dataset data")
	defer span.End()

	span.SetAttributes(attr.Key("dataset_id").String(resourceID))

	httpUrl := fmt.Sprintf("%s/resources/%s/data", vba.baseUrl, url.PathEscape(resourceID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	request, err := resourceDataQueryRequest(params)
	if err != nil {
		return nil, err
	}
	headers := vba.buildHeaders(ctx)
	headers[oteltrace.HTTP_HEADER_METHOD_OVERRIDE] = http.MethodGet
	respCode, respData, err := vba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, request)
	logger.Debugf("QueryDatasetData finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		safeErr := fmt.Errorf("Vega dependency request failed")
		common.LogSafeError(ctx, "QueryDatasetData failed: "+common.SafeErrorSummary(err), safeErr)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http query dataset data failed")
		return nil, safeErr
	}

	if respCode != http.StatusOK {
		err := fmt.Errorf("QueryDatasetData returned HTTP %d", respCode)
		common.LogSafeError(ctx, "QueryDatasetData failed", err)
		logger.Debugf("QueryDatasetData response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, err
	}

	var response interfaces.DatasetQueryResponse
	// Dynamic resource fields must keep their original JSON number representation.
	if err := vegaResponseJSON.Unmarshal(respData, &response); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal QueryDatasetData response", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal QueryDatasetData response failed")
		return nil, fmt.Errorf("failed to unmarshal QueryDatasetData response: %v", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &response, nil
}

func resourceDataQueryRequest(params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryParams, error) {
	if params == nil {
		return nil, fmt.Errorf("resource data query params are required")
	}
	request := *params
	if request.Paging.Cursor == "" && request.Paging.Mode == "" {
		return nil, fmt.Errorf("resource data paging mode or cursor is required")
	}
	return &request, nil
}

func (vba *vegaBackendAccess) WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Write dataset documents")
	defer span.End()

	span.SetAttributes(attr.Key("dataset_id").String(datasetID))
	span.SetAttributes(attr.Key("documents_count").Int(len(documents)))

	httpUrl := fmt.Sprintf("%s/resources/%s/data", vba.baseUrl, url.PathEscape(datasetID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	headers := vba.buildHeaders(ctx)
	headers[oteltrace.HTTP_HEADER_METHOD_OVERRIDE] = http.MethodPost
	respCode, respData, err := vba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, documents)
	logger.Debugf("WriteDatasetDocuments finished, document count is [%d], response code is [%d], %s",
		len(documents), respCode, common.SafeErrorSummary(err))

	if err != nil {
		safeErr := fmt.Errorf("Vega dependency request failed")
		common.LogSafeError(ctx, "WriteDatasetDocuments failed: "+common.SafeErrorSummary(err), safeErr)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http write dataset documents failed")
		return safeErr
	}

	if respCode != http.StatusCreated && respCode != http.StatusOK {
		err := fmt.Errorf("WriteDatasetDocuments returned HTTP %d", respCode)
		common.LogSafeError(ctx, "WriteDatasetDocuments failed", err)
		logger.Debugf("WriteDatasetDocuments response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201 or 200")
		return err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}
