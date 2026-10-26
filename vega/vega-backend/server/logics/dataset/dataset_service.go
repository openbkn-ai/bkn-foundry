// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package dataset provides Dataset management business logic.
package dataset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/local_index"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/model_factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/query_error"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

var (
	dsServiceOnce sync.Once
	dsService     interfaces.DatasetService
)

type datasetService struct {
	appSetting *common.AppSetting
	lim        interfaces.LocalIndexManager
	mfs        interfaces.ModelFactoryService
	ra         interfaces.ResourceAccess
	rs         interfaces.ResourceService
}

// NewDatasetService creates a new DatasetService.
func NewDatasetService(appSetting *common.AppSetting) interfaces.DatasetService {
	dsServiceOnce.Do(func() {
		service := &datasetService{
			appSetting: appSetting,
			lim:        local_index.NewLocalIndexManager(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
			ra:         logics.RA,
		}
		service.rs = resourcelogic.NewResourceService(appSetting, service)
		dsService = service
	})
	return dsService
}

// Create a new Dataset.
func (ds *datasetService) Create(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset")
	defer span.End()

	indexID, err := uuid.NewV7()
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("generate dataset index UUIDv7 failed")
	}
	indexName := fmt.Sprintf("%s-%s", interfaces.DatasetIndexPrefix, indexID)
	resourcelogic.InitializeJSONIndexAttributes(res.SchemaDefinition)
	err = ds.lim.CreateIndex(ctx, indexName, res.SchemaDefinition, map[string]string{"resource_id": res.ID})
	if err != nil {
		otellog.LogError(ctx, "Create dataset index failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}
	res.LocalIndexName = indexName
	res.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable

	span.SetStatus(codes.Ok, "")
	return nil
}

// Update a Dataset.
func (ds *datasetService) Update(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update dataset")
	defer span.End()

	if err := ds.lim.UpdateIndex(ctx, res.LocalIndexName, res.SchemaDefinition); err != nil {
		span.SetStatus(codes.Error, "Update dataset failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Delete a Dataset.
func (ds *datasetService) Delete(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset")
	defer span.End()

	// Check dataset exist first
	exist, err := ds.lim.CheckIndexExist(ctx, res.LocalIndexName)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if exist {
		// Delete from storage
		if err := ds.lim.DeleteIndex(ctx, res.LocalIndexName); err != nil {
			span.SetStatus(codes.Error, "Delete dataset failed")
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// ListDocuments lists the documents in the dataset
func (ds *datasetService) ListDocuments(ctx context.Context, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List dataset documents")
	defer span.End()

	// Call the local index store to list the documents
	documents, total, err := ds.lim.ListDocuments(ctx, res.LocalIndexName, res, params)
	if err != nil {
		span.SetStatus(codes.Error, "List dataset documents failed")
		if httpErr := query_error.KeywordLimitHTTPError(ctx, err); httpErr != nil {
			return nil, 0, httpErr
		}
		if reason, ok := interfaces.RequestSideQueryError(err); ok {
			return nil, 0, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
				WithErrorDetails(reason)
		}
		return nil, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return documents, total, nil
}

// CountDocuments 返回 Dataset 当前本地索引中的文档总数。
func (ds *datasetService) CountDocuments(ctx context.Context, res *interfaces.Resource) (int64, error) {
	_, total, err := ds.ListDocuments(ctx, res, &interfaces.ResourceDataQueryParams{
		Paging:    interfaces.PagingRequest{Limit: 1},
		NeedTotal: true,
	})
	return total, err
}

// GetDocuments retrieves documents in input order. With ignoreMissing enabled,
// a missing document is represented by a nil entry at the same position.
func (ds *datasetService) GetDocuments(ctx context.Context, res *interfaces.Resource, docIDs []string, ignoreMissing bool) ([]map[string]any, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get dataset documents")
	defer span.End()

	if err := ds.rs.CheckResourcePermission(ctx, res.ID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return nil, err
	}
	documents, err := ds.getDocuments(ctx, res, docIDs, ignoreMissing)
	if err != nil {
		span.SetStatus(codes.Error, "Get dataset documents failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return documents, nil
}

func (ds *datasetService) getDocuments(ctx context.Context, res *interfaces.Resource, docIDs []string, ignoreMissing bool) ([]map[string]any, error) {
	documents := make([]map[string]any, len(docIDs))
	loadedDocuments, err := ds.lim.GetDocuments(ctx, res.LocalIndexName, docIDs)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if len(loadedDocuments) != len(docIDs) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails("local index returned a document count different from the requested IDs")
	}
	for i, document := range loadedDocuments {
		if document == nil && !ignoreMissing {
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
				WithErrorDetails(fmt.Sprintf("document %s not found", docIDs[i]))
		}
		documents[i] = document
	}

	return documents, nil
}

// CreateDocument materializes a Dataset document and writes it only after all
// vector work has completed successfully.
func (ds *datasetService) CreateDocument(ctx context.Context, res *interfaces.Resource, document map[string]any) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset document")
	defer span.End()

	if err := ds.rs.CheckResourcePermission(ctx, res.ID, interfaces.OPERATION_TYPE_DATA_WRITE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return "", err
	}
	if _, exists := document["_id"]; exists {
		return "", invalidDocumentError(ctx, "document id is generated by the service; use PUT with a path document id instead")
	}
	materialized, err := ds.materializeDocument(ctx, res, document)
	if err != nil {
		span.SetStatus(codes.Error, "Materialize dataset document failed")
		return "", err
	}
	docIDs, err := ds.lim.CreateDocuments(ctx, res.LocalIndexName, []map[string]any{materialized})
	if err != nil {
		span.SetStatus(codes.Error, "Create dataset document failed")
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}
	if len(docIDs) != 1 {
		span.SetStatus(codes.Error, "Create dataset document returned no id")
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("local index did not return a document ID")
	}
	span.SetStatus(codes.Ok, "")
	return docIDs[0], nil
}

// ReplaceDocument performs full document replacement for path-based Dataset PUT.
func (ds *datasetService) ReplaceDocument(ctx context.Context, res *interfaces.Resource, docID string, document map[string]any) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Replace dataset document")
	defer span.End()

	if err := ds.rs.CheckResourcePermission(ctx, res.ID, interfaces.OPERATION_TYPE_DATA_WRITE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	materialized, err := ds.materializeDocument(ctx, res, document)
	if err != nil {
		span.SetStatus(codes.Error, "Materialize dataset document failed")
		return err
	}
	results, err := ds.lim.IndexDocuments(ctx, res.LocalIndexName, []interfaces.DocumentWrite{{ID: docID, Document: materialized}})
	if err != nil || len(results) != 1 || results[0].ID != docID || results[0].Status < 200 || results[0].Status >= 300 {
		span.SetStatus(codes.Error, "Replace dataset document failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).WithErrorDetails("dataset document indexing failed")
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocuments authorizes, validates existence, and deletes Dataset documents.
func (ds *datasetService) DeleteDocuments(ctx context.Context, res *interfaces.Resource, docIDs []string, ignoreMissing bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents")
	defer span.End()

	if err := ds.rs.CheckResourcePermission(ctx, res.ID, interfaces.OPERATION_TYPE_DATA_WRITE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	documents, err := ds.getDocuments(ctx, res, docIDs, ignoreMissing)
	if err != nil {
		span.SetStatus(codes.Error, "Get dataset documents failed")
		return err
	}
	if len(documents) != len(docIDs) {
		span.SetStatus(codes.Error, "Get dataset documents returned inconsistent count")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails("dataset document count does not match requested IDs")
	}
	idsToDelete := make([]string, 0, len(docIDs))
	for i, document := range documents {
		if document != nil {
			idsToDelete = append(idsToDelete, docIDs[i]) //nolint:gosec // Document count is checked against docIDs above.
		}
	}
	if len(idsToDelete) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	// Call the local index store to batch delete documents
	if err := ds.lim.DeleteDocuments(ctx, res.LocalIndexName, idsToDelete); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocumentsByQuery for batch deletion of dataset documents
func (ds *datasetService) DeleteDocumentsByQuery(ctx context.Context, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.DeleteByQueryResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents by query")
	defer span.End()

	if err := ds.rs.CheckResourcePermission(ctx, res.ID, interfaces.OPERATION_TYPE_DATA_WRITE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return nil, err
	}
	if params == nil || params.FilterCondCfg == nil {
		span.SetStatus(codes.Error, "Delete dataset documents rejected without filter")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("delete-by-query requires a filter condition")
	}
	if err := filter_condition.NormalizeValueFrom(params.FilterCondCfg); err != nil {
		span.SetStatus(codes.Error, "Validate dataset delete condition failed")
		errorCode := verrors.VegaBackend_InvalidParameter_FilterConditionValueFrom
		switch {
		case errors.Is(err, filter_condition.ErrMissingOperation):
			errorCode = verrors.VegaBackend_NullParameter_FilterConditionOperation
		case errors.Is(err, filter_condition.ErrUnsupportedOperation):
			errorCode = verrors.VegaBackend_UnsupportFilterConditionOperation
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, errorCode).
			WithErrorDetails(err.Error())
	}
	querySchema := local_index.SchemaForQuery(res.SchemaDefinition)
	fieldMap := make(map[string]*interfaces.Property, len(querySchema))
	for _, prop := range querySchema {
		if prop != nil {
			fieldMap[prop.Name] = prop
		}
	}
	for name, prop := range local_index.GeneratedFields(res.SchemaDefinition) {
		fieldMap[name] = prop
	}
	actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
	if err != nil {
		var zoneErr *interfaces.ServerTimeZoneError
		if errors.As(err, &zoneErr) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).WithErrorDetails(zoneErr.Error())
		}

		span.SetStatus(codes.Error, "Build dataset delete condition failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	if actualFilterCond == nil {
		span.SetStatus(codes.Error, "Delete dataset documents rejected with empty filter")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("delete-by-query requires a non-empty filter condition")
	}
	if !hasEffectiveDeleteFilter(actualFilterCond) {
		span.SetStatus(codes.Error, "Delete dataset documents rejected with ineffective filter")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("delete-by-query requires a non-empty filter condition")
	}
	params.ActualFilterCond = actualFilterCond
	// Call the local index store to batch delete documents
	result, err := ds.lim.DeleteDocumentsByQuery(ctx, res.LocalIndexName, res, params)
	if err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		var incomplete *interfaces.DeleteByQueryIncompleteError
		if errors.As(err, &incomplete) {
			return result, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Dataset_DeleteIncomplete).WithErrorDetails(incomplete.Result)
		}
		if reason, ok := interfaces.RequestSideQueryError(err); ok {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
				WithErrorDetails(reason)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails("delete-by-query failed; completion is unknown")
	}

	span.SetStatus(codes.Ok, "")
	return result, nil
}

func hasEffectiveDeleteFilter(condition interfaces.FilterCondition) bool {
	if condition == nil {
		return false
	}
	switch typed := condition.(type) {
	case *filter_condition.AndCond:
		if len(typed.SubConds) == 0 {
			return false
		}
		for _, subCondition := range typed.SubConds {
			if !hasEffectiveDeleteFilter(subCondition) {
				return false
			}
		}
	case *filter_condition.OrCond:
		if len(typed.SubConds) == 0 {
			return false
		}
		for _, subCondition := range typed.SubConds {
			if !hasEffectiveDeleteFilter(subCondition) {
				return false
			}
		}
	}
	return true
}

// BulkReplaceDocuments 使用调用方已完成存在性、类型校验及 data_write 鉴权的 Dataset；文档条数由调用方校验。
func (ds *datasetService) BulkReplaceDocuments(ctx context.Context, res *interfaces.Resource,
	documents []map[string]any) ([]interfaces.DocumentWriteResult, error) {

	seen := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		value, exists := document["_id"]
		if !exists {
			return nil, invalidDocumentError(ctx, "document id is required")
		}
		id, ok := value.(string)
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			return nil, invalidDocumentError(ctx, "duplicate document id in bulk request")
		}
		seen[id] = struct{}{}
	}

	writes, err := ds.materializeDocuments(ctx, res, documents)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusBadRequest &&
			httpErr.ErrorCode == verrors.VegaBackend_InvalidParameter_RequestBody {
			return nil, httpErr
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).WithErrorDetails("materialize dataset documents failed")
	}

	return ds.lim.IndexDocuments(ctx, res.LocalIndexName, writes)
}
