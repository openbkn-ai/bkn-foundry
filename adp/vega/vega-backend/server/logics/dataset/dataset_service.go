// Package dataset provides Dataset management business logic.
package dataset

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
)

var (
	dsServiceOnce sync.Once
	dsService     interfaces.DatasetService
)

type datasetService struct {
	appSetting *common.AppSetting
	lim        interfaces.LocalIndexManager
	ra         interfaces.ResourceAccess
	cs         interfaces.CatalogService
}

// NewDatasetService creates a new DatasetService.
func NewDatasetService(appSetting *common.AppSetting) interfaces.DatasetService {
	dsServiceOnce.Do(func() {
		dsService = &datasetService{
			appSetting: appSetting,
			lim:        local_index.NewLocalIndexManager(appSetting),
			ra:         logics.RA,
			cs:         catalog.NewCatalogService(appSetting),
		}
	})
	return dsService
}

// Create a new Dataset.
func (ds *datasetService) Create(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset")
	defer span.End()

	// 调用本地索引存储创建 dataset 索引，索引名称为 resource id
	err := ds.lim.CreateIndex(ctx, res.ID, res.SchemaDefinition)
	if err != nil {
		otellog.LogError(ctx, "Create dataset index failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Update a Dataset.
func (ds *datasetService) Update(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update dataset")
	defer span.End()

	// 调用本地索引存储更新 dataset 索引，保留历史索引名规则：<res.source_identifier>-<id>
	if err := ds.lim.UpdateIndex(ctx, fmt.Sprintf("%s-%s", res.SourceIdentifier, res.ID), res.SchemaDefinition); err != nil {
		span.SetStatus(codes.Error, "Update dataset failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Delete a Dataset.
func (ds *datasetService) Delete(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset")
	defer span.End()

	// Check dataset exist first
	exist, err := ds.lim.CheckExist(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if exist {
		// Delete from storage
		if err := ds.lim.DeleteIndex(ctx, id); err != nil {
			span.SetStatus(codes.Error, "Delete dataset failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExist checks if a dataset exists.
func (ds *datasetService) CheckExist(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check dataset exist")
	defer span.End()

	exist, err := ds.lim.CheckExist(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return exist, nil
}

// ListDocuments 列出 dataset 中的文档
func (ds *datasetService) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List dataset documents")
	defer span.End()

	// 调用本地索引存储列出文档
	documents, total, err := ds.lim.ListDocuments(ctx, indexName, res, params)
	if err != nil {
		span.SetStatus(codes.Error, "List dataset documents failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return documents, total, nil
}

// CreateDocuments 批量创建 dataset 文档
func (ds *datasetService) CreateDocuments(ctx context.Context, id string, documents []map[string]any) ([]string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset documents")
	defer span.End()

	// 调用本地索引存储批量创建文档
	docIDs, err := ds.lim.CreateDocuments(ctx, id, documents)
	if err != nil {
		span.SetStatus(codes.Error, "Create dataset documents failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return docIDs, nil
}

// GetDocument 获取 dataset 文档
func (ds *datasetService) GetDocument(ctx context.Context, id string, docID string) (map[string]any, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get dataset document")
	defer span.End()

	// 调用本地索引存储获取文档
	document, err := ds.lim.GetDocument(ctx, id, docID)
	if err != nil {
		span.SetStatus(codes.Error, "Get dataset document failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return document, nil
}

// DeleteDocument 删除 dataset 文档
func (ds *datasetService) DeleteDocument(ctx context.Context, id string, docID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset document")
	defer span.End()

	// 调用本地索引存储删除文档
	if err := ds.lim.DeleteDocument(ctx, id, docID); err != nil {
		span.SetStatus(codes.Error, "Delete dataset document failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpsertDocuments 批量更新 dataset 文档
func (ds *datasetService) UpsertDocuments(ctx context.Context, id string, updateRequests []map[string]any) ([]string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update dataset documents")
	defer span.End()

	// 调用本地索引存储批量更新文档
	docIDs, err := ds.lim.UpsertDocuments(ctx, id, updateRequests)
	if err != nil {
		span.SetStatus(codes.Error, "Update dataset documents failed")
		return docIDs, err
	}

	span.SetStatus(codes.Ok, "")
	return docIDs, nil
}

// DeleteDocuments 批量删除 dataset 文档
func (ds *datasetService) DeleteDocuments(ctx context.Context, id string, docIDs string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents")
	defer span.End()

	// 调用本地索引存储批量删除文档
	if err := ds.lim.DeleteDocuments(ctx, id, docIDs); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocumentsByQuery 批量删除 dataset 文档
func (ds *datasetService) DeleteDocumentsByQuery(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents by query")
	defer span.End()

	// 调用本地索引存储批量删除文档
	if err := ds.lim.DeleteDocumentsByQuery(ctx, indexName, res, params); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
